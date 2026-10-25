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
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
)

// countingMetrics records the page reads the storage decorator reports, so a
// spec can assert that reading a task through the API actually moves the counter
// a verification run discriminates on.
type countingMetrics struct {
	reads int64
}

func (m *countingMetrics) AddFileReads(kind string, count int64) {
	if kind == vaultapi.FileReadKindRead {
		m.reads += count
	}
}

func (m *countingMetrics) SetOpenTasks(map[vaultapi.OpenTaskKey]int) {}

var _ = Describe("Task content endpoints", func() {
	const secret = "test-secret"
	const contentBody = "## Notes\n\nwritten by the API\n"

	var (
		ctx       context.Context
		vaultPath string
		tasksDir  string
		index     *vaultapi.Index
		writer    *fakeWriter
		router    http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-content")
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

		writer = &fakeWriter{}
		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(2026, 10, 25, 12, 0, 0, 0, time.UTC))
		router = handler.NewHandler(
			ctx, index, newTestStore(), secret, vaultPath, "25 Tasks", "test", writer, clock,
			&recordingPublisher{}, &fakeRefClient{},
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	store := func() storage.PageStorage {
		config := storage.DefaultConfig()
		config.TasksDir = "25 Tasks"
		config.GoalsDir = "24 Goals"
		return storage.NewPageStorage(config)
	}

	// seed writes one task and rebuilds the index over it.
	seed := func(frontmatter string) {
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "Alpha.md"),
			[]byte("---\n"+frontmatter+"---\n"+contentBody),
			0o600,
		)).To(Succeed())
		Expect(index.Build(ctx, store())).To(Succeed())
	}

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

	Describe("GET /tasks/{id}", func() {
		It("serves the whole page, not the listing's flat projection", func() {
			seed("task_identifier: task-1\nstatus: in_progress\npriority: 2\n")
			recorder := call(http.MethodGet, "/api/v1/tasks/task-1", "")
			Expect(recorder.Code).To(Equal(http.StatusOK))

			body := decode(recorder)
			Expect(body["id"]).To(Equal("task-1"))
			Expect(body["title"]).To(Equal("Alpha"))
			Expect(body["body"]).To(Equal(contentBody))
			frontmatter, ok := body["frontmatter"].(map[string]any)
			Expect(ok).To(BeTrue(), "the read carries the frontmatter as an object")
			Expect(frontmatter["priority"]).To(BeNumerically("==", 2))
		})

		It("keeps a frontmatter block scalar that contains a delimiter line", func() {
			// YAML allows a block scalar whose lines begin with dashes. Splitting on
			// the first "\n---" would cut the real frontmatter in half and move the
			// rest into the body, so the closing delimiter is matched as a whole line.
			Expect(os.WriteFile(
				filepath.Join(tasksDir, "Alpha.md"),
				[]byte("---\ntask_identifier: task-1\nstatus: in_progress\n"+
					"description: |\n  line one\n  ---\n  line two\n---\n"+contentBody),
				0o600,
			)).To(Succeed())
			Expect(index.Build(ctx, store())).To(Succeed())

			recorder := call(http.MethodGet, "/api/v1/tasks/task-1", "")
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)["body"]).To(Equal(contentBody))

			// The same file must survive a body replacement with its block scalar intact.
			replaced := call(
				http.MethodPut, "/api/v1/tasks/task-1/content", `{"body":"new body\n"}`,
			)
			Expect(replaced.Code).To(Equal(http.StatusOK))
			written := writer.calls[0].content
			Expect(written).To(ContainSubstring("line one"))
			Expect(written).To(ContainSubstring("line two"))
			Expect(written).To(ContainSubstring("task_identifier: task-1"))
		})

		It("answers 404 for an identifier no task answers to", func() {
			recorder := call(http.MethodGet, "/api/v1/tasks/nope", "")
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(decode(recorder)["error"]).To(Equal("not_found"))
		})
	})

	Describe("PATCH /tasks/{id}", func() {
		It("writes a typed key and answers the updated page", func() {
			seed("task_identifier: task-1\nstatus: in_progress\npriority: 2\n")
			recorder := call(http.MethodPatch, "/api/v1/tasks/task-1", `{"priority":5}`)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			frontmatter, ok := decode(recorder)["frontmatter"].(map[string]any)
			Expect(ok).To(BeTrue(), "the response carries the frontmatter as an object")
			Expect(frontmatter["priority"]).To(BeNumerically("==", 5))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].content).To(ContainSubstring("priority: 5"))
		})

		It("answers 400 unknown_field rather than writing a key the API does not type", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(http.MethodPatch, "/api/v1/tasks/task-1", `{"secret_sauce":"x"}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("unknown_field"))
			Expect(decode(recorder)["field"]).To(Equal("secret_sauce"))
			Expect(writer.calls).To(BeEmpty(), "a refused field must not be written")
		})

		It("refuses the approval record, which the approve endpoint owns", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPatch, "/api/v1/tasks/task-1", `{"approved_by":"someone"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("approval_is_a_verb"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("runs a status change through the transition graph", func() {
			// planning is the only phase a task may leave for execution, so a jump
			// straight to done from a task that never entered planning is refused.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(http.MethodPatch, "/api/v1/tasks/task-1", `{"phase":"done"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("invalid_transition"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("answers 422 for a status the domain does not know", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(http.MethodPatch, "/api/v1/tasks/task-1", `{"status":"nonsense"}`)
			Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(decode(recorder)["error"]).To(Equal("unknown_status"))
		})
	})

	Describe("PUT /tasks/{id}/content", func() {
		It("replaces the body and leaves the frontmatter untouched", func() {
			seed("task_identifier: task-1\nstatus: in_progress\npriority: 2\n")
			recorder := call(
				http.MethodPut, "/api/v1/tasks/task-1/content",
				`{"body":"## Replaced\n\nnew text\n"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(writer.calls).To(HaveLen(1))

			written := writer.calls[0].content
			Expect(written).To(ContainSubstring("priority: 2"), "frontmatter survives")
			Expect(written).To(ContainSubstring("## Replaced"))
			Expect(written).NotTo(ContainSubstring("written by the API"))
		})

		It("refuses a request that omits body rather than blanking the content", func() {
			// With a plain string field a caller who sent the wrong field name would
			// decode to an empty body and wipe the task's content.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPut, "/api/v1/tasks/task-1/content", `{"frontmatter":{}}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("invalid_body"))
			Expect(writer.calls).To(BeEmpty(), "a body-less request must not write")
		})

		It("answers the replaced page", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPut, "/api/v1/tasks/task-1/content", `{"body":"only this\n"}`,
			)
			Expect(decode(recorder)["body"]).To(Equal("only this\n"))
		})
	})

	Describe("the read counter", func() {
		It("moves page_read when a task is read by identifier", func() {
			// This is the positive control a dev verification discriminates on. A
			// handler that read the file directly would leave the counter flat and
			// the criterion nothing to observe, so the read goes through the
			// counting storage decorator.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			metrics := &countingMetrics{}
			counting := handler.NewHandler(
				ctx,
				index,
				vaultapi.NewCountingPageStorage(newTestStore(), metrics),
				secret,
				vaultPath,
				"25 Tasks",
				"test",
				writer,
				libtime.NewCurrentDateTime(),
				&recordingPublisher{},
				&fakeRefClient{},
			)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-1", nil)
			request.Header.Set("X-Gateway-Secret", secret)
			recorder := httptest.NewRecorder()
			counting.ServeHTTP(recorder, request)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(metrics.reads).To(BeNumerically(">=", 1))
		})
	})

	Describe("GET /tasks filters", func() {
		It("refuses a filter it does not serve instead of ignoring it", func() {
			// The collector's filename glob is the case that matters: ignoring an
			// unknown filter returns every task, so a caller reads the whole vault
			// as its match set and cannot tell the filter was dropped.
			recorder := call(http.MethodGet, "/api/v1/tasks?glob=*Sentry*", "")
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("unknown_filter"))
			Expect(decode(recorder)["filter"]).To(Equal("glob"))
		})

		It("refuses a declared filter sent with no value", func() {
			// query.Get cannot tell `?task_type=` from an absent parameter, so
			// treating an empty value as unset returns the whole vault to a caller
			// who meant to narrow it.
			recorder := call(http.MethodGet, "/api/v1/tasks?task_type=", "")
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("invalid_filter"))
			Expect(decode(recorder)["filter"]).To(Equal("task_type"))
		})

		It("refuses an unparseable updated_since rather than dropping it", func() {
			recorder := call(http.MethodGet, "/api/v1/tasks?updated_since=yesterday", "")
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("invalid_filter"))
		})

		It("serves the typed filters it does declare", func() {
			seed("task_identifier: task-1\nstatus: in_progress\ntask_type: build-fix\n")
			recorder := call(http.MethodGet, "/api/v1/tasks?task_type=build-fix", "")
			Expect(recorder.Code).To(Equal(http.StatusOK))
		})
	})

	Describe("POST /tasks", func() {
		It("creates the task and mints an identifier when none is given", func() {
			Expect(index.Build(ctx, store())).To(Succeed())
			recorder := call(
				http.MethodPost, "/api/v1/tasks",
				`{"title":"Bravo","frontmatter":{"status":"in_progress"},"body":"hello\n"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].path).To(Equal("25 Tasks/Bravo.md"))
			Expect(writer.calls[0].content).To(ContainSubstring("task_identifier:"))
			Expect(decode(recorder)["id"]).NotTo(BeEmpty())
		})

		It("answers 409 task_exists when the title is already taken", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(http.MethodPost, "/api/v1/tasks", `{"title":"Alpha"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("task_exists"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses the fields PATCH refuses, so create is not a way around it", func() {
			Expect(index.Build(ctx, store())).To(Succeed())
			for _, body := range []string{
				`{"title":"Bravo","frontmatter":{"approved_by":"someone"}}`,
				`{"title":"Bravo","frontmatter":{"secret_sauce":"x"}}`,
			} {
				recorder := call(http.MethodPost, "/api/v1/tasks", body)
				Expect(recorder.Code).To(Equal(http.StatusBadRequest), body)
				Expect(writer.calls).To(BeEmpty(), "a refused create must not write")
			}
		})

		It("refuses an unknown status rather than creating an invisible task", func() {
			// A task whose status does not parse is excluded from the index, so it
			// would answer 201 and then never appear in any listing.
			Expect(index.Build(ctx, store())).To(Succeed())
			recorder := call(
				http.MethodPost, "/api/v1/tasks",
				`{"title":"Bravo","frontmatter":{"status":"nonsense"}}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(decode(recorder)["error"]).To(Equal("unknown_status"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses a task_identifier another task already answers to", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks",
				`{"title":"Bravo","frontmatter":{"task_identifier":"task-1"}}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("task_exists"))
			Expect(writer.calls).To(BeEmpty(), "a colliding identifier must not overwrite")
		})

		It("answers 400 for a title that cannot be a filename", func() {
			Expect(index.Build(ctx, store())).To(Succeed())
			recorder := call(http.MethodPost, "/api/v1/tasks", `{"title":"../escape"}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("invalid_title"))
			Expect(writer.calls).To(BeEmpty())
		})
	})
})
