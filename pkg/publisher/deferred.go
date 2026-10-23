// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package publisher

import (
	"context"
	stderrors "errors"
	"sync/atomic"

	lib "github.com/bborbe/agent"
)

// ErrNotReady is returned by a Deferred publisher whose underlying publisher has
// not been built yet. It reports a transient startup state rather than a failure:
// a caller logs it and the next publish after construction succeeds.
var ErrNotReady = stderrors.New("task publisher not ready")

// Deferred is a TaskPublisher that stands in for one built later.
//
// The Kafka producer must not be built on the startup path. NewSyncProducer blocks
// while a broker is unreachable, so constructing it before the HTTP server starts
// would make vault-api's read endpoints depend on Kafka being up — and a write
// endpoint that built it on first use would hang that request instead. Run
// therefore hands out a Deferred immediately and builds the real publisher in the
// background; until construction finishes, publishes report ErrNotReady rather
// than blocking.
//
// A publish that misses this window is not lost: the write itself has already
// landed in git, and the pull path republishes the same change from the commit
// diff, so the stream converges without the caller.
type Deferred struct {
	inner atomic.Pointer[TaskPublisher]
}

// NewDeferred creates a Deferred with no publisher installed. Every publish
// reports ErrNotReady until Set is called.
//
// It returns the concrete type rather than TaskPublisher on purpose: Set and Ready
// exist only on the handle, and a caller holding the interface could not install
// the publisher it is meant to stand in for. Callers that only publish take it as a
// TaskPublisher.
func NewDeferred() *Deferred {
	return &Deferred{}
}

// Set installs the built publisher. Publishes issued after this call use it.
//
// A nil publisher is ignored rather than stored: storing it would put a non-nil
// pointer to a nil interface in the holder, so Ready would report true and the
// next publish would call a method on a nil interface and panic on the request
// path. Leaving the handle not-ready is the honest reading of "nothing installed".
func (d *Deferred) Set(p TaskPublisher) {
	if p == nil {
		return
	}
	d.inner.Store(&p)
}

// Ready reports whether Set has been called.
func (d *Deferred) Ready() bool {
	return d.inner.Load() != nil
}

// discardPublisher accepts every publish and reports success without sending
// anything.
type discardPublisher struct{}

// NewDiscard returns a TaskPublisher that publishes nothing and reports success.
//
// It is what a Deferred holds when change events are switched off by
// configuration. The write path calls the interface unconditionally, so without
// it a deliberately disabled producer would take the not-ready branch and log an
// error on every successful write — noise that reads as a fault and can trip
// error-rate alerting.
func NewDiscard() TaskPublisher {
	return discardPublisher{}
}

func (discardPublisher) PublishChanged(context.Context, lib.Task) error {
	return nil
}

func (discardPublisher) PublishDeleted(context.Context, lib.TaskIdentifier) error {
	return nil
}

// PublishChanged delegates to the installed publisher, or reports ErrNotReady.
func (d *Deferred) PublishChanged(ctx context.Context, task lib.Task) error {
	p := d.inner.Load()
	if p == nil {
		return ErrNotReady
	}
	return (*p).PublishChanged(ctx, task)
}

// PublishDeleted delegates to the installed publisher, or reports ErrNotReady.
func (d *Deferred) PublishDeleted(ctx context.Context, id lib.TaskIdentifier) error {
	p := d.inner.Load()
	if p == nil {
		return ErrNotReady
	}
	return (*p).PublishDeleted(ctx, id)
}
