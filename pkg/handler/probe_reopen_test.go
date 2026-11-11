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

// probeRecord is the byte-exact content of
// tasks/probe-sentry-collector-agent-prod.md as it stands in the private-agent
// vault: a terminal record a probe reuses on every healthcheck tick, carrying
// the very identifier the probe's create asks for.
const probeRecord = `---
aborted_reason: unsupported phase
assignee: sentry-collector-agent
current_job: ""
gate_successor: none
job_started_at: ""
phase: in_progress
retry_count: 0
spawn_notification: false
stage: prod
status: aborted
target_vault: openclaw
task_identifier: 718474b5-d7bd-54b5-8eb7-7a712726a810
task_type: healthcheck
trigger_count: 0
---
reply 'ok'
`

const probeIdentifier = "718474b5-d7bd-54b5-8eb7-7a712726a810"

// liveProbeRecord is the same lane's record once it has PARKED: it parses, it
// carries the probe's own identifier, and its status is in_progress — not
// terminal. This is the shape measured on 2026-11-11 (`probe-service-analysis-
// agent-dev`, parked at human_review), where every later tick was refused with
// `title … occupied` and the lane's probe was silently dead.
const liveProbeRecord = `---
assignee: sentry-collector-agent
current_job: ""
job_started_at: ""
phase: human_review
retry_count: 0
stage: prod
status: in_progress
target_vault: openclaw
task_identifier: 718474b5-d7bd-54b5-8eb7-7a712726a810
task_type: healthcheck
trigger_count: 0
---
reply 'ok'
`

