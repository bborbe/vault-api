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
		publisher *recordingPublisher
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
		publisher = &recordingPublisher{}
		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(2026, 10, 25, 12, 0, 0, 0, time.UTC))
		router = handler.NewHandler(
			ctx, index, newTestStore(), secret, vaultPath, "25 Tasks", "test", writer, clock,
			publisher, &fakeRefClient{},
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

		It("writes an agent-pipeline key, which this API stores but does not model", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPatch,
				"/api/v1/tasks/task-1",
				`{"trigger_scope":"planning:","trigger_count":1,"current_job":"job-1"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			frontmatter, ok := decode(recorder)["frontmatter"].(map[string]any)
			Expect(ok).To(BeTrue(), "the response carries the frontmatter as an object")
			Expect(frontmatter["trigger_scope"]).To(Equal("planning:"))
			Expect(frontmatter["current_job"]).To(Equal("job-1"))
			Expect(writer.calls).To(HaveLen(1))
			// The emitter quotes the value because it ends in a colon; asserting the
			// quoted form keeps this a check on the write rather than on the encoder.
			Expect(writer.calls[0].content).To(ContainSubstring("trigger_scope: 'planning:'"))
		})

		// One row per entry in agentPipelineFields. A misspelled map key fails
		// closed — 400 unknown_field — so without this a typo would pass every
		// other test here and only break the controller at runtime.
		It("accepts every agent-pipeline key, not just the three the redrive sends", func() {
			values := map[string]string{
				"trigger_scope":      `"planning:"`,
				"trigger_count":      `1`,
				"max_triggers":       `3`,
				"current_job":        `"job-1"`,
				"job_started_at":     `"2026-10-28T10:00:00Z"`,
				"spawn_notification": `true`,
				"queued_at":          `"2026-10-28T10:00:00Z"`,
				"aborted_reason":     `"cap reached"`,
				"gate_successor":     `"none"`,
				"target_vault":       `"personal"`,
				"previous_assignee":  `"bborbe"`,
				"retry_count":        `0`,
				// Written by github-pr-watcher / the release watcher on the
				// CREATE path, not by the executor/controller. Their absence
				// refused every create outright (400 unknown_field, 2026-10-28),
				// and fieldRefusal names only one key per attempt, so each of
				// these appeared as "the" cause in a different log line.
				"base_ref":        `"master"`,
				"clone_url":       `"https://github.com/bborbe/example.git"`,
				"ref":             `"0123456789abcdef"`,
				"repo":            `"bborbe/example"`,
				"current_version": `"v1.2.3"`,
				"override_reason": `"trivial-bot-bump"`,
				"title":           `"Some Task Title"`,
			}
			for key, value := range values {
				seed("task_identifier: task-1\nstatus: in_progress\n")
				recorder := call(http.MethodPatch, "/api/v1/tasks/task-1", `{"`+key+`":`+value+`}`)
				Expect(recorder.Code).To(
					Equal(http.StatusOK), key+" must be accepted by PATCH",
				)
				Expect(writer.calls).NotTo(BeEmpty(), key)
				Expect(writer.calls[len(writer.calls)-1].content).To(
					ContainSubstring(key+":"), key+" must reach the written file",
				)
			}
		})

		It("still refuses a key in neither set, so the drift control survives", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPatch, "/api/v1/tasks/task-1", `{"probe_unknown_field_zzz":"x"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("unknown_field"))
			Expect(writer.calls).To(BeEmpty())
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

		It("refuses a task_type that names no type after splitting", func() {
			// `?task_type=,` is not empty, so the empty-value check passes it, and it
			// yields no values — which would apply no filter at all and return the
			// whole vault.
			recorder := call(http.MethodGet, "/api/v1/tasks?task_type=,", "")
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("invalid_filter"))
			Expect(decode(recorder)["filter"]).To(Equal("task_type"))
		})

		It("serves several task_type values, repeated or comma-separated", func() {
			seed("task_identifier: task-1\nstatus: in_progress\ntask_type: build-fix\n")
			Expect(os.WriteFile(
				filepath.Join(tasksDir, "Bravo.md"),
				[]byte("---\ntask_identifier: task-2\nstatus: in_progress\n"+
					"task_type: pr-review\n---\nbody\n"),
				0o600,
			)).To(Succeed())
			Expect(index.Build(ctx, store())).To(Succeed())

			for _, query := range []string{
				"?task_type=build-fix&task_type=pr-review",
				"?task_type=build-fix,pr-review",
			} {
				recorder := call(http.MethodGet, "/api/v1/tasks"+query, "")
				Expect(recorder.Code).To(Equal(http.StatusOK), query)
				items, ok := decode(recorder)["items"].([]any)
				Expect(ok).To(BeTrue(), query)
				Expect(items).To(HaveLen(2), "%s is a union, not a constraint", query)
			}

			// The union must still intersect with the other filters — this is the
			// distinction a per-filter test cannot see.
			recorder := call(
				http.MethodGet, "/api/v1/tasks?task_type=build-fix,pr-review&status=completed", "",
			)
			items, _ := decode(recorder)["items"].([]any)
			Expect(items).To(BeEmpty(), "(type in {a,b}) AND status=completed, not an OR")
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

		// The create route shares the PATCH allowlist, so it accepts the pipeline
		// keys too. That is deliberate rather than incidental: the controller's
		// create executor stamps target_vault, and max_triggers is documented as
		// something only a task's own create sets.
		It("accepts an agent-pipeline key on create, which the controller sends", func() {
			Expect(index.Build(ctx, store())).To(Succeed())
			recorder := call(
				http.MethodPost,
				"/api/v1/tasks",
				`{"title":"Bravo","frontmatter":{"status":"in_progress","target_vault":"personal","max_triggers":3},"body":"hello\n"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].content).To(ContainSubstring("target_vault: personal"))
			Expect(writer.calls[0].content).To(ContainSubstring("max_triggers: 3"))
		})

		It("answers 409 task_exists when the title is already taken", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(http.MethodPost, "/api/v1/tasks", `{"title":"Alpha"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("task_exists"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("names the occupant in the 409, so the caller need not re-read to decide", func() {
			// `id` used to be the title, which is the address the caller had just
			// sent — so it learned nothing and could not tell "terminal, reopen"
			// from "different task, disambiguate" without a second read it had no
			// address for.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(http.MethodPost, "/api/v1/tasks", `{"title":"Alpha"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))

			body := decode(recorder)
			Expect(body["task_identifier"]).To(Equal("task-1"))
			Expect(body["status"]).To(Equal("in_progress"))
			// `id` keeps its original meaning so an existing caller is unaffected
			Expect(body["id"]).To(Equal("Alpha"))
		})

		It("omits task_identifier from the 409 when the occupant has none", func() {
			// A page the index addresses by its filename slug carries no
			// task_identifier; advertising the string "<nil>" would be exactly the
			// misleading payload this refusal exists to replace.
			Expect(os.WriteFile(
				filepath.Join(tasksDir, "Alpha.md"),
				[]byte("---\nstatus: in_progress\n---\nbody\n"),
				0o600,
			)).To(Succeed())
			Expect(index.Build(ctx, store())).To(Succeed())

			recorder := call(http.MethodPost, "/api/v1/tasks", `{"title":"Alpha"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			body := decode(recorder)
			Expect(body).NotTo(HaveKey("task_identifier"))
			Expect(body["status"]).To(Equal("in_progress"))
		})

		It("replaces a terminal occupant when reopen is terminal", func() {
			seed(
				"task_identifier: task-1\nstatus: completed\nphase: done\ncompleted_date: 2026-01-01T00:00:00Z\nrecovery_sha: deadbeef\n",
			)
			recorder := call(
				http.MethodPost,
				"/api/v1/tasks",
				`{"title":"Alpha","reopen":"terminal","frontmatter":{"task_identifier":"task-2","status":"in_progress"},"body":"fresh\n"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))

			body := decode(recorder)
			Expect(body["reopened"]).To(Equal(true))
			Expect(body["id"]).To(Equal("task-2"))

			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].path).To(Equal("25 Tasks/Alpha.md"))
			// REPLACED, not merged: a merged reopen would leave the previous
			// attempt's completion record on a live task.
			Expect(writer.calls[0].content).NotTo(ContainSubstring("completed_date"))
			Expect(writer.calls[0].content).NotTo(ContainSubstring("recovery_sha"))
			Expect(writer.calls[0].content).NotTo(ContainSubstring("task-1"))
			Expect(writer.calls[0].content).To(ContainSubstring("task-2"))
			Expect(writer.calls[0].content).To(ContainSubstring("fresh"))
		})

		It("refuses to reopen a live occupant even when asked", func() {
			// Reopening a live task would let two attempts share one identity,
			// which is the collision the terminal-status rule exists to prevent.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks",
				`{"title":"Alpha","reopen":"terminal","frontmatter":{"status":"in_progress"}}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["task_identifier"]).To(Equal("task-1"))
			Expect(writer.calls).To(BeEmpty())
		})

		It(
			"judges the occupant at the path, not a page whose identifier matches the title",
			func() {
				// The data-loss sequence the index's shared key space makes reachable:
				// GetTask resolves identifier, name and slug together, so a page
				// elsewhere carrying task_identifier "Alpha" would stand in for the task
				// that actually occupies Alpha.md — and its terminal status would
				// authorise replacing a live task.
				seed("task_identifier: task-1\nstatus: in_progress\n")
				Expect(os.WriteFile(
					filepath.Join(tasksDir, "Bravo.md"),
					[]byte("---\ntask_identifier: Alpha\nstatus: completed\n---\nbody\n"),
					0o600,
				)).To(Succeed())
				Expect(index.Build(ctx, store())).To(Succeed())

				recorder := call(
					http.MethodPost,
					"/api/v1/tasks",
					`{"title":"Alpha","reopen":"terminal","frontmatter":{"task_identifier":"task-9","status":"in_progress"}}`,
				)
				Expect(recorder.Code).To(Equal(http.StatusConflict))
				Expect(decode(recorder)["task_identifier"]).To(Equal("task-1"))
				Expect(writer.calls).To(BeEmpty(), "the live Alpha.md must survive")
			},
		)

		It("keeps 409 for a reopen value it does not recognise", func() {
			seed("task_identifier: task-1\nstatus: completed\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks",
				`{"title":"Alpha","reopen":"always"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(writer.calls).To(BeEmpty())
		})

		It("mints an identifier when a reopen omits one", func() {
			seed("task_identifier: task-1\nstatus: completed\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks",
				`{"title":"Alpha","reopen":"terminal","frontmatter":{"status":"in_progress"}}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)["id"]).NotTo(BeEmpty())
			Expect(decode(recorder)["id"]).NotTo(Equal("task-1"))
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

	Describe("POST /tasks/{id}/readdress", func() {
		It("moves the identifier and leaves the file where it is", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks/Alpha/readdress",
				`{"task_identifier":"task-2"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)["id"]).To(Equal("task-2"))

			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].path).To(Equal("25 Tasks/Alpha.md"))
			Expect(writer.calls[0].content).To(ContainSubstring("task-2"))
			Expect(writer.calls[0].content).NotTo(ContainSubstring("task-1"))

			// The change event must announce the NEW address. It is read from the
			// item, so publishing before the item is updated announces the old one —
			// the single value a caller must not be told after a move.
			Expect(publisher.changed).To(HaveLen(1))
			Expect(string(publisher.changed[0].TaskIdentifier)).To(Equal("task-2"))
		})

		It("treats a move to the task's own title as a no-op", func() {
			// GetTask keys identifier, name and slug in one map, so the task's own
			// title resolves back to itself; without the path comparison this lands
			// in the collision branch and answers 409 naming the task itself.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks/Alpha/readdress",
				`{"task_identifier":"Alpha"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)["id"]).To(Equal("Alpha"))
		})

		It("refuses to move onto an identifier another task already answers to", func() {
			// Silently merging two tasks into one address is the worst outcome
			// available here, so the refusal is mandatory rather than defensive.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			Expect(os.WriteFile(
				filepath.Join(tasksDir, "Bravo.md"),
				[]byte("---\ntask_identifier: task-2\nstatus: in_progress\n---\nbody\n"),
				0o600,
			)).To(Succeed())
			Expect(index.Build(ctx, store())).To(Succeed())

			recorder := call(
				http.MethodPost, "/api/v1/tasks/Alpha/readdress",
				`{"task_identifier":"task-2"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("task_exists"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("preserves the frontmatter it does not set", func() {
			seed("task_identifier: task-1\nstatus: in_progress\npriority: 2\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks/Alpha/readdress",
				`{"task_identifier":"task-2"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].content).To(ContainSubstring("priority"))
			Expect(writer.calls[0].content).To(ContainSubstring("status: in_progress"))
		})

		It("is a no-op when the task is already addressed that way", func() {
			// A caller re-sending the same move is retrying, not colliding with
			// itself; 409 here would make the verb non-idempotent.
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks/Alpha/readdress",
				`{"task_identifier":"task-1"}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)["id"]).To(Equal("task-1"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses an empty identifier", func() {
			seed("task_identifier: task-1\nstatus: in_progress\n")
			recorder := call(
				http.MethodPost, "/api/v1/tasks/Alpha/readdress",
				`{"task_identifier":"  "}`,
			)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("invalid_task_identifier"))
			Expect(writer.calls).To(BeEmpty())
		})
	})
})
