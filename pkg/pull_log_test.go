// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/glog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

// countingWriter accepts every write, so the log assertions never depend on a
// write failing.
type countingWriter struct {
	writes int
}

func (c *countingWriter) WriteFile(_ context.Context, _ string, _ []byte) error {
	c.writes++
	return nil
}

var _ = Describe("ResolveDivergedPull merge log", func() {
	var (
		ctx    context.Context
		lines  []string
		writer *countingWriter
		base   vaultapi.Side
	)

	BeforeEach(func() {
		ctx = context.Background()
		lines = nil
		writer = &countingWriter{}
		vaultapi.MergeLog = func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		}
		base = vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Base\n---\nbody line\n",
			),
			CommitterTime: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
		}
	})

	AfterEach(func() {
		vaultapi.MergeLog = glog.Infof
	})

	It("emits exactly one line naming the path and both committer times", func() {
		ours := vaultapi.Side{
			Content: []byte(
				"---\nphase: execution\nstatus: next\ntitle: Base\n---\nbody line\n",
			),
			CommitterTime: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		}
		theirs := vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Changed\n---\nbody line\n",
			),
			CommitterTime: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC),
		}

		reason, err := vaultapi.ResolveDivergedPull(
			ctx, writer, "25 Tasks/Alpha.md", base, ours, theirs,
		)

		Expect(err).To(BeNil())
		Expect(reason).To(Equal(vaultapi.MergeReason("")))
		Expect(writer.writes).To(Equal(1))
		Expect(lines).To(HaveLen(1))
		Expect(lines[0]).To(HavePrefix(vaultapi.MergeMarker))
		Expect(lines[0]).To(ContainSubstring("25 Tasks/Alpha.md"))
		Expect(lines[0]).To(ContainSubstring("2026-10-22T10:00:00Z"))
		Expect(lines[0]).To(ContainSubstring("2026-10-21T10:00:00Z"))
	})

	It("emits no line when the merge quarantines", func() {
		ours := vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Base\n---\nmy body\n",
			),
			CommitterTime: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		}
		theirs := vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Base\n---\nother body\n",
			),
			CommitterTime: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC),
		}

		reason, err := vaultapi.ResolveDivergedPull(
			ctx, writer, "25 Tasks/Alpha.md", base, ours, theirs,
		)

		Expect(err).To(BeNil())
		Expect(reason).To(Equal(vaultapi.ReasonBodyConflict))
		Expect(writer.writes).To(Equal(0))
		Expect(lines).To(BeEmpty())
	})

	It("emits no line when the sides do not diverge", func() {
		reason, err := vaultapi.ResolveDivergedPull(
			ctx, writer, "25 Tasks/Alpha.md", base, base, base,
		)

		Expect(err).To(BeNil())
		Expect(reason).To(Equal(vaultapi.MergeReason("")))
		Expect(lines).To(BeEmpty())
	})

	It("emits no line when only one side changed", func() {
		theirs := vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Changed\n---\nbody line\n",
			),
			CommitterTime: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC),
		}

		reason, err := vaultapi.ResolveDivergedPull(
			ctx, writer, "25 Tasks/Alpha.md", base, base, theirs,
		)

		Expect(err).To(BeNil())
		Expect(reason).To(Equal(vaultapi.MergeReason("")))
		Expect(writer.writes).To(Equal(1))
		Expect(lines).To(BeEmpty())
	})
})