var _ = Describe("Reopening a probe's record", func() {
	const secret = "test-secret"

	var (
		ctx       context.Context
		vaultPath string
		tasksDir  string
		index     *vaultapi.Index
		writer    *fakeWriter
		publisher *recordingPublisher
		router    http.Handler
	)

	probeStore := func() storage.PageStorage {
		config := storage.DefaultConfig()
		config.TasksDir = "tasks"
		return storage.NewPageStorage(config)
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-probe")
		Expect(err).To(BeNil())
		tasksDir = filepath.Join(vaultPath, "tasks")
		Expect(os.MkdirAll(tasksDir, 0o700)).To(Succeed())

		index = vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "openclaw",
			TasksDir:  "tasks",
		}, fakeMetrics{})

		writer = &fakeWriter{}
		publisher = &recordingPublisher{}
		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(2026, 11, 9, 10, 14, 39, 0, time.UTC))
		router = handler.NewHandler(
			ctx, index, probeStore(), secret, vaultPath, "tasks", "openclaw", writer, clock,
			publisher, &fakeRefClient{},
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	// seedProbe writes the reused probe record and rebuilds the index over it, so
	// the identifier is resolvable exactly as it is in a long-running instance.
	seedProbe := func() {
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "probe-sentry-collector-agent-prod.md"),
			[]byte(probeRecord),
			0o600,
		)).To(Succeed())
		Expect(index.Build(ctx, probeStore())).To(Succeed())
	}

	// seedLiveProbe writes the same lane's record in its PARKED, non-terminal shape
	// and rebuilds the index over it.
	seedLiveProbe := func() {
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "probe-sentry-collector-agent-prod.md"),
			[]byte(liveProbeRecord),
			0o600,
		)).To(Succeed())
		Expect(index.Build(ctx, probeStore())).To(Succeed())
	}

	call := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(
			http.MethodPost, "/api/v1/tasks", bytes.NewBufferString(body),
		)
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

	// probeCreate is the create body the task-controller sends for one probe lane:
	// the same identifier and title the record already carries, with reopen opted
	// in because the command is not from the recurring-task publisher.
	probeCreate := func(title, identifier string) string {
		return `{"title":"` + title + `","reopen":"terminal",` +
			`"frontmatter":{"task_type":"healthcheck","status":"in_progress",` +
			`"phase":"planning","assignee":"sentry-collector-agent","stage":"prod",` +
			`"task_identifier":"` + identifier + `",` +
			`"target_vault":"openclaw"},"body":"reply 'ok'"}`
	}

	It("reopens the terminal record that already carries the requested identifier", func() {
		// The identifier guard refused this with a 409 naming the caller's own
		// identifier: the page being replaced counted as a duplicate of itself, so
		// no probe could ever re-take the record it owns.
		seedProbe()
		recorder := call(probeCreate("probe-sentry-collector-agent-prod", probeIdentifier))

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(decode(recorder)["reopened"]).To(Equal(true))
		Expect(writer.calls).To(HaveLen(1))
		Expect(writer.calls[0].path).To(Equal("tasks/probe-sentry-collector-agent-prod.md"))
	})

	It("still refuses an identifier another page already carries", func() {
		// The exemption is scoped to the page at the target path. An identifier
		// living on a DIFFERENT page is the collision the guard exists for: two
		// pages sharing one identifier make one of them unaddressable.
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "Elsewhere.md"),
			[]byte("---\ntask_identifier: "+probeIdentifier+"\nstatus: in_progress\n---\nbody\n"),
			0o600,
		)).To(Succeed())
		Expect(index.Build(ctx, probeStore())).To(Succeed())

		recorder := call(probeCreate("probe-sentry-collector-agent-prod", probeIdentifier))

		Expect(recorder.Code).To(Equal(http.StatusConflict))
		Expect(decode(recorder)["error"]).To(Equal("task_exists"))
		Expect(writer.calls).To(BeEmpty())
	})

	It("reopens a PARKED probe record that carries the requested identifier", func() {
		// The park is what kills the lane: the record is non-terminal, so the
		// terminal-status rule refuses the create, and the paired update-frontmatter
		// fails with it. Both halves of the tick's write path were refused, and
		// neither log line read as an error.
		seedLiveProbe()
		recorder := call(probeCreate("probe-sentry-collector-agent-prod", probeIdentifier))

		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(decode(recorder)["reopened"]).To(Equal(true))
		Expect(writer.calls).To(HaveLen(1))
		Expect(writer.calls[0].path).To(Equal("tasks/probe-sentry-collector-agent-prod.md"))
		Expect(string(writer.calls[0].content)).To(ContainSubstring("phase: planning"))
	})

	It("still refuses a parked probe record carrying a DIFFERENT identifier", func() {
		// The identity guarantee: the reopen is gated on the occupant's own
		// identifier. A different identifier at the same path is the collision the
		// terminal-status rule exists to prevent, and it keeps its 409.
		seedLiveProbe()
		recorder := call(
			probeCreate(
				"probe-sentry-collector-agent-prod",
				"00000000-0000-5000-8000-000000000000",
			),
		)

		Expect(recorder.Code).To(Equal(http.StatusConflict))
		Expect(decode(recorder)["task_identifier"]).To(Equal(probeIdentifier))
		Expect(writer.calls).To(BeEmpty())
	})

	It("still refuses a parked probe record when the create carries no identifier", func() {
		// No identifier is no claim to the record: the occupant could be anyone's.
		seedLiveProbe()
		recorder := call(
			`{"title":"probe-sentry-collector-agent-prod","reopen":"terminal",` +
				`"frontmatter":{"task_type":"healthcheck","status":"in_progress"}}`,
		)

		Expect(recorder.Code).To(Equal(http.StatusConflict))
		Expect(writer.calls).To(BeEmpty())
	})

	It("still refuses a PARKED non-probe record carrying its own identifier", func() {
		// The exception is scoped to probe task types. A live pr-review record, or
		// any other kind, is a genuine occupant and keeps the 409.
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "probe-sentry-collector-agent-prod.md"),
			[]byte("---\ntask_identifier: "+probeIdentifier+"\nstatus: in_progress\n"+
				"task_type: pr-review\n---\nbody\n"),
			0o600,
		)).To(Succeed())
		Expect(index.Build(ctx, probeStore())).To(Succeed())

		recorder := call(probeCreate("probe-sentry-collector-agent-prod", probeIdentifier))

		Expect(recorder.Code).To(Equal(http.StatusConflict))
		Expect(writer.calls).To(BeEmpty())
	})
})
