// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"testing"

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

func TestPublishChanges(t *testing.T) {
	tests := []struct {
		name        string
		emittedHead string
		current     string
		emitErr     error
		factoryErr  error
		wantHead    string
		wantCalls   int
	}{
		{
			name:        "advances the baseline once the batch published",
			emittedHead: "aaaaaaa1",
			current:     "bbbbbbb2",
			wantHead:    "bbbbbbb2",
			wantCalls:   1,
		},
		{
			name:        "keeps the baseline when publishing fails, so the diff retries",
			emittedHead: "aaaaaaa1",
			current:     "bbbbbbb2",
			emitErr:     errors.New("kafka unavailable"),
			wantHead:    "aaaaaaa1",
			wantCalls:   1,
		},
		{
			name:        "adopts the first head seen instead of replaying history",
			emittedHead: "",
			current:     "bbbbbbb2",
			wantHead:    "bbbbbbb2",
			wantCalls:   0,
		},
		{
			name:        "keeps an empty baseline while the tree has no readable head",
			emittedHead: "",
			current:     "",
			wantHead:    "",
			wantCalls:   0,
		},
		{
			name:        "publishes nothing when the head did not move",
			emittedHead: "aaaaaaa1",
			current:     "aaaaaaa1",
			wantHead:    "aaaaaaa1",
			wantCalls:   0,
		},
		{
			name:        "keeps the baseline when the producer cannot be built",
			emittedHead: "aaaaaaa1",
			current:     "bbbbbbb2",
			factoryErr:  errors.New("broker unreachable"),
			wantHead:    "aaaaaaa1",
			wantCalls:   0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			emitter := &fakeEmitter{err: test.emitErr}
			publisher := newPublisher(emitter, test.factoryErr)

			got := publishChanges(context.Background(), publisher, test.emittedHead, test.current)

			if got != test.wantHead {
				t.Errorf("baseline = %q, want %q", got, test.wantHead)
			}
			if emitter.calls != test.wantCalls {
				t.Errorf("emit calls = %d, want %d", emitter.calls, test.wantCalls)
			}
		})
	}
}

func TestPublishChangesBuildsTheProducerLazily(t *testing.T) {
	// The producer must not be built until there is something to publish: a broker
	// outage then costs events, never the read path.
	built := 0
	emitter := &fakeEmitter{}
	publisher := &changePublisher{
		factory: func(_ context.Context) (vaultapi.Emitter, func() error, error) {
			built++
			return emitter, func() error { return nil }, nil
		},
	}

	publishChanges(context.Background(), publisher, "aaaaaaa1", "aaaaaaa1")
	if built != 0 {
		t.Fatalf("producer built %d times with no change to publish, want 0", built)
	}

	publishChanges(context.Background(), publisher, "aaaaaaa1", "bbbbbbb2")
	if built != 1 {
		t.Fatalf("producer built %d times on a real change, want 1", built)
	}

	// A second change reuses the producer rather than rebuilding it.
	publishChanges(context.Background(), publisher, "bbbbbbb2", "ccccccc3")
	if built != 1 {
		t.Fatalf("producer rebuilt on the second change (%d builds), want 1", built)
	}
}

func TestPublishChangesNilPublisher(t *testing.T) {
	// No brokers configured: the loop still runs, it just publishes nothing.
	if got := publishChanges(context.Background(), nil, "aaaaaaa1", "bbbbbbb2"); got != "aaaaaaa1" {
		t.Fatalf("baseline = %q, want %q", got, "aaaaaaa1")
	}
}
