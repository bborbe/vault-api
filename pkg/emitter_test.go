// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"errors"

	lib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

const probeIdentifier = "11111111-1111-1111-1111-111111111111"

// fakeDiffer stands in for git: the emitter's job is deciding what to publish,
// and the real diff-tree behaviour is covered by the GitDiffer specs.
type fakeDiffer struct {
	changes []vaultapi.Change
	diffErr error

	readAtContent []byte
	readAtErr     error
	readAtHead    string
	readAtPath    string

	diffCalls int
}

func (f *fakeDiffer) Diff(_ context.Context, _ string, _ string) ([]vaultapi.Change, error) {
	f.diffCalls++
	return f.changes, f.diffErr
}

func (f *fakeDiffer) ReadAt(_ context.Context, head string, path string) ([]byte, error) {
	f.readAtHead = head
	f.readAtPath = path
	return f.readAtContent, f.readAtErr
}

// fakePublisher records every publish so the specs can assert counts, which is
// what makes a never-publishing build fail them.
type fakePublisher struct {
	changed    []lib.Task
	deleted    []lib.TaskIdentifier
	changedErr error
}

func (f *fakePublisher) PublishChanged(_ context.Context, task lib.Task) error {
	f.changed = append(f.changed, task)
	return f.changedErr
}

func (f *fakePublisher) PublishDeleted(_ context.Context, id lib.TaskIdentifier) error {
	f.deleted = append(f.deleted, id)
	return nil
}

var _ = Describe("Emitter", func() {
	var (
		ctx       context.Context
		differ    *fakeDiffer
		publisher *fakePublisher
		emitter   vaultapi.Emitter
	)

	taskFile := "---\ntask_identifier: " + probeIdentifier + "\nstatus: hold\n---\n# Body\n"

	BeforeEach(func() {
		ctx = context.Background()
		differ = &fakeDiffer{}
		publisher = &fakePublisher{}
		emitter = vaultapi.NewEmitter(vaultapi.Config{
			VaultPath: "/vault",
			VaultName: "private-personal",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, differ, publisher)
	})

	Describe("Emit", func() {
		It("publishes exactly one changed event for one changed task file", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeModified},
			}
			differ.readAtContent = []byte(taskFile)

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).To(Succeed())

			Expect(publisher.changed).To(HaveLen(1))
			Expect(
				publisher.changed[0].TaskIdentifier,
			).To(Equal(lib.TaskIdentifier(probeIdentifier)))
			Expect(publisher.changed[0].Vault).To(Equal("private-personal"))
			Expect(string(publisher.changed[0].Content)).To(Equal("# Body\n"))
			Expect(publisher.deleted).To(BeEmpty())
		})

		It("reads the new bytes at the diffed commit, not the working tree", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeAdded},
			}
			differ.readAtContent = []byte(taskFile)

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).To(Succeed())

			Expect(differ.readAtHead).To(Equal("bbbbbbb2"))
			Expect(differ.readAtPath).To(Equal("25 Tasks/Probe Task.md"))
		})

		It("publishes nothing when the two heads are identical", func() {
			Expect(emitter.Emit(ctx, "aaaaaaa1", "aaaaaaa1")).To(Succeed())

			Expect(differ.diffCalls).To(Equal(0))
			Expect(publisher.changed).To(BeEmpty())
			Expect(publisher.deleted).To(BeEmpty())
		})

		It("publishes no task event when only a goals file changed", func() {
			differ.changes = []vaultapi.Change{
				{Path: "24 Goals/Some Goal.md", Status: vaultapi.ChangeModified},
			}

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).To(Succeed())

			Expect(publisher.changed).To(BeEmpty())
			Expect(publisher.deleted).To(BeEmpty())
		})

		It("publishes a tombstone keyed by the identifier read from the old commit", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeDeleted},
			}
			differ.readAtContent = []byte(taskFile)

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).To(Succeed())

			Expect(differ.readAtHead).To(Equal("aaaaaaa1"))
			Expect(differ.readAtPath).To(Equal("25 Tasks/Probe Task.md"))
			Expect(publisher.deleted).To(Equal([]lib.TaskIdentifier{probeIdentifier}))
			Expect(publisher.changed).To(BeEmpty())
		})

		It("skips a file whose frontmatter does not parse without failing the batch", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Broken.md", Status: vaultapi.ChangeModified},
			}
			differ.readAtContent = []byte("no frontmatter here")

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).To(Succeed())
			Expect(publisher.changed).To(BeEmpty())
		})

		It("returns a read error so the caller retries instead of dropping the event", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeModified},
			}
			differ.readAtErr = errors.New("object missing")

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).NotTo(Succeed())
			Expect(publisher.changed).To(BeEmpty())
		})

		It("returns the publish error so the caller keeps its baseline unadvanced", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeModified},
			}
			differ.readAtContent = []byte(taskFile)
			publisher.changedErr = errors.New("kafka unavailable")

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).NotTo(Succeed())
		})

		It("stops the batch when the context is cancelled", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeModified},
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeModified},
			}
			differ.readAtContent = []byte(taskFile)
			cancelled, cancel := context.WithCancel(ctx)
			cancel()

			Expect(emitter.Emit(cancelled, "aaaaaaa1", "bbbbbbb2")).NotTo(Succeed())
			Expect(publisher.changed).To(BeEmpty())
		})

		It("falls back to the path slug when the file carries no task_identifier", func() {
			differ.changes = []vaultapi.Change{
				{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeAdded},
			}
			differ.readAtContent = []byte("---\nstatus: hold\n---\n# Body\n")

			Expect(emitter.Emit(ctx, "aaaaaaa1", "bbbbbbb2")).To(Succeed())

			Expect(publisher.changed).To(HaveLen(1))
			Expect(publisher.changed[0].TaskIdentifier).To(Equal(lib.TaskIdentifier("probe-task")))
		})
	})
})
