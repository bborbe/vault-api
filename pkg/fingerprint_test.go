// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

var _ = Describe("PageFingerprint", func() {
	var (
		root string
		cfg  vaultapi.Config
	)

	BeforeEach(func() {
		var err error
		root, err = os.MkdirTemp("", "vault-api-fp")
		Expect(err).NotTo(HaveOccurred())
		for _, dir := range []string{"T", "G", "P"} {
			Expect(os.MkdirAll(filepath.Join(root, dir), 0o750)).To(Succeed())
		}
		cfg = vaultapi.Config{VaultPath: root, TasksDir: "T", GoalsDir: "G", TopicsDir: "P"}
	})

	AfterEach(func() { _ = os.RemoveAll(root) })

	write := func(rel string, mod time.Time) {
		path := filepath.Join(root, rel)
		Expect(os.WriteFile(path, []byte("x"), 0o600)).To(Succeed())
		Expect(os.Chtimes(path, mod, mod)).To(Succeed())
	}

	It("is zero for empty or missing directories", func() {
		cfg.TopicsDir = "missing"
		Expect(vaultapi.PageFingerprint(cfg)).To(Equal(vaultapi.Fingerprint{}))
	})

	It("counts only markdown files across the three directories", func() {
		base := time.Unix(1_700_000_000, 0)
		write("T/a.md", base)
		write("G/b.md", base.Add(time.Minute))
		write("P/c.md", base.Add(2*time.Minute))
		write("T/notes.txt", base.Add(time.Hour))
		Expect(os.MkdirAll(filepath.Join(root, "T", "sub.md"), 0o750)).To(Succeed())
		fp := vaultapi.PageFingerprint(cfg)
		Expect(fp.Count).To(Equal(3))
		Expect(fp.MaxModTime.Equal(base.Add(2 * time.Minute))).To(BeTrue())
	})

	It("moves when a file is edited, created or deleted", func() {
		base := time.Unix(1_700_000_000, 0)
		write("T/a.md", base)
		first := vaultapi.PageFingerprint(cfg)

		write("T/a.md", base.Add(time.Second))
		edited := vaultapi.PageFingerprint(cfg)
		Expect(edited).NotTo(Equal(first))

		write("T/b.md", base)
		created := vaultapi.PageFingerprint(cfg)
		Expect(created).NotTo(Equal(edited))

		Expect(os.Remove(filepath.Join(root, "T", "b.md"))).To(Succeed())
		Expect(vaultapi.PageFingerprint(cfg)).To(Equal(edited))
	})
})
