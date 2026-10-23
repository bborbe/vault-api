// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

// fakeEmitter records every Emit call so the specs can assert counts, which is
// what makes a never-publishing build fail them.
type fakeEmitter struct {
	calls int
	err   error
}

func (f *fakeEmitter) Emit(_ context.Context, _ string, _ string) error {
	f.calls++
	return f.err
}

func newFeed(emitter vaultapi.Emitter, factoryErr error) vaultapi.Feed {
	return vaultapi.NewFeed(func(_ context.Context) (vaultapi.Emitter, func() error, error) {
		if factoryErr != nil {
			return nil, nil, factoryErr
		}
		return emitter, func() error { return nil }, nil
	}, nil)
}

var _ = Describe("Feed", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("Advance returns the baseline to carry forward",
		func(baseline, head string, emitErr, factoryErr error, want string, wantCalls int) {
			emitter := &fakeEmitter{err: emitErr}
			feed := newFeed(emitter, factoryErr)

			// Adopt the baseline first when the case starts from one: adoption is
			// not a publish, so it must not count toward the expected calls.
			if baseline != "" {
				Expect(feed.Advance(ctx, baseline)).To(Equal(baseline))
			}
			Expect(emitter.calls).To(Equal(0))

			Expect(feed.Advance(ctx, head)).To(Equal(want))
			Expect(emitter.calls).To(Equal(wantCalls))
		},
		Entry("advances once the batch published", "aaaaaaa1", "bbbbbbb2", nil, nil, "bbbbbbb2", 1),
		Entry("keeps the baseline when publishing fails, so the diff retries",
			"aaaaaaa1", "bbbbbbb2", errors.New("kafka unavailable"), nil, "aaaaaaa1", 1),
		Entry("adopts the first head seen instead of replaying history", "", "bbbbbbb2", nil, nil, "bbbbbbb2", 0),
		Entry("keeps an empty baseline while the tree has no readable head", "", "", nil, nil, "", 0),
		Entry("publishes nothing when the head did not move", "aaaaaaa1", "aaaaaaa1", nil, nil, "aaaaaaa1", 0),
		Entry("keeps the baseline when the producer cannot be built",
			"aaaaaaa1", "bbbbbbb2", nil, errors.New("broker unreachable"), "aaaaaaa1", 0),
	)

	It("builds the producer lazily, once, and reuses it", func() {
		// The producer must not be built until there is something to publish: a
		// broker outage then costs events, never the read path.
		built := 0
		emitter := &fakeEmitter{}
		feed := vaultapi.NewFeed(func(_ context.Context) (vaultapi.Emitter, func() error, error) {
			built++
			return emitter, func() error { return nil }, nil
		}, nil)

		feed.Advance(ctx, "aaaaaaa1")
		Expect(built).To(Equal(0))

		feed.Advance(ctx, "bbbbbbb2")
		Expect(built).To(Equal(1))

		feed.Advance(ctx, "ccccccc3")
		Expect(built).To(Equal(1))
	})

	It("abandons a poison baseline even while the vault keeps moving", func() {
		// The budget is keyed on the baseline, not the range: if it were keyed on
		// the range, every new commit would reset the counter and an actively
		// edited vault would wedge the stream forever.
		emitter := &fakeEmitter{err: errors.New("message too large")}
		feed := newFeed(emitter, nil)

		Expect(feed.Advance(ctx, "aaaaaaa1")).To(Equal("aaaaaaa1"))
		Expect(feed.Advance(ctx, "bbbbbbb2")).To(Equal("aaaaaaa1"))
		Expect(feed.Advance(ctx, "ccccccc3")).To(Equal("aaaaaaa1"))
		Expect(feed.Advance(ctx, "ddddddd4")).To(Equal("ddddddd4"))
	})

	It("never abandons a transient failure, however long the outage", func() {
		emitter := &fakeEmitter{err: errors.New("dial tcp: connection refused")}
		feed := vaultapi.NewFeed(func(_ context.Context) (vaultapi.Emitter, func() error, error) {
			return emitter, func() error { return nil }, nil
		}, func(error) bool { return false })

		Expect(feed.Advance(ctx, "aaaaaaa1")).To(Equal("aaaaaaa1"))
		for range 5 {
			Expect(feed.Advance(ctx, "bbbbbbb2")).To(Equal("aaaaaaa1"))
		}
		Expect(emitter.calls).To(Equal(5))

		// Once the broker is back the pending range publishes normally.
		emitter.err = nil
		Expect(feed.Advance(ctx, "bbbbbbb2")).To(Equal("bbbbbbb2"))
	})

	It("publishes nothing when no producer is configured", func() {
		feed := vaultapi.NewFeed(nil, nil)
		Expect(feed.Advance(ctx, "bbbbbbb2")).To(Equal("bbbbbbb2"))
		feed.Close()
	})
})
