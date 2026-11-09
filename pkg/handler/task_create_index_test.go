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
	"time"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
)

// diskWriter writes the file where a real git-rest client would, so the read
// path serves exactly what the write path stored. The package's fakeWriter only
// records calls, which cannot show whether a read after a write resolves.
type diskWriter struct {
	vaultPath string
	writes    []string
	err       error
}

func (d *diskWriter) WriteFile(_ context.Context, path string, content []byte) error {
	if d.err != nil {
		return d.err
	}
	absolute := filepath.Join(d.vaultPath, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(absolute, content, 0o600); err != nil {
		return err
	}
	d.writes = append(d.writes, path)
	return nil
}

// The create and the write that follows it are one unit of work for every
// caller that addresses a task by the identifier it just created — the
// executor's probe publishes create-task and update-frontmatter back to back.
// These specs pin that pair: no index rebuild runs between the create and the
// read, because a rebuild is exactly what the read path must not depend on.
var _ = Describe("Create then address the created task", func() {
	const secret = "test-secret"

	var (
		ctx       context.Context
		vaultPath string
		tasksDir  string
		index     *vaultapi.Index
		writer    *diskWriter
		router    http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-create-index")
		Expect(err).To(BeNil())
		tasksDir = filepath.Join(vaultPath, "25 Tasks")
		Expect(os.MkdirAll(tasksDir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Goals"), 0o700)).To(Succeed())

		index = vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, fakeMetrics{})

		writer = &diskWriter{vaultPath: vaultPath}
		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(2026, 11, 9, 12, 0, 0, 0, time.UTC))
		router = handler.NewHandler(
			ctx, index, newTestStore(), secret, vaultPath, "25 Tasks", "test", writer, clock,
			&recordingPublisher{}, &fakeRefClient{},
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	call := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		request.Header.Set("X-Gateway-Secret", secret)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	decode := func(recorder *httptest.ResponseRecorder) map[string]any {
		var body map[string]any
		Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
		return body
	}

	It("resolves the created task by identifier, and accepts the write that follows it", func() {
		created := call(
			http.MethodPost,
			"/api/v1/tasks",
			`{"title":"probe-alpha","frontmatter":`+
				`{"task_identifier":"probe-1","status":"in_progress","phase":"planning"}}`,
		)
		Expect(created.Code).To(Equal(http.StatusCreated))
		Expect(writer.writes).To(HaveLen(1))

		// Deliberately no index.Build here: the point is that the create is
		// readable without waiting for the next HEAD-driven rebuild.
		read := call(http.MethodGet, "/api/v1/tasks/probe-1", "")
		Expect(read.Code).To(Equal(http.StatusOK))
		Expect(decode(read)["title"]).To(Equal("probe-alpha"))

		patched := call(
			http.MethodPatch,
			"/api/v1/tasks/probe-1",
			`{"trigger_count":0,"current_job":"","job_started_at":""}`,
		)
		Expect(patched.Code).To(Equal(http.StatusOK))
		frontmatter, ok := decode(patched)["frontmatter"].(map[string]any)
		Expect(ok).To(BeTrue(), "the response carries the frontmatter as an object")
		Expect(frontmatter["trigger_count"]).To(BeNumerically("==", 0))
		Expect(frontmatter["current_job"]).To(Equal(""))
	})

	It("resolves a reopened terminal task by identifier", func() {
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "probe-alpha.md"),
			[]byte("---\ntask_identifier: probe-1\nstatus: completed\n---\nold\n"),
			0o600,
		)).To(Succeed())
		// The index is deliberately NOT built over this file. A create whose
		// identifier the index already carries is refused as task_exists, so the
		// reopen branch is reachable exactly while the index has not caught up with
		// the tree — the same window a create-then-address pair lives in.

		reopened := call(
			http.MethodPost,
			"/api/v1/tasks",
			`{"title":"probe-alpha","reopen":"terminal","frontmatter":`+
				`{"task_identifier":"probe-1","status":"in_progress","phase":"planning"}}`,
		)
		Expect(reopened.Code).To(Equal(http.StatusOK))
		Expect(decode(reopened)["reopened"]).To(Equal(true))

		read := call(http.MethodGet, "/api/v1/tasks/probe-1", "")
		Expect(read.Code).To(Equal(http.StatusOK))
		frontmatter, ok := decode(read)["frontmatter"].(map[string]any)
		Expect(ok).To(BeTrue(), "the read carries the frontmatter as an object")
		Expect(frontmatter["status"]).To(Equal("in_progress"))
	})

	It("still answers 404 for an identifier no create produced", func() {
		created := call(
			http.MethodPost,
			"/api/v1/tasks",
			`{"title":"probe-alpha","frontmatter":`+
				`{"task_identifier":"probe-1","status":"in_progress","phase":"planning"}}`,
		)
		Expect(created.Code).To(Equal(http.StatusCreated))

		// Indexing the create must not turn the read path lenient: an identifier
		// nothing carries is still a miss, which is what lets a genuine
		// "task not found" stay a failure rather than becoming a silent success.
		missing := call(http.MethodGet, "/api/v1/tasks/never-created", "")
		Expect(missing.Code).To(Equal(http.StatusNotFound))
		Expect(decode(missing)["error"]).To(Equal("not_found"))

		missingPatch := call(
			http.MethodPatch, "/api/v1/tasks/never-created", `{"trigger_count":0}`,
		)
		Expect(missingPatch.Code).To(Equal(http.StatusNotFound))
	})
})
