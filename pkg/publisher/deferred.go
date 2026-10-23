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
func NewDeferred() *Deferred {
	return &Deferred{}
}

// Set installs the built publisher. Publishes issued after this call use it.
func (d *Deferred) Set(p TaskPublisher) {
	d.inner.Store(&p)
}

// Ready reports whether Set has been called.
func (d *Deferred) Ready() bool {
	return d.inner.Load() != nil
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
