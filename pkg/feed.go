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
//
// Abandoning a range moves the publish position past it but deliberately leaves
// the persisted baseline before it: the skipped changes are then re-attempted
// after a restart instead of being lost for good.
type Feed interface {
	// Advance publishes the changes from the current publish position up to head
	// and returns the baseline a restart should resume from. That value lags the
	// publish position by exactly the ranges that were abandoned, so a caller that
	// persists it re-attempts them rather than skipping them.
	Advance(ctx context.Context, head string) string
	// Close releases the producer if it was ever built.
	Close()
}

// MaxPublishRetries is the number of attempts a permanently failing baseline gets
// before it is abandoned.
const MaxPublishRetries = 3

// NewFeed creates a Feed over the given factory with a process-local baseline.
// isPermanent classifies an Emit error as one the broker will never accept; nil
// treats every error as permanent.
//
// A process-local baseline cannot survive a restart, so a caller that can persist
// one should use NewFeedWithBaseline instead.
func NewFeed(factory EmitterFactory, isPermanent func(err error) bool) Feed {
	return NewFeedWithBaseline(factory, isPermanent, "")
}

// NewFeedWithBaseline creates a Feed that starts from baseline rather than
// adopting the first HEAD it sees.
//
// An empty baseline is the first run: there is no earlier commit to compare
// against, so adopting the current HEAD is correct and publishes nothing.
func NewFeedWithBaseline(
	factory EmitterFactory,
	isPermanent func(err error) bool,
	baseline string,
) Feed {
	return &feed{factory: factory, isPermanent: isPermanent, baseline: baseline}
}

type feed struct {
	factory     EmitterFactory
	emitter     Emitter
	close       func() error
	isPermanent func(err error) bool

	// baseline is the publish position: the commit the next diff starts from.
	// persisted is what the caller persists and a restart resumes from. They are
	// equal except after an abandonment, where the position must move past the
	// poison range so later changes still publish while the persisted baseline
	// stays before it so a reload re-attempts the range.
	baseline  string
	persisted string

	attempts int
	failedAt string
}

func (f *feed) Advance(ctx context.Context, head string) string {
	if head == "" {
		return f.persisted
	}
	// Adopt the first head seen rather than diffing from nothing: with no earlier
	// commit there is nothing to publish, and starting from "" would replay the
	// vault's entire history.
	//
	// This is only correct on a genuine first run. A restart with no persisted
	// baseline reaches the same branch, and there the adoption is a silent loss:
	// every commit pushed while the process was down is skipped and never
	// published. That is why a caller passes a persisted baseline through
	// NewFeedWithBaseline — with one, this branch is not reached at all.
	if f.baseline == "" {
		f.baseline = head
		f.persisted = head
		return f.persisted
	}
	if head == f.baseline {
		return f.persisted
	}
	emitter := f.get(ctx)
	if emitter == nil {
		return f.persisted
	}
	if err := emitter.Emit(ctx, f.baseline, head); err != nil {
		// Key the budget on the baseline, not on the range: head moves with every
		// commit, so a range key would reset the counter on an actively edited
		// vault and the cap would never fire.
		if f.fail(err) {
			// Abandon the range without persisting the skip. The publish position
			// moves to head so the next change publishes, but the persisted baseline
			// stays put: a restart then re-attempts this range instead of dropping it
			// for good. The next Advance sees a new baseline, so the budget resets on
			// its own — the retained range cannot re-abandon on the first new failure.
			glog.Errorf(
				"abandoned_range_retained range=%s..%s baseline=%s: %v",
				f.baseline,
				head,
				f.persisted,
				err,
			)
			f.baseline = head
			return f.persisted
		}
		glog.Warningf(
			"publish changes %s..%s failed (attempt %d/%d): %v",
			f.baseline,
			head,
			f.attempts,
			MaxPublishRetries,
			err,
		)
		return f.persisted
	}
	f.succeeded()
	f.baseline = head
	f.persisted = head
	return f.persisted
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
