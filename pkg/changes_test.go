// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

var _ = Describe("GitDiffer", func() {
	var (
		ctx       context.Context
		vaultPath string
		differ    vaultapi.Differ
	)

	// git runs the same flag form the differ uses (--git-dir/--work-tree) rather
	// than -C, so the fixture and the code under test resolve the repository the
	// same way.
	git := func(args ...string) string {
		base := make([]string, 0, 4+len(args))
		base = append(base, "--git-dir", filepath.Join(vaultPath, ".git"), "--work-tree", vaultPath)
		cmd := exec.Command("git", append(base, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		return strings.TrimSpace(string(out))
	}

	write := func(relPath string, content string) {
		full := filepath.Join(vaultPath, filepath.FromSlash(relPath))
		Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
		Expect(os.WriteFile(full, []byte(content), 0o644)).To(Succeed())
	}

	commit := func(message string) string {
		git("add", "-A")
		git("commit", "-q", "-m", message)
		return git("rev-parse", "HEAD")
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-differ")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(vaultPath) })
		git("init", "-q")
		differ = vaultapi.NewGitDiffer(vaultPath)
	})

	It("reports a modified task file between two commits", func() {
		write("25 Tasks/Probe Task.md", "---\ntask_identifier: abc\nstatus: hold\n---\n# One\n")
		oldHead := commit("add probe")
		write("25 Tasks/Probe Task.md", "---\ntask_identifier: abc\nstatus: hold\n---\n# Two\n")
		newHead := commit("edit probe")

		changes, err := differ.Diff(ctx, oldHead, newHead)
		Expect(err).NotTo(HaveOccurred())
		Expect(changes).To(Equal([]vaultapi.Change{
			{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeModified},
		}))
	})

	It("reports an added file and reads a deleted one from the old commit", func() {
		write("25 Tasks/Probe Task.md", "---\ntask_identifier: abc\nstatus: hold\n---\n# One\n")
		oldHead := commit("add probe")

		Expect(os.Remove(filepath.Join(vaultPath, "25 Tasks/Probe Task.md"))).To(Succeed())
		newHead := commit("delete probe")

		changes, err := differ.Diff(ctx, oldHead, newHead)
		Expect(err).NotTo(HaveOccurred())
		Expect(changes).To(Equal([]vaultapi.Change{
			{Path: "25 Tasks/Probe Task.md", Status: vaultapi.ChangeDeleted},
		}))

		content, err := differ.ReadAt(ctx, oldHead, "25 Tasks/Probe Task.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(ContainSubstring("task_identifier: abc"))
	})

	It("reports nothing for two identical commits", func() {
		write("25 Tasks/Probe Task.md", "---\nstatus: hold\n---\n")
		head := commit("add probe")

		changes, err := differ.Diff(ctx, head, head)
		Expect(err).NotTo(HaveOccurred())
		Expect(changes).To(BeEmpty())
	})

	It("reports a non-ASCII path unquoted so it can match the tasks dir", func() {
		// git C-quotes such a path by default, and a quoted path never matches the
		// tasks-dir prefix — the file would be skipped silently.
		write("25 Tasks/Käse Task.md", "---\nstatus: hold\n---\n# One\n")
		oldHead := commit("add unicode task")
		write("25 Tasks/Käse Task.md", "---\nstatus: hold\n---\n# Two\n")
		newHead := commit("edit unicode task")

		changes, err := differ.Diff(ctx, oldHead, newHead)
		Expect(err).NotTo(HaveOccurred())
		Expect(changes).To(Equal([]vaultapi.Change{
			{Path: "25 Tasks/Käse Task.md", Status: vaultapi.ChangeModified},
		}))
	})
})
