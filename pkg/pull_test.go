// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

// pullWriter records the bytes the pull seam asked it to write.
type pullWriter struct {
	path    string
	content string
	writes  int
}

func (p *pullWriter) WriteFile(_ context.Context, path string, content []byte) error {
	p.path = path
	p.content = string(content)
	p.writes++
	return nil
}

var _ = Describe("ResolveDivergedPull", func() {
	var (
		ctx    context.Context
		writer *pullWriter
		base   vaultapi.Side
		ours   vaultapi.Side
		theirs vaultapi.Side
	)

	BeforeEach(func() {
		ctx = context.Background()
		writer = &pullWriter{}
		base = vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Base\n---\nbody line\n",
			),
			CommitterTime: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
		}
		ours = vaultapi.Side{
			Content: []byte(
				"---\nphase: execution\nstatus: next\ntitle: Base\n---\nbody line\n",
			),
			CommitterTime: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		}
		theirs = vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Changed\n---\nbody line\n",
			),
			CommitterTime: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC),
		}
	})

	It("writes the merged bytes and nothing either side wrote", func() {
		reason, err := vaultapi.ResolveDivergedPull(
			ctx, writer, "25 Tasks/Alpha.md", base, ours, theirs,
		)

		Expect(err).To(BeNil())
		Expect(reason).To(Equal(vaultapi.MergeReason("")))
		Expect(writer.writes).To(Equal(1))
		Expect(writer.path).To(Equal("25 Tasks/Alpha.md"))
		// The merged file carries both one-sided edits: the phase only ours
		// changed and the title only theirs changed. Neither side holds both, so
		// these bytes can only have come from MergeFile.
		Expect(writer.content).To(Equal(
			"---\nphase: execution\nstatus: next\ntitle: Changed\n---\nbody line\n",
		))
		Expect(writer.content).NotTo(Equal(string(ours.Content)))
		Expect(writer.content).NotTo(Equal(string(theirs.Content)))
	})

	It("writes nothing when the merge quarantines", func() {
		theirs = vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Base\n---\nother body\n",
			),
			CommitterTime: time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC),
		}
		ours = vaultapi.Side{
			Content: []byte(
				"---\nphase: planning\nstatus: next\ntitle: Base\n---\nmy body\n",
			),
			CommitterTime: time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC),
		}

		reason, err := vaultapi.ResolveDivergedPull(
			ctx, writer, "25 Tasks/Alpha.md", base, ours, theirs,
		)

		Expect(err).To(BeNil())
		Expect(reason).To(Equal(vaultapi.ReasonBodyConflict))
		Expect(writer.writes).To(Equal(0))
	})
})
