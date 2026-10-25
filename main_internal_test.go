// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("eventVaultName", func() {
	It("prefers the routing slug when one is configured", func() {
		app := &application{VaultName: "private-personal", EventVaultName: "personal"}

		Expect(app.eventVaultName()).To(Equal("personal"))
	})

	It("falls back to the reported vault name when no slug is configured", func() {
		app := &application{VaultName: "private-personal"}

		Expect(app.eventVaultName()).To(Equal("private-personal"))
	})

	It("treats an explicitly empty slug as unset rather than stamping an empty vault", func() {
		app := &application{VaultName: "personal", EventVaultName: ""}

		Expect(app.eventVaultName()).To(Equal("personal"))
	})
})

// recordingStore records what was written and can be made to fail, so the save
// path and its retry can both be observed without a file.
type recordingStore struct {
	saves []string
	err   error
}

func (s *recordingStore) Load(context.Context) (string, error) { return "", nil }

func (s *recordingStore) Save(_ context.Context, baseline string) error {
	if s.err != nil {
		return s.err
	}
	s.saves = append(s.saves, baseline)
	return nil
}

var _ = Describe("persistBaseline", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("writes a baseline that moved", func() {
		store := &recordingStore{}
		saved := ""

		persistBaseline(ctx, store, "bbbbbbb2", &saved)

		Expect(store.saves).To(Equal([]string{"bbbbbbb2"}))
		Expect(saved).To(Equal("bbbbbbb2"))
	})

	It("writes nothing for an unchanged baseline", func() {
		store := &recordingStore{}
		saved := "bbbbbbb2"

		persistBaseline(ctx, store, "bbbbbbb2", &saved)

		Expect(store.saves).To(BeEmpty())
	})

	It("writes nothing for an empty baseline", func() {
		store := &recordingStore{}
		saved := ""

		persistBaseline(ctx, store, "", &saved)

		Expect(store.saves).To(BeEmpty())
	})

	It("retries a baseline whose save failed", func() {
		// Recording a failed write as saved would suppress every later attempt, and
		// on a quiescent vault the file would stay at the previous commit until the
		// baseline moved again — the failure this guards.
		store := &recordingStore{err: errors.New("volume is read-only")}
		saved := ""

		persistBaseline(ctx, store, "bbbbbbb2", &saved)
		Expect(saved).To(BeEmpty())

		store.err = nil
		persistBaseline(ctx, store, "bbbbbbb2", &saved)

		Expect(store.saves).To(Equal([]string{"bbbbbbb2"}))
		Expect(saved).To(Equal("bbbbbbb2"))
	})
})
