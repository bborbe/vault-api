// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"

	"github.com/golang/glog"
)

// EmitterFactory builds the change-event producer on demand. It is called at most
// once on success; a failed call is retried on the next change that needs
// publishing, so a broker that comes up late still gets the backlog.
type EmitterFactory func(ctx context.Context) (Emitter, func() error, error)

//counterfeiter:generate -o ../mocks/feed.go --fake-name Feed . Feed

// Feed publishes the changes between the last published commit and the current
// one, and owns the baseline that makes that possible.
//
// The baseline advances only after a batch published, so a failure retries the
// same diff rather than dropping it. A failure the broker will never accept — a
// payload over message.max.bytes — is abandoned after maxPublishRetries attempts
// on the same baseline, because one poison event must not stop every later change
// from publishing. Transient failures are never abandoned.
type Feed interface {
	// Advance publishes the changes from the current baseline up to head and
	// returns the baseline to carry forward.
	Advance(ctx context.Context, head string) string
	// Close releases the producer if it was ever built.
	Close()
}

// MaxPublishRetries is the number of attempts a permanently failing baseline gets
// before it is abandoned.
const MaxPublishRetries = 3

// NewFeed creates a Feed over the given factory. isPermanent classifies an Emit
// error as one the broker will never accept; nil treats every error as permanent.
func NewFeed(factory EmitterFactory, isPermanent func(err error) bool) Feed {
	return &feed{factory: factory, isPermanent: isPermanent}
}

type feed struct {
	factory     EmitterFactory
	emitter     Emitter
	close       func() error
	isPermanent func(err error) bool

	baseline string
	attempts int
	failedAt string
}

func (f *feed) Advance(ctx context.Context, head string) string {
	if head == "" {
		return f.baseline
	}
	// Adopt the first head seen rather than diffing from nothing: with no earlier
	// commit there is nothing to publish, and starting from "" would replay the
	// vault's entire history.
	//
	// Known limitation, accepted: the baseline is process-local, so changes pushed
	// while the process was down are adopted silently and never published.
	// Recovering them needs a persisted baseline or a full re-publish at startup.
	if f.baseline == "" {
		f.baseline = head
		return f.baseline
	}
	if head == f.baseline {
		return f.baseline
	}
	emitter := f.get(ctx)
	if emitter == nil {
		return f.baseline
	}
	if err := emitter.Emit(ctx, f.baseline, head); err != nil {
		// Key the budget on the baseline, not on the range: head moves with every
		// commit, so a range key would reset the counter on an actively edited
		// vault and the cap would never fire.
		if f.fail(err) {
			glog.Errorf(
				"abandoning changes from %s after %d failed attempts; resuming at %s: %v",
				f.baseline,
				MaxPublishRetries,
				head,
				err,
			)
			f.baseline = head
			return f.baseline
		}
		glog.Warningf(
			"publish changes %s..%s failed (attempt %d/%d): %v",
			f.baseline,
			head,
			f.attempts,
			MaxPublishRetries,
			err,
		)
		return f.baseline
	}
	f.succeeded()
	f.baseline = head
	return f.baseline
}

func (f *feed) get(ctx context.Context) Emitter {
	if f == nil || f.factory == nil {
		return nil
	}
	if f.emitter != nil {
		return f.emitter
	}
	emitter, closePublisher, err := f.factory(ctx)
	if err != nil {
		glog.Warningf("change events unavailable, retrying on the next change: %v", err)
		return nil
	}
	f.emitter = emitter
	f.close = closePublisher
	return f.emitter
}

// fail records a failed attempt against the current baseline and reports whether
// it should be abandoned rather than retried again. A transient failure resets the
// counter: an outage of any length must not consume the budget meant for a poison
// message.
func (f *feed) fail(err error) bool {
	if f.isPermanent != nil && !f.isPermanent(err) {
		f.attempts = 0
		f.failedAt = f.baseline
		return false
	}
	if f.failedAt != f.baseline {
		f.failedAt = f.baseline
		f.attempts = 0
	}
	f.attempts++
	return f.attempts >= MaxPublishRetries
}

func (f *feed) succeeded() {
	f.attempts = 0
	f.failedAt = ""
}

func (f *feed) Close() {
	if f == nil || f.close == nil {
		return
	}
	if err := f.close(); err != nil {
		glog.Warningf("close change-event publisher failed: %v", err)
	}
}
