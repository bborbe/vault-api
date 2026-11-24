// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"os"
	"path/filepath"

	markdown "github.com/bborbe/vault-markdown"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

var _ = Describe("LinkIndex", func() {
	var (
		ctx       context.Context
		vaultPath string
		index     *vaultapi.LinkIndex
	)

	// write drops a page into the fixture vault.
	write := func(rel, body string) {
		full := filepath.Join(vaultPath, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(full), 0o700)).To(Succeed())
		Expect(os.WriteFile(full, []byte(body), 0o600)).To(Succeed())
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-links")
		Expect(err).NotTo(HaveOccurred())
		index = vaultapi.NewLinkIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "fixture",
		})
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	// backlinkPaths returns the index's inbound set for a page path.
	backlinkPaths := func(pagePath string) []string {
		refs, err := index.Backlinks(ctx, pagePath)
		Expect(err).NotTo(HaveOccurred())
		out := make([]string, 0, len(refs))
		for _, ref := range refs {
			out = append(out, ref.Path)
		}
		return out
	}

	Describe("Backlinks", func() {
		It("returns the known inbound set for a page with one", func() {
			write("24 Goals/Target Goal.md", "---\npage_type: goal\n---\n\nNo links here.\n")
			write("25 Tasks/Linking Task.md", "---\npage_type: task\n---\n\nSee [[Target Goal]].\n")
			write("50 Knowledge Base/Other.md", "Unrelated.\n")

			Expect(backlinkPaths("24 Goals/Target Goal.md")).
				To(Equal([]string{"25 Tasks/Linking Task.md"}))
		})

		It("returns an empty set rather than nil for a page nobody links", func() {
			write("24 Goals/Target Goal.md", "Alone.\n")

			refs, err := index.Backlinks(ctx, "24 Goals/Target Goal.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(refs).NotTo(BeNil())
			Expect(refs).To(BeEmpty())
		})

		It("matches vault-markdown's Backlinks for every page in the vault", func() {
			// The fixture carries every shape the two implementations can disagree
			// on: an alias, a heading anchor, an embed, a link inside frontmatter, a
			// fenced block, a tilde block, an inline code span, a link with a space
			// before the anchor (which Backlinks does NOT count), a duplicate
			// basename, and a root-level page linking to itself.
			write("Index.md", "# Index\n\n[[Target Goal]] and [[Target Goal|alias]]\n")
			write("24 Goals/Target Goal.md", "---\npage_type: goal\n---\n\n[[Target Goal]]\n")
			write("25 Tasks/Alias Task.md", "See [[Target Goal|the goal]].\n")
			write("25 Tasks/Anchor Task.md", "See [[Target Goal#Impact]].\n")
			write("25 Tasks/Spaced Anchor Task.md", "See [[Target Goal #Impact]].\n")
			write("25 Tasks/Embed Task.md", "![[Target Goal]]\n")
			write(
				"25 Tasks/Frontmatter Task.md",
				"---\ngoals:\n  - '[[Target Goal]]'\n---\n\nBody.\n",
			)
			write("25 Tasks/Fenced Task.md", "```\n[[Target Goal]]\n```\n")
			write("25 Tasks/Tilde Task.md", "~~~\n[[Target Goal]]\n~~~\n")
			write("25 Tasks/Inline Task.md", "Text `[[Target Goal]]` code.\n")
			write("25 Tasks/Dup.md", "Daily note in Tasks.\n")
			write("60 Periodic Notes/Daily/Dup.md", "Daily note in Periodic.\n")
			write("25 Tasks/Links Dup.md", "See [[Dup]].\n")

			Expect(index.Refresh(ctx)).To(Succeed())

			bodies := map[string]string{}
			Expect(filepath.Walk(vaultPath, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() || filepath.Ext(p) != ".md" {
					return nil
				}
				rel, relErr := filepath.Rel(vaultPath, p)
				Expect(relErr).NotTo(HaveOccurred())
				raw, readErr := os.ReadFile(p)
				Expect(readErr).NotTo(HaveOccurred())
				bodies[filepath.ToSlash(rel)] = string(raw)
				return nil
			})).To(Succeed())

			for rel := range bodies {
				name := rel[:len(rel)-len(".md")]
				name = filepath.Base(name)
				want := markdown.Backlinks(bodies, name)
				if want == nil {
					want = []string{}
				}
				got := backlinkPaths(rel)
				if got == nil {
					got = []string{}
				}
				Expect(got).To(Equal(want), "backlinks for %q", rel)
			}
		})

		It("picks up a page written after the first build", func() {
			write("24 Goals/Target Goal.md", "Alone.\n")
			Expect(index.Refresh(ctx)).To(Succeed())
			Expect(backlinkPaths("24 Goals/Target Goal.md")).To(BeEmpty())

			write("25 Tasks/New Linker.md", "See [[Target Goal]].\n")
			Expect(index.Refresh(ctx)).To(Succeed())
			Expect(backlinkPaths("24 Goals/Target Goal.md")).
				To(Equal([]string{"25 Tasks/New Linker.md"}))
		})

		It("drops a page deleted after the first build", func() {
			write("24 Goals/Target Goal.md", "Alone.\n")
			write("25 Tasks/Gone.md", "See [[Target Goal]].\n")
			Expect(index.Refresh(ctx)).To(Succeed())
			Expect(backlinkPaths("24 Goals/Target Goal.md")).To(HaveLen(1))

			Expect(os.Remove(filepath.Join(vaultPath, "25 Tasks", "Gone.md"))).To(Succeed())
			Expect(index.Refresh(ctx)).To(Succeed())
			Expect(backlinkPaths("24 Goals/Target Goal.md")).To(BeEmpty())
		})

		It("refuses a page that is absent or escapes the vault", func() {
			write("24 Goals/Target Goal.md", "Alone.\n")

			_, err := index.Backlinks(ctx, "24 Goals/Missing.md")
			Expect(vaultapi.IsNotFound(err)).To(BeTrue())

			_, err = index.Backlinks(ctx, "../escape.md")
			Expect(vaultapi.IsNotFound(err)).To(BeTrue())

			_, err = index.Backlinks(ctx, "/etc/passwd.md")
			Expect(vaultapi.IsNotFound(err)).To(BeTrue())
		})
	})

	Describe("Page", func() {
		It("returns the body plus the resolved links and embeds", func() {
			write("24 Goals/Target Goal.md", "---\npage_type: goal\n---\n\nGoal body.\n")
			write("25 Tasks/Embedded Task.md", "---\npage_type: task\n---\n\nEmbedded body.\n")
			write(
				"25 Tasks/Page.md",
				"---\npage_type: task\n---\n\n"+
					"See [[Target Goal]] and [[Target Goal#Impact|the goal]].\n\n"+
					"![[Embedded Task]]\n\n"+
					"See [[Missing Page]] too.\n",
			)

			page, err := index.Page(ctx, "25 Tasks/Page.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Vault).To(Equal("fixture"))
			Expect(page.Path).To(Equal("25 Tasks/Page.md"))
			Expect(page.Name).To(Equal("Page"))
			Expect(page.Content).To(ContainSubstring("page_type: task"))
			Expect(page.Links).To(HaveKeyWithValue("Target Goal", "24 Goals/Target Goal.md"))
			Expect(page.Links).NotTo(HaveKey("Missing Page"))
			Expect(page.Embeds).To(HaveKey("Embedded Task"))
			Expect(page.Embeds["Embedded Task"]).To(ContainSubstring("Embedded body."))
			Expect(page.Embeds["Embedded Task"]).NotTo(ContainSubstring("page_type"))
		})

		It("resolves links carried by an embedded body", func() {
			write("24 Goals/Target Goal.md", "Goal body.\n")
			write("25 Tasks/Embedded Task.md", "Embedded links [[Target Goal]].\n")
			write("25 Tasks/Page.md", "![[Embedded Task]]\n")

			page, err := index.Page(ctx, "25 Tasks/Page.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Links).To(HaveKeyWithValue("Target Goal", "24 Goals/Target Goal.md"))
		})

		It("maps a self-link to the page that was requested", func() {
			write("24 Goals/Dup.md", "First copy.\n")
			write("25 Tasks/Dup.md", "Second copy, links [[Dup]].\n")

			page, err := index.Page(ctx, "25 Tasks/Dup.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Links).To(HaveKeyWithValue("Dup", "25 Tasks/Dup.md"))
		})

		It("serves a page that carries no frontmatter", func() {
			write("50 Knowledge Base/Raw.md", "No frontmatter here.\n")

			page, err := index.Page(ctx, "50 Knowledge Base/Raw.md")
			Expect(err).NotTo(HaveOccurred())
			Expect(page.Content).To(Equal("No frontmatter here.\n"))
		})

		It("refuses a page that is absent or escapes the vault", func() {
			write("24 Goals/Target Goal.md", "Alone.\n")

			_, err := index.Page(ctx, "24 Goals/Missing.md")
			Expect(vaultapi.IsNotFound(err)).To(BeTrue())

			_, err = index.Page(ctx, "../escape.md")
			Expect(vaultapi.IsNotFound(err)).To(BeTrue())

			_, err = index.Page(ctx, "24 Goals/Target Goal.txt")
			Expect(vaultapi.IsNotFound(err)).To(BeTrue())
		})
	})

	Describe("LinkRegistry", func() {
		It("resolves a vault by name, case-insensitively, defaulting to the primary", func() {
			registry := vaultapi.NewLinkRegistry(vaultapi.NewLinkIndex(vaultapi.Config{
				VaultPath: vaultPath,
				VaultName: "private-personal",
			}))
			registry.Attach(vaultapi.NewLinkIndex(vaultapi.Config{
				VaultPath: vaultPath,
				VaultName: "seibert-personal",
			}))

			_, ok := registry.For("")
			Expect(ok).To(BeTrue())
			_, ok = registry.For("PRIVATE-PERSONAL")
			Expect(ok).To(BeTrue())
			_, ok = registry.For("seibert-personal")
			Expect(ok).To(BeTrue())
			_, ok = registry.For("unknown")
			Expect(ok).To(BeFalse())
			Expect(registry.Indexes()).To(HaveLen(2))
		})
	})

	Describe("LinkTargets", func() {
		It("splits at the first pipe or hash without trimming", func() {
			Expect(vaultapi.LinkTargets("[[A]] [[B|alias]] [[C#head]] [[D|a#b]]")).
				To(Equal([]string{"A", "B", "C", "D"}))
			// A space before the anchor is part of the target, which is what
			// vault-markdown's Backlinks decides too.
			Expect(vaultapi.LinkTargets("[[E #head]]")).To(Equal([]string{"E "}))
		})

		It("ignores a link inside frontmatter, code or a fence", func() {
			body := "---\nlink: '[[Front]]'\n---\n\n`[[Inline]]`\n\n```\n[[Fenced]]\n```\n\n[[Real]]\n"
			Expect(vaultapi.LinkTargets(body)).To(Equal([]string{"Real"}))
		})
	})
})
