// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

type fakeEmitter struct {
	calls int
	err   error
}

func (f *fakeEmitter) Emit(_ context.Context, _ string, _ string) error {
	f.calls++
	return f.err
}

func newPublisher(emitter vaultapi.Emitter, factoryErr error) *changePublisher {
	return &changePublisher{
		factory: func(_ context.Context) (vaultapi.Emitter, func() error, error) {
			if factoryErr != nil {
				return nil, nil, factoryErr
			}
			return emitter, func() error { return nil }, nil
		},
	}
}

var _ = Describe("publishChanges", func() {
	DescribeTable(
		"returns the baseline to carry forward",
		func(emittedHead, current string, emitErr, factoryErr error, wantHead string, wantCalls int) {
			emitter := &fakeEmitter{err: emitErr}
			publisher := newPublisher(emitter, factoryErr)

			got := publishChanges(context.Background(), publisher, emittedHead, current)

			Expect(got).To(Equal(wantHead))
			Expect(emitter.calls).To(Equal(wantCalls))
		},
		Entry("advances once the batch published", "aaaaaaa1", "bbbbbbb2", nil, nil, "bbbbbbb2", 1),
		Entry("keeps the baseline when publishing fails, so the diff retries",
			"aaaaaaa1", "bbbbbbb2", errors.New("kafka unavailable"), nil, "aaaaaaa1", 1),
		Entry(
			"adopts the first head seen instead of replaying history",
			"",
			"bbbbbbb2",
			nil,
			nil,
			"bbbbbbb2",
			0,
		),
		Entry(
			"keeps an empty baseline while the tree has no readable head",
			"",
			"",
			nil,
			nil,
			"",
			0,
		),
		Entry(
			"publishes nothing when the head did not move",
			"aaaaaaa1",
			"aaaaaaa1",
			nil,
			nil,
			"aaaaaaa1",
			0,
		),
		Entry("keeps the baseline when the producer cannot be built",
			"aaaaaaa1", "bbbbbbb2", nil, errors.New("broker unreachable"), "aaaaaaa1", 0),
	)

	It("builds the producer lazily, once, and reuses it", func() {
		// The producer must not be built until there is something to publish: a
		// broker outage then costs events, never the read path.
		built := 0
		emitter := &fakeEmitter{}
		publisher := &changePublisher{
			factory: func(_ context.Context) (vaultapi.Emitter, func() error, error) {
				built++
				return emitter, func() error { return nil }, nil
			},
		}

		publishChanges(context.Background(), publisher, "aaaaaaa1", "aaaaaaa1")
		Expect(built).To(Equal(0))

		publishChanges(context.Background(), publisher, "aaaaaaa1", "bbbbbbb2")
		Expect(built).To(Equal(1))

		publishChanges(context.Background(), publisher, "bbbbbbb2", "ccccccc3")
		Expect(built).To(Equal(1))
	})

	It("abandons a batch that keeps failing so one poison event cannot wedge the stream", func() {
		emitter := &fakeEmitter{err: errors.New("message too large")}
		publisher := newPublisher(emitter, nil)

		// The first two attempts keep the baseline, so the range is retried.
		Expect(publishChanges(context.Background(), publisher, "aaaaaaa1", "bbbbbbb2")).
			To(Equal("aaaaaaa1"))
		Expect(publishChanges(context.Background(), publisher, "aaaaaaa1", "bbbbbbb2")).
			To(Equal("aaaaaaa1"))

		// The third gives up on the range and moves on, so later changes still publish.
		Expect(publishChanges(context.Background(), publisher, "aaaaaaa1", "bbbbbbb2")).
			To(Equal("bbbbbbb2"))

		emitter.err = nil
		Expect(publishChanges(context.Background(), publisher, "bbbbbbb2", "ccccccc3")).
			To(Equal("ccccccc3"))
	})

	It("never abandons a transient failure, however long the outage", func() {
		emitter := &fakeEmitter{err: errors.New("dial tcp: connection refused")}
		publisher := newPublisher(emitter, nil)
		publisher.isPermanent = func(error) bool { return false }

		for range 5 {
			Expect(publishChanges(context.Background(), publisher, "aaaaaaa1", "bbbbbbb2")).
				To(Equal("aaaaaaa1"))
		}
		Expect(emitter.calls).To(Equal(5))

		// Once the broker is back the pending range publishes normally.
		emitter.err = nil
		Expect(publishChanges(context.Background(), publisher, "aaaaaaa1", "bbbbbbb2")).
			To(Equal("bbbbbbb2"))
	})

	It("publishes nothing when no producer is configured", func() {
		// No brokers configured: the loop still runs, it just publishes nothing.
		Expect(
			publishChanges(context.Background(), nil, "aaaaaaa1", "bbbbbbb2"),
		).To(Equal("aaaaaaa1"))
	})
})
