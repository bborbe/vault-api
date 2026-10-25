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
	"time"

	lib "github.com/bborbe/agent"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
	"github.com/bborbe/vault-api/pkg/publisher"
)

// taskIdentifier is the fixed id every fixture task carries, so the rendered
// output is byte-for-byte predictable.
const taskIdentifier = "11111111-1111-1111-1111-111111111111"

// taskBody is everything after the frontmatter. A write must preserve it
// exactly.
const taskBody = "Tags: [[Task]] [[Agent]]\n\n# Tasks\n\n- [ ] Do the thing\n"

// approvalYear and friends are the injected clock value, chosen so the rendered
// approved_at is unambiguous.
const (
	approvalYear  = 2026
	approvalMonth = time.October
	approvalDay   = 23
	approvalHour  = 12
)

// writeCall is one file the handler handed to the writer.
type writeCall struct {
	path    string
	content string
}

// fakeWriter records what the handler asked git-rest to write. It is the seam
// these tests assert on: the rendered bytes never reach a real git-rest.
type fakeWriter struct {
	calls []writeCall
	err   error
}

func (f *fakeWriter) WriteFile(_ context.Context, path string, content []byte) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, writeCall{path: path, content: string(content)})
	return nil
}

// recordingPublisher records the change events a write produced. It is the seam
// that proves a landed write reaches the task topic without a broker in the test.
type recordingPublisher struct {
	changed     []lib.Task
	deleted     []lib.TaskIdentifier
	err         error
	hadDeadline bool
}

var _ publisher.TaskPublisher = (*recordingPublisher)(nil)

func (r *recordingPublisher) PublishChanged(ctx context.Context, task lib.Task) error {
	_, r.hadDeadline = ctx.Deadline()
	if r.err != nil {
		return r.err
	}
	r.changed = append(r.changed, task)
	return nil
}

func (r *recordingPublisher) PublishDeleted(_ context.Context, id lib.TaskIdentifier) error {
	if r.err != nil {
		return r.err
	}
	r.deleted = append(r.deleted, id)
	return nil
}

// fakeMetrics satisfies vaultapi.Metrics without registering collectors, so the
// suite can build as many indexes as it likes.
type fakeMetrics struct{}

func (fakeMetrics) AddFileReads(string, int64) {}

func (fakeMetrics) SetOpenTasks(map[vaultapi.OpenTaskKey]int) {}

// newTestStore is the page storage every handler suite hands to NewHandler. The
// handlers read through it rather than the filesystem, so it must be a real
// store even where the spec never exercises a read.
func newTestStore() storage.PageStorage {
	config := storage.DefaultConfig()
	config.TasksDir = "25 Tasks"
	config.GoalsDir = "24 Goals"
	return storage.NewPageStorage(config)
}

