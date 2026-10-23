// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

var _ = Describe("ResolveHead", func() {
	var (
		vaultPath string
		gitDir    string
	)

	BeforeEach(func() {
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-head")
		Expect(err).To(BeNil())
		DeferCleanup(func() {
			_ = os.RemoveAll(vaultPath)
		})
		gitDir = filepath.Join(vaultPath, ".git")
		Expect(os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o700)).To(Succeed())
	})

	writeFile := func(path, content string) {
		Expect(os.MkdirAll(filepath.Dir(path), 0o700)).To(Succeed())
		Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
	}

	It("returns the short SHA a symbolic ref points at", func() {
		writeFile(filepath.Join(gitDir, "HEAD"), "ref: refs/heads/main\n")
		writeFile(
			filepath.Join(gitDir, "refs", "heads", "main"),
			"0123456789abcdef0123456789abcdef01234567\n",
		)

		Expect(vaultapi.ResolveHead(vaultPath)).To(Equal("01234567"))
	})

	It("returns the short SHA of a detached HEAD", func() {
		writeFile(filepath.Join(gitDir, "HEAD"), "fedcba9876543210fedcba9876543210fedcba98\n")

		Expect(vaultapi.ResolveHead(vaultPath)).To(Equal("fedcba98"))
	})

	It("refuses a ref that escapes the git directory", func() {
		outside := filepath.Join(vaultPath, "secret")
		writeFile(outside, "leaked\n")
		writeFile(filepath.Join(gitDir, "HEAD"), "ref: ../secret\n")

		Expect(vaultapi.ResolveHead(vaultPath)).To(BeEmpty())
	})

	It("refuses an absolute ref", func() {
		outside := filepath.Join(vaultPath, "secret")
		writeFile(outside, "leaked\n")
		writeFile(filepath.Join(gitDir, "HEAD"), "ref: "+outside+"\n")

		Expect(vaultapi.ResolveHead(vaultPath)).To(BeEmpty())
	})

	It("refuses a ref that is a symlink pointing outside the git directory", func() {
		outside := filepath.Join(vaultPath, "secret")
		writeFile(outside, "leaked\n")
		link := filepath.Join(gitDir, "refs", "heads", "evil")
		Expect(os.Symlink(outside, link)).To(Succeed())
		writeFile(filepath.Join(gitDir, "HEAD"), "ref: refs/heads/evil\n")

		Expect(vaultapi.ResolveHead(vaultPath)).To(BeEmpty())
	})

	It("returns empty when the directory is not a git working tree", func() {
		Expect(vaultapi.ResolveHead(vaultPath)).To(BeEmpty())
	})
})
