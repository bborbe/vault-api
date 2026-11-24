// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
)

var _ = Describe("Page endpoints", func() {
	const secret = "test-secret"

	var (
		ctx       context.Context
		vaultPath string
		otherPath string
		router    http.Handler
	)

	write := func(root, rel, body string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		Expect(os.MkdirAll(filepath.Dir(full), 0o700)).To(Succeed())
		Expect(os.WriteFile(full, []byte(body), 0o600)).To(Succeed())
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-pages")
		Expect(err).NotTo(HaveOccurred())
		otherPath, err = os.MkdirTemp("", "vault-api-pages-other")
		Expect(err).NotTo(HaveOccurred())

		write(vaultPath, "24 Goals/Target Goal.md", "---\npage_type: goal\n---\n\nGoal body.\n")
		write(vaultPath, "25 Tasks/Linker.md", "See [[Target Goal]].\n")
		write(vaultPath, "25 Tasks/Page.md", "See [[Target Goal]].\n\n![[Target Goal]]\n")
		write(otherPath, "25 Tasks/Other Page.md", "Another vault.\n")

		links := vaultapi.NewLinkRegistry(vaultapi.NewLinkIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "private-personal",
		}))
		links.Attach(vaultapi.NewLinkIndex(vaultapi.Config{
			VaultPath: otherPath,
			VaultName: "seibert-personal",
		}))

		index := vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "private-personal",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, fakeMetrics{})
		Expect(index.Build(ctx, newTestStore())).To(Succeed())

		router = handler.NewHandler(
			ctx, index, newTestStore(), secret, vaultPath, "25 Tasks", "private-personal",
			&fakeWriter{}, libtime.NewCurrentDateTime(), &recordingPublisher{},
			&fakeRefClient{}, links,
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
		Expect(os.RemoveAll(otherPath)).To(Succeed())
	})

	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, bytes.NewBuffer(nil))
		request.Header.Set("X-Gateway-Secret", secret)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	Describe("GET /api/v1/pages/{path}", func() {
		It("returns the body with the resolved links and embeds", func() {
			recorder := call("/api/v1/pages/25%20Tasks/Page.md")
			Expect(recorder.Code).To(Equal(http.StatusOK))

			var body struct {
				Vault   string            `json:"vault"`
				Path    string            `json:"path"`
				Name    string            `json:"name"`
				Content string            `json:"content"`
				Links   map[string]string `json:"links"`
				Embeds  map[string]string `json:"embeds"`
			}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
			Expect(body.Vault).To(Equal("private-personal"))
			Expect(body.Path).To(Equal("25 Tasks/Page.md"))
			Expect(body.Name).To(Equal("Page"))
			Expect(body.Content).To(ContainSubstring("See [[Target Goal]]"))
			Expect(body.Links).To(HaveKeyWithValue("Target Goal", "24 Goals/Target Goal.md"))
			Expect(body.Embeds).To(HaveKey("Target Goal"))
			Expect(body.Embeds["Target Goal"]).To(ContainSubstring("Goal body"))
		})

		It("serves a page in another vault named by the vault query", func() {
			recorder := call("/api/v1/pages/25%20Tasks/Other%20Page.md?vault=seibert-personal")
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("Another vault."))
		})

		It("answers 404 for an unknown vault", func() {
			recorder := call("/api/v1/pages/25%20Tasks/Page.md?vault=nope")
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(recorder.Body.String()).To(ContainSubstring("unknown_vault"))
		})

		It("answers 404 for a missing page", func() {
			recorder := call("/api/v1/pages/25%20Tasks/Missing.md")
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(recorder.Body.String()).To(ContainSubstring("not_found"))
		})
	})

	Describe("GET /api/v1/pages/{path}/backlinks", func() {
		It("returns exactly the known inbound set", func() {
			recorder := call("/api/v1/pages/24%20Goals/Target%20Goal.md/backlinks")
			Expect(recorder.Code).To(Equal(http.StatusOK))

			var body struct {
				Vault     string `json:"vault"`
				Path      string `json:"path"`
				Name      string `json:"name"`
				Backlinks []struct {
					Path string `json:"path"`
					Name string `json:"name"`
				} `json:"backlinks"`
			}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
			Expect(body.Name).To(Equal("Target Goal"))
			Expect(body.Backlinks).To(HaveLen(2))
			Expect(body.Backlinks[0].Path).To(Equal("25 Tasks/Linker.md"))
			Expect(body.Backlinks[0].Name).To(Equal("Linker"))
			Expect(body.Backlinks[1].Path).To(Equal("25 Tasks/Page.md"))
		})

		It("is not swallowed by the page route's greedy path", func() {
			// A page literally named "backlinks.md" and a backlinks request for a
			// different page must both route correctly.
			write(vaultPath, "25 Tasks/backlinks.md", "A page named backlinks.\n")

			recorder := call("/api/v1/pages/24%20Goals/Target%20Goal.md/backlinks")
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring(`"backlinks"`))

			page := call("/api/v1/pages/25%20Tasks/backlinks.md")
			Expect(page.Code).To(Equal(http.StatusOK))
			Expect(page.Body.String()).To(ContainSubstring("A page named backlinks."))
		})

		It("answers 404 for a missing page", func() {
			recorder := call("/api/v1/pages/24%20Goals/Missing.md/backlinks")
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
		})

		It("requires the gateway secret", func() {
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/pages/24%20Goals/Target%20Goal.md/backlinks",
				bytes.NewBuffer(nil),
			)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			Expect(recorder.Code).To(Equal(http.StatusUnauthorized))
		})
	})
})