var _ = Describe("Write endpoints", func() {
	const secret = "test-secret"

	var (
		ctx       context.Context
		vaultPath string
		tasksDir  string
		index     *vaultapi.Index
		writer    *fakeWriter
		published *recordingPublisher
		router    http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-handler")
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
		published = &recordingPublisher{}
		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(
			approvalYear, approvalMonth, approvalDay, approvalHour, 0, 0, 0, time.UTC,
		))

		router = handler.NewHandler(
			ctx,
			index,
			newTestStore(),
			secret,
			vaultPath,
			"25 Tasks",
			"test",
			writer,
			clock,
			published,
			&fakeRefClient{},
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	store := func() storage.PageStorage {
		storageConfig := storage.DefaultConfig()
		storageConfig.TasksDir = "25 Tasks"
		storageConfig.GoalsDir = "24 Goals"
		return storage.NewPageStorage(storageConfig)
	}

	// seed writes one task file and rebuilds the index over it.
	seed := func(frontmatter string) {
		content := "---\n" + frontmatter + "---\n" + taskBody
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "Alpha.md"), []byte(content), 0o600,
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

	Describe("PATCH /api/v1/tasks/{id}/phase", func() {
		It("patches only the changed key and leaves the rest byte-identical", func() {
			seed("status: next\nphase: planning\nassignee: bborbe\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"execution"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)).To(Equal(map[string]any{
				"id":     taskIdentifier,
				"path":   "25 Tasks/Alpha.md",
				"status": "next",
				"phase":  "execution",
			}))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].path).To(Equal("25 Tasks/Alpha.md"))
			Expect(writer.calls[0].content).To(Equal(
				"---\n" +
					"status: next\n" +
					"phase: execution\n" +
					"assignee: bborbe\n" +
					"task_identifier: " + taskIdentifier + "\n" +
					"---\n" + taskBody,
			))
		})

		It("publishes one change event for the written task", func() {
			seed("status: next\nphase: planning\nassignee: bborbe\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"execution"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(published.changed).To(HaveLen(1))
			event := published.changed[0]
			Expect(event.TaskIdentifier).To(Equal(lib.TaskIdentifier(taskIdentifier)))
			Expect(event.Vault).To(Equal("test"))
			Expect(event.Frontmatter["phase"]).To(Equal("execution"))
			// The body is carried unchanged: a write re-renders only the frontmatter.
			Expect(event.Content).To(Equal(lib.TaskContent(taskBody)))
			// A mutation is an upsert, never a delete: a tombstone here would drop the
			// task from the executor's view.
			Expect(published.deleted).To(BeEmpty())
			// Bounded, so a broker lost after startup cannot hold the request open.
			Expect(published.hadDeadline).To(BeTrue())
		})

		It("publishes nothing when git-rest rejects the write", func() {
			// A write that never landed must not appear on the topic, or a consumer
			// would show a change the vault does not have.
			seed("status: next\nphase: planning\n" +
				"task_identifier: " + taskIdentifier + "\n")
			writer.err = errors.New("git-rest unavailable")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"execution"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusBadGateway))
			Expect(published.changed).To(BeEmpty())
		})

		It("answers 200 when the change event cannot be published", func() {
			// The file is already in git, so the write succeeded. Reporting an error
			// would make the client retry a transition that is now applied, and the
			// retry would be refused as illegal.
			seed("status: next\nphase: planning\n" +
				"task_identifier: " + taskIdentifier + "\n")
			published.err = errors.New("broker unavailable")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"execution"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(writer.calls).To(HaveLen(1))
		})

		It("refuses an illegal transition with the named error and the graph", func() {
			seed("status: todo\nphase: planning\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"done"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusConflict))
			body := decode(recorder)
			Expect(body["error"]).To(Equal("invalid_transition"))
			Expect(body["from"]).To(Equal("planning"))
			Expect(body["to"]).To(Equal("done"))
			Expect(body["allowed"]).To(Equal([]any{"execution"}))
			Expect(body["path"]).To(Equal("25 Tasks/Alpha.md"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses a body that carries the approval record", func() {
			seed("status: todo\nphase: planning\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"execution","approved_by":"operator"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("approval_is_a_verb"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses an unknown phase value", func() {
			seed("status: todo\nphase: planning\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"shipped"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(decode(recorder)["error"]).To(Equal("unknown_phase"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("reports a git-rest failure as a bad gateway", func() {
			seed("status: next\nphase: planning\n" +
				"task_identifier: " + taskIdentifier + "\n")
			writer.err = errors.New("git-rest unreachable")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"execution"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusBadGateway))
			Expect(decode(recorder)["error"]).To(Equal("git_rest_failed"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses leaving the approval inbox without an approve call", func() {
			seed("status: todo\nphase: todo\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/phase",
				`{"phase":"planning"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("approval_required"))
			Expect(writer.calls).To(BeEmpty())
		})
	})

	It("publishes from the status and approve handlers too", func() {
		// All three handlers call document.write, so one spec per endpoint proves the
		// shared chokepoint is what publishes rather than the phase handler alone.
		seed("status: next\nphase: execution\n" +
			"task_identifier: " + taskIdentifier + "\n")
		call(
			http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/status",
			`{"status":"in_progress"}`,
		)
		Expect(published.changed).To(HaveLen(1))
		// Assert the payload, not just the count: a wrong status in the event would
		// otherwise pass while the chokepoint visibly fired.
		Expect(published.changed[0].Frontmatter["status"]).To(Equal("in_progress"))

		published.changed = nil
		seed("status: todo\nphase: todo\n" +
			"task_identifier: " + taskIdentifier + "\n")
		call(
			http.MethodPost, "/api/v1/tasks/"+taskIdentifier+"/approve",
			`{"approved_by":"operator"}`,
		)
		Expect(published.changed).To(HaveLen(1))
		Expect(published.changed[0].Frontmatter["approved_by"]).To(Equal("operator"))
		Expect(published.changed[0].Frontmatter["phase"]).To(Equal("planning"))
	})

	Describe("PATCH /api/v1/tasks/{id}/status", func() {
		It("patches only the changed key and leaves the rest byte-identical", func() {
			seed("status: next\nphase: execution\nassignee: bborbe\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/status",
				`{"status":"in_progress"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)).To(Equal(map[string]any{
				"id":     taskIdentifier,
				"path":   "25 Tasks/Alpha.md",
				"status": "in_progress",
				"phase":  "execution",
			}))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].content).To(Equal(
				"---\n" +
					"status: in_progress\n" +
					"phase: execution\n" +
					"assignee: bborbe\n" +
					"task_identifier: " + taskIdentifier + "\n" +
					"---\n" + taskBody,
			))
		})

		It("refuses in_progress while the task sits in the approval inbox", func() {
			seed("status: todo\nphase: todo\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/status",
				`{"status":"in_progress"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusConflict))
			body := decode(recorder)
			Expect(body["error"]).To(Equal("approval_required"))
			Expect(body["allowed"]).To(Equal([]any{"POST /api/v1/tasks/{id}/approve"}))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses an illegal status edge", func() {
			seed("status: completed\nphase: done\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPatch, "/api/v1/tasks/"+taskIdentifier+"/status",
				`{"status":"in_progress"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("invalid_transition"))
			Expect(writer.calls).To(BeEmpty())
		})
	})

	Describe("POST /api/v1/tasks/{id}/approve", func() {
		It("writes the approval record and the phase, and leaves status alone", func() {
			seed("status: next\nphase: todo\nassignee: bborbe\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPost, "/api/v1/tasks/"+taskIdentifier+"/approve",
				`{"approved_by":"operator"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode(recorder)).To(Equal(map[string]any{
				"id":     taskIdentifier,
				"path":   "25 Tasks/Alpha.md",
				"status": "next",
				"phase":  "planning",
			}))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].content).To(Equal(
				"---\n" +
					"status: next\n" +
					"phase: planning\n" +
					"assignee: bborbe\n" +
					"task_identifier: " + taskIdentifier + "\n" +
					"approved_at: 2026-10-23T12:00:00Z\n" +
					"approved_by: operator\n" +
					"---\n" + taskBody,
			))
		})

		It("fixes a missing owner at approval time", func() {
			seed("status: todo\nphase: todo\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPost, "/api/v1/tasks/"+taskIdentifier+"/approve",
				`{"approved_by":"operator"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(writer.calls).To(HaveLen(1))
			Expect(writer.calls[0].content).To(ContainSubstring("assignee: operator\n"))
		})

		It("refuses a clock that is not an instant", func() {
			seed("status: next\nphase: todo\n" +
				"task_identifier: " + taskIdentifier + "\n")
			// An unset clock falls back to the wall clock, so the zero instant has
			// to be set explicitly — it is the case the approve guard exists for: a
			// placeholder is not a measurement.
			zero := libtime.NewCurrentDateTime()
			zero.SetNow(libtime.DateTime{})
			zeroClock := handler.NewHandler(
				ctx,
				index,
				newTestStore(),
				secret,
				vaultPath,
				"25 Tasks",
				"test",
				writer,
				zero,
				published,
				&fakeRefClient{},
			)
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/tasks/"+taskIdentifier+"/approve",
				bytes.NewBufferString(`{"approved_by":"operator"}`),
			)
			request.Header.Set("X-Gateway-Secret", secret)
			recorder := httptest.NewRecorder()
			zeroClock.ServeHTTP(recorder, request)

			Expect(recorder.Code).To(Equal(http.StatusInternalServerError))
			Expect(decode(recorder)["error"]).To(Equal("invalid_clock"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses a blank approver", func() {
			seed("status: todo\nphase: todo\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPost, "/api/v1/tasks/"+taskIdentifier+"/approve",
				`{"approved_by":"  "}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode(recorder)["error"]).To(Equal("blank_approver"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses a task that is not in the approval inbox", func() {
			seed("status: todo\nphase: planning\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPost, "/api/v1/tasks/"+taskIdentifier+"/approve",
				`{"approved_by":"operator"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("not_approvable"))
			Expect(writer.calls).To(BeEmpty())
		})

		It("refuses a task that already carries an approval record", func() {
			seed("status: todo\nphase: todo\napproved_by: someone\n" +
				"task_identifier: " + taskIdentifier + "\n")

			recorder := call(
				http.MethodPost, "/api/v1/tasks/"+taskIdentifier+"/approve",
				`{"approved_by":"operator"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode(recorder)["error"]).To(Equal("already_approved"))
			Expect(writer.calls).To(BeEmpty())
		})
	})
})
