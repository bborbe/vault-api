// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

var _ = Describe("BaselineStore", func() {
	var ctx context.Context
	var dir string

	BeforeEach(func() {
		ctx = context.Background()
		dir = GinkgoT().TempDir()
	})

	Context("file store", func() {
		It("reports an empty baseline on the first run rather than failing", func() {
			// A missing file is the normal first run, not an error: failing here would
			// take startup down on a path that is only an optimisation.
			store := vaultapi.NewFileBaselineStore(filepath.Join(dir, "event-baseline"))

			baseline, err := store.Load(ctx)
			Expect(err).To(BeNil())
			Expect(baseline).To(BeEmpty())
		})

		It("round-trips a saved baseline across a new store instance", func() {
			// A second instance stands in for the restart: it must see what the first
			// one saved, which is the whole point of the file.
			path := filepath.Join(dir, "event-baseline")
			Expect(vaultapi.NewFileBaselineStore(path).Save(ctx, "aaaaaaa1")).To(BeNil())

			baseline, err := vaultapi.NewFileBaselineStore(path).Load(ctx)
			Expect(err).To(BeNil())
			Expect(baseline).To(Equal("aaaaaaa1"))
		})

		It("creates a missing parent directory", func() {
			// The path is operator-configured and its directory is not guaranteed to
			// exist; refusing to save there would leave persistence silently off.
			path := filepath.Join(dir, "nested", "deeper", "event-baseline")
			store := vaultapi.NewFileBaselineStore(path)

			Expect(store.Save(ctx, "bbbbbbb2")).To(BeNil())
			baseline, err := store.Load(ctx)
			Expect(err).To(BeNil())
			Expect(baseline).To(Equal("bbbbbbb2"))
		})

		It("does not overwrite a good baseline with an empty one", func() {
			// An empty baseline is the feed's first-run state. Writing it would turn a
			// transient empty read into a permanent gap, which is the defect this
			// store exists to prevent.
			path := filepath.Join(dir, "event-baseline")
			store := vaultapi.NewFileBaselineStore(path)
			Expect(store.Save(ctx, "aaaaaaa1")).To(BeNil())

			Expect(store.Save(ctx, "")).To(BeNil())
			baseline, err := store.Load(ctx)
			Expect(err).To(BeNil())
			Expect(baseline).To(Equal("aaaaaaa1"))
		})

		It("leaves no temporary file behind after a save", func() {
			path := filepath.Join(dir, "event-baseline")
			Expect(vaultapi.NewFileBaselineStore(path).Save(ctx, "aaaaaaa1")).To(BeNil())

			_, err := os.Stat(path + ".tmp")
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Context("noop store", func() {
		It("persists nothing and reports an empty baseline", func() {
			store := vaultapi.NoopBaselineStore()

			Expect(store.Save(ctx, "aaaaaaa1")).To(BeNil())
			baseline, err := store.Load(ctx)
			Expect(err).To(BeNil())
			Expect(baseline).To(BeEmpty())
		})
	})
})

// resolvingDiffer is a Differ whose Exists answer the test controls; Diff and
// ReadAt are unused by ResolveBaseline.
type resolvingDiffer struct {
	exists bool
	err    error
	calls  int
}

func (d *resolvingDiffer) Diff(context.Context, string, string) ([]vaultapi.Change, error) {
	return nil, nil
}

func (d *resolvingDiffer) ReadAt(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func (d *resolvingDiffer) Exists(context.Context, string) (bool, error) {
	d.calls++
	return d.exists, d.err
}

var _ = Describe("ResolveBaseline", func() {
	ctx := context.Background()

	It("keeps a baseline the repository still holds", func() {
		differ := &resolvingDiffer{exists: true}

		Expect(vaultapi.ResolveBaseline(ctx, differ, "aaaaaaa1")).To(Equal("aaaaaaa1"))
		Expect(differ.calls).To(Equal(1))
	})

	It("drops a baseline naming a commit the repository lost", func() {
		// The wedge case: the stored SHA is gone, so diff-tree fails in a way the
		// feed treats as transient and retries forever. Adopting HEAD is the
		// pre-existing behaviour, and it is what keeps publishing alive.
		differ := &resolvingDiffer{exists: false}

		Expect(vaultapi.ResolveBaseline(ctx, differ, "aaaaaaa1")).To(BeEmpty())
	})

	It("drops a baseline it cannot verify, without failing startup", func() {
		differ := &resolvingDiffer{err: errors.New("repository unreadable")}

		Expect(vaultapi.ResolveBaseline(ctx, differ, "aaaaaaa1")).To(BeEmpty())
	})

	It("returns an empty baseline without consulting the repository", func() {
		differ := &resolvingDiffer{exists: true}

		Expect(vaultapi.ResolveBaseline(ctx, differ, "")).To(BeEmpty())
		Expect(differ.calls).To(Equal(0))
	})
})
