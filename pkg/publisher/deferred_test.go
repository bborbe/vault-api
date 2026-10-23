// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package publisher_test

import (
	"context"
	stderrors "errors"

	lib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-api/pkg/publisher"
)

// recordingPublisher records what it was asked to publish. It is defined here
// rather than taken from the generated counterfeiter mocks because mocks/ is
// produced by `make generate` and is not committed, so a test that imported it
// would fail on a checkout where generate had not run.
type recordingPublisher struct {
	changed []lib.Task
	deleted []lib.TaskIdentifier
	err     error
}

func (r *recordingPublisher) PublishChanged(_ context.Context, task lib.Task) error {
	r.changed = append(r.changed, task)
	return r.err
}

func (r *recordingPublisher) PublishDeleted(_ context.Context, id lib.TaskIdentifier) error {
	r.deleted = append(r.deleted, id)
	return r.err
}

var _ = Describe("Deferred", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("reports not ready before a publisher is installed", func() {
		deferred := publisher.NewDeferred()

		Expect(deferred.Ready()).To(BeFalse())
		Expect(deferred.PublishChanged(ctx, lib.Task{})).To(MatchError(publisher.ErrNotReady))
		Expect(deferred.PublishDeleted(ctx, "id")).To(MatchError(publisher.ErrNotReady))
	})

	It("delegates to the installed publisher", func() {
		recorder := &recordingPublisher{}
		deferred := publisher.NewDeferred()
		deferred.Set(recorder)

		Expect(deferred.Ready()).To(BeTrue())
		Expect(deferred.PublishChanged(ctx, lib.Task{TaskIdentifier: "task-1"})).To(Succeed())
		Expect(deferred.PublishDeleted(ctx, "task-2")).To(Succeed())

		Expect(recorder.changed).To(HaveLen(1))
		Expect(recorder.changed[0].TaskIdentifier).To(Equal(lib.TaskIdentifier("task-1")))
		Expect(recorder.deleted).To(Equal([]lib.TaskIdentifier{"task-2"}))
	})

	It("returns the installed publisher's error rather than swallowing it", func() {
		// A caller that cannot see a publish failure cannot retry or report it. The
		// pull path keeps its baseline on any error, so a swallowed error there
		// would drop a change a retry would have delivered.
		publishErr := stderrors.New("broker rejected the message")
		deferred := publisher.NewDeferred()
		deferred.Set(&recordingPublisher{err: publishErr})

		Expect(deferred.PublishChanged(ctx, lib.Task{})).To(MatchError(publishErr))
		Expect(deferred.PublishDeleted(ctx, "id")).To(MatchError(publishErr))
	})

	It("ignores a nil publisher instead of storing it", func() {
		// Storing nil would put a non-nil pointer to a nil interface in the holder:
		// Ready would report true and the next publish would panic on the request
		// path.
		deferred := publisher.NewDeferred()
		deferred.Set(nil)

		Expect(deferred.Ready()).To(BeFalse())
		Expect(deferred.PublishChanged(ctx, lib.Task{})).To(MatchError(publisher.ErrNotReady))
	})

	It("reports success from a discard publisher without recording anything", func() {
		// A disabled producer must not read as a fault: the write path calls the
		// interface on every successful write.
		discard := publisher.NewDiscard()

		Expect(discard.PublishChanged(ctx, lib.Task{TaskIdentifier: "task-1"})).To(Succeed())
		Expect(discard.PublishDeleted(ctx, "task-2")).To(Succeed())
	})

	It("publishes correctly while the builder installs the publisher", func() {
		// The production pattern: the background builder calls Set while requests are
		// already publishing. Race-freedom is the whole point of the atomic holder.
		deferred := publisher.NewDeferred()
		done := make(chan struct{})

		go func() {
			defer close(done)
			for range 100 {
				// Either ErrNotReady or success is correct here; a panic or a data race
				// is not.
				_ = deferred.PublishChanged(ctx, lib.Task{})
			}
		}()
		deferred.Set(&recordingPublisher{})
		Eventually(done).Should(BeClosed())

		Expect(deferred.Ready()).To(BeTrue())
		Expect(deferred.PublishChanged(ctx, lib.Task{TaskIdentifier: "after"})).To(Succeed())
	})

	It("keeps the not-ready error a plain sentinel", func() {
		// A not-ready publish must not read as a permanent broker rejection: a
		// publisher that is merely still starting would then burn a caller's retry
		// budget and drop a change that a retry would have delivered. It is a plain
		// error, not a sarama KError, so any is-permanent classifier reports false.
		Expect(publisher.ErrNotReady.Error()).To(Equal("task publisher not ready"))
		Expect(stderrors.Is(publisher.ErrNotReady, publisher.ErrNotReady)).To(BeTrue())
	})
})
