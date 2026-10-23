// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
)

var _ = Describe("Merge endpoint", func() {
	const secret = "test-secret"

	var (
		ctx       context.Context
		vaultPath string
		index     *vaultapi.Index
		writer    *fakeWriter
		router    http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-merge")
		Expect(err).To(BeNil())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "25 Tasks"), 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Goals"), 0o700)).To(Succeed())

		index = vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, fakeMetrics{})
		writer = &fakeWriter{}
		router = handler.NewHandler(
			ctx, index, secret, vaultPath, writer, libtime.NewCurrentDateTime(),
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	side := func(content, committerTime string) map[string]string {
		return map[string]string{"content": content, "committer_time": committerTime}
	}

	call := func(body map[string]any) *httptest.ResponseRecorder {
		encoded, err := json.Marshal(body)
		Expect(err).To(BeNil())
		request := httptest.NewRequest(
			http.MethodPost, "/api/v1/merge", bytes.NewBuffer(encoded),
		)
		request.Header.Set("X-Gateway-Secret", secret)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	decode := func(recorder *httptest.ResponseRecorder) map[string]any {
		var decoded map[string]any
		Expect(json.Unmarshal(recorder.Body.Bytes(), &decoded)).To(Succeed())
		return decoded
	}

	It("merges a diverged file and writes the merged bytes", func() {
		recorder := call(map[string]any{
			"path": "25 Tasks/Alpha.md",
			"base": side(
				"---\nphase: planning\ntitle: Base\n---\nbody\n",
				"2026-10-20T10:00:00Z",
			),
			"ours": side(
				"---\nphase: execution\ntitle: Base\n---\nbody\n",
				"2026-10-22T10:00:00Z",
			),
			"theirs": side(
				"---\nphase: planning\ntitle: Changed\n---\nbody\n",
				"2026-10-21T10:00:00Z",
			),
		})

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(decode(recorder)["result"]).To(Equal("merged"))
		Expect(writer.calls).To(HaveLen(1))
		Expect(writer.calls[0].path).To(Equal("25 Tasks/Alpha.md"))
		// Both one-sided edits survive, so these bytes can only have come from
		// the merge rather than from either side.
		Expect(writer.calls[0].content).To(ContainSubstring("phase: execution"))
		Expect(writer.calls[0].content).To(ContainSubstring("title: Changed"))
	})

	It("quarantines a body conflict without writing", func() {
		recorder := call(map[string]any{
			"path": "25 Tasks/Alpha.md",
			"base": side(
				"---\ntitle: Base\n---\nbody\n", "2026-10-20T10:00:00Z",
			),
			"ours": side(
				"---\ntitle: Base\n---\nmine\n", "2026-10-22T10:00:00Z",
			),
			"theirs": side(
				"---\ntitle: Base\n---\ntheirs\n", "2026-10-21T10:00:00Z",
			),
		})

		Expect(recorder.Code).To(Equal(http.StatusOK))
		decoded := decode(recorder)
		Expect(decoded["result"]).To(Equal("quarantined"))
		Expect(decoded["reason"]).To(Equal(string(vaultapi.ReasonBodyConflict)))
		Expect(writer.calls).To(BeEmpty())
	})

	It("rejects a committer time that is not RFC3339", func() {
		recorder := call(map[string]any{
			"path":   "25 Tasks/Alpha.md",
			"base":   side("---\ntitle: Base\n---\nbody\n", "yesterday"),
			"ours":   side("---\ntitle: Ours\n---\nbody\n", "2026-10-22T10:00:00Z"),
			"theirs": side("---\ntitle: Theirs\n---\nbody\n", "2026-10-21T10:00:00Z"),
		})

		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		decoded := decode(recorder)
		Expect(decoded["error"]).To(Equal("invalid_body"))
		Expect(decoded["reason"]).To(Equal("base.committer_time must be RFC3339"))
		Expect(writer.calls).To(BeEmpty())
	})

	It("answers 502 when git-rest refuses the write", func() {
		writer.err = errors.New("git-rest answered 409: conflict")

		recorder := call(map[string]any{
			"path": "25 Tasks/Alpha.md",
			"base": side(
				"---\ntitle: Base\n---\nbody\n", "2026-10-20T10:00:00Z",
			),
			"ours": side(
				"---\ntitle: Ours\n---\nbody\n", "2026-10-22T10:00:00Z",
			),
			"theirs": side(
				"---\ntitle: Theirs\n---\nbody\n", "2026-10-21T10:00:00Z",
			),
		})

		Expect(recorder.Code).To(Equal(http.StatusBadGateway))
		Expect(decode(recorder)["error"]).To(Equal("git_rest_failed"))
	})

	It("refuses a request with no gateway secret", func() {
		encoded, err := json.Marshal(map[string]any{
			"path":   "25 Tasks/Alpha.md",
			"base":   side("---\ntitle: Base\n---\nbody\n", "2026-10-20T10:00:00Z"),
			"ours":   side("---\ntitle: Ours\n---\nbody\n", "2026-10-22T10:00:00Z"),
			"theirs": side("---\ntitle: Theirs\n---\nbody\n", "2026-10-21T10:00:00Z"),
		})
		Expect(err).To(BeNil())
		request := httptest.NewRequest(
			http.MethodPost, "/api/v1/merge", bytes.NewBuffer(encoded),
		)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)

		Expect(recorder.Code).To(Equal(http.StatusUnauthorized))
		Expect(writer.calls).To(BeEmpty())
	})

	It("accepts three file contents larger than the write endpoints' cap", func() {
		// The write endpoints cap at 64 KiB because they carry a phase name and
		// an approver. A merge carries three whole files, so a body the write
		// endpoints would reject must still be accepted here.
		bulk := strings.Repeat("x", 100*1024)

		recorder := call(map[string]any{
			"path":   "25 Tasks/Alpha.md",
			"base":   side("---\ntitle: Base\n---\n"+bulk+"\n", "2026-10-20T10:00:00Z"),
			"ours":   side("---\ntitle: Ours\n---\n"+bulk+"\n", "2026-10-22T10:00:00Z"),
			"theirs": side("---\ntitle: Theirs\n---\n"+bulk+"\n", "2026-10-21T10:00:00Z"),
		})

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(decode(recorder)["result"]).To(Equal("merged"))
	})

	It("refuses a path that escapes the vault", func() {
		recorder := call(map[string]any{
			"path":   "25 Tasks/../../etc/passwd",
			"base":   side("---\ntitle: Base\n---\nbody\n", "2026-10-20T10:00:00Z"),
			"ours":   side("---\ntitle: Ours\n---\nbody\n", "2026-10-22T10:00:00Z"),
			"theirs": side("---\ntitle: Theirs\n---\nbody\n", "2026-10-21T10:00:00Z"),
		})

		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(recorder)["reason"]).To(Equal("path must not contain a .. segment"))
		Expect(writer.calls).To(BeEmpty())
	})

	It("reports an omitted side as required, not as a malformed time", func() {
		recorder := call(map[string]any{
			"path":   "25 Tasks/Alpha.md",
			"ours":   side("---\ntitle: Ours\n---\nbody\n", "2026-10-22T10:00:00Z"),
			"theirs": side("---\ntitle: Theirs\n---\nbody\n", "2026-10-21T10:00:00Z"),
		})

		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(recorder)["reason"]).To(Equal("base is required"))
		Expect(writer.calls).To(BeEmpty())
	})

	It("rejects a body with no path", func() {
		recorder := call(map[string]any{
			"base":   side("---\ntitle: Base\n---\nbody\n", "2026-10-20T10:00:00Z"),
			"ours":   side("---\ntitle: Ours\n---\nbody\n", "2026-10-22T10:00:00Z"),
			"theirs": side("---\ntitle: Theirs\n---\nbody\n", "2026-10-21T10:00:00Z"),
		})

		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(recorder)["reason"]).To(Equal("path is required"))
		Expect(writer.calls).To(BeEmpty())
	})
})
