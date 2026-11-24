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

var _ = Describe("Local-mode read endpoints", func() {
	const secret = "test-secret"

	var (
		ctx       context.Context
		vaultPath string
		tasksDir  string
		topicsDir string
		index     *vaultapi.Index
		router    http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-local-handler")
		Expect(err).To(BeNil())
		tasksDir = filepath.Join(vaultPath, "25 Tasks")
		topicsDir = filepath.Join(vaultPath, "23 Topics")
		Expect(os.MkdirAll(tasksDir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(topicsDir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Goals"), 0o700)).To(Succeed())

		index = vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
			TopicsDir: "23 Topics",
		}, fakeMetrics{})

		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(2026, 11, 21, 12, 0, 0, 0, time.UTC))
		router = handler.NewHandler(
			ctx, index, newTestStore(), secret, vaultPath, "25 Tasks", "test", &fakeWriter{},
			clock, &recordingPublisher{}, &fakeRefClient{}, nil,
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	store := func() storage.PageStorage {
		config := storage.DefaultConfig()
		config.TasksDir = "25 Tasks"
		config.GoalsDir = "24 Goals"
		config.TopicsDir = "23 Topics"
		return storage.NewPageStorage(config)
	}

	write := func(dir, name, frontmatter string) {
		Expect(os.WriteFile(
			filepath.Join(dir, name+".md"),
			[]byte("---\n"+frontmatter+"---\n\nBody\n"),
			0o600,
		)).To(Succeed())
	}

	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, bytes.NewBufferString(""))
		request.Header.Set("X-Gateway-Secret", secret)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	decode := func(recorder *httptest.ResponseRecorder) map[string]any {
		var body map[string]any
		Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
		return body
	}

	It("serves flag and due filters on /api/v1/tasks", func() {
		write(tasksDir, "FlaggedDue", "status: in_progress\nflag: true\n")
		write(
			tasksDir,
			"FlaggedFuture",
			"status: in_progress\nflag: true\ndefer_date: \"2999-01-01\"\n",
		)
		write(tasksDir, "Unflagged", "status: in_progress\n")
		Expect(index.Build(ctx, store())).To(Succeed())

		recorder := call("/api/v1/tasks?flag=true&status=in_progress&due=now")
		Expect(recorder.Code).To(Equal(http.StatusOK))
		body := decode(recorder)
		Expect(body["total"]).To(BeNumerically("==", 1))
		items, ok := body["items"].([]any)
		Expect(ok).To(BeTrue())
		Expect(items).To(HaveLen(1))
		item, ok := items[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(item["title"]).To(Equal("FlaggedDue"))

		flaggedOnly := decode(call("/api/v1/tasks?flag=true"))
		Expect(flaggedOnly["total"]).To(BeNumerically("==", 2))
	})

	It("serves the fields the board renders on every task-list row", func() {
		// Every field here is one the board's own task response carries, so a
		// consumer serving the board from this list needs no per-row detail call.
		boardTask := "---\n" +
			"status: in_progress\n" +
			"priority: 1\n" +
			"blocked_by:\n" +
			"    - '[[Blocker]]'\n" +
			"due_date: \"2026-12-01\"\n" +
			"planned_date: \"2026-11-24\"\n" +
			"claude_session_id: 78027d5c-e97e-466d-a653-38b3d644ee18\n" +
			"jira: BRO-123\n" +
			"mode: local\n" +
			"---\n\n" +
			"## Open Questions\n\n" +
			"1. Which vault?\n"
		Expect(os.WriteFile(
			filepath.Join(tasksDir, "Board Fields.md"), []byte(boardTask), 0o600,
		)).To(Succeed())
		write(tasksDir, "Bare", "status: in_progress\n")
		Expect(index.Build(ctx, store())).To(Succeed())

		body := decode(call("/api/v1/tasks?status=in_progress"))
		items, ok := body["items"].([]any)
		Expect(ok).To(BeTrue())
		Expect(items).To(HaveLen(2))

		byTitle := map[string]map[string]any{}
		for _, raw := range items {
			item, ok := raw.(map[string]any)
			Expect(ok).To(BeTrue())
			title, ok := item["title"].(string)
			Expect(ok).To(BeTrue())
			byTitle[title] = item
		}

		board := byTitle["Board Fields"]
		Expect(board["blocked_by"]).To(Equal([]any{"[[Blocker]]"}))
		Expect(board["priority"]).To(BeNumerically("==", 1))
		Expect(board["due_date"]).To(Equal("2026-12-01"))
		Expect(board["planned_date"]).To(Equal("2026-11-24"))
		Expect(board["claude_session_id"]).
			To(Equal("78027d5c-e97e-466d-a653-38b3d644ee18"))
		Expect(board["jira"]).To(Equal("BRO-123"))
		Expect(board["mode"]).To(Equal("local"))
		Expect(board["open_questions"]).To(Equal([]any{
			map[string]any{"index": float64(1), "text": "Which vault?"},
		}))

		// A row carrying none of the optional values still serialises every key, and
		// the two lists are [] rather than null — a consumer must never have to tell
		// "absent" from "empty".
		bare := byTitle["Bare"]
		for _, key := range []string{
			"blocked_by", "priority", "due_date", "planned_date",
			"claude_session_id", "jira", "mode", "open_questions",
		} {
			Expect(bare).To(HaveKey(key))
		}
		Expect(bare["blocked_by"]).To(Equal([]any{}))
		Expect(bare["open_questions"]).To(Equal([]any{}))
		Expect(bare["priority"]).To(BeNumerically("==", 0))
		Expect(bare["due_date"]).To(BeNil())
		Expect(bare["claude_session_id"]).To(BeNil())
	})

	It("refuses a malformed flag or due value instead of widening the result", func() {
		write(tasksDir, "Alpha", "status: in_progress\nflag: true\n")
		Expect(index.Build(ctx, store())).To(Succeed())

		badFlag := call("/api/v1/tasks?flag=maybe")
		Expect(badFlag.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(badFlag)["error"]).To(Equal("invalid_filter"))

		badDue := call("/api/v1/tasks?due=soon")
		Expect(badDue.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(badDue)["error"]).To(Equal("invalid_filter"))

		emptyFlag := call("/api/v1/tasks?flag=")
		Expect(emptyFlag.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(emptyFlag)["error"]).To(Equal("invalid_filter"))

		unknown := call("/api/v1/tasks?nope=true")
		Expect(unknown.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(unknown)["error"]).To(Equal("unknown_filter"))
	})

	It("serves /api/v1/topics as an empty array when there are none", func() {
		Expect(index.Build(ctx, store())).To(Succeed())

		recorder := call("/api/v1/topics")
		Expect(recorder.Code).To(Equal(http.StatusOK))
		body := decode(recorder)
		Expect(body["total"]).To(BeNumerically("==", 0))
		items, ok := body["items"].([]any)
		Expect(ok).To(BeTrue())
		Expect(items).ToNot(BeNil(), "items must be [] not null")
		Expect(items).To(BeEmpty())
	})

	It("serves /api/v1/topics", func() {
		write(topicsDir, "Some Topic", "page_type: topic\nstatus: in_progress\n")
		Expect(index.Build(ctx, store())).To(Succeed())

		recorder := call("/api/v1/topics")
		Expect(recorder.Code).To(Equal(http.StatusOK))
		body := decode(recorder)
		Expect(body["total"]).To(BeNumerically("==", 1))
		items, ok := body["items"].([]any)
		Expect(ok).To(BeTrue())
		item, ok := items[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(item["title"]).To(Equal("Some Topic"))
	})

	It("serves the enrichment fields on /api/v1/goals", func() {
		goalsDir := filepath.Join(vaultPath, "24 Goals")
		write(goalsDir, "Flagged Goal", "status: in_progress\nflag: true\n"+
			"claude_session_id: 11111111-1111-1111-1111-111111111111\n"+
			"role: manager\nmode: headless\ndefer_date: \"2999-01-01\"\n"+
			"flag_set_by: operator\nblocked_by:\n  - '[[Some Blocker]]'\n")
		write(goalsDir, "Quiet Goal", "status: in_progress\n")

		Expect(index.Build(ctx, store())).To(Succeed())

		flagged := call("/api/v1/goals?flag=true")
		Expect(flagged.Code).To(Equal(http.StatusOK))
		body := decode(flagged)
		items, ok := body["items"].([]any)
		Expect(ok).To(BeTrue())
		item, ok := items[0].(map[string]any)
		Expect(ok).To(BeTrue())
		// A goal's due date, role, blockers and provenance must be readable from
		// this projection: a consumer that cannot see them either re-reads the page
		// or applies the task rule to a goal, and a future-deferred goal then opens
		// its manager early.
		Expect(item["role"]).To(Equal("manager"))
		Expect(item["mode"]).To(Equal("headless"))
		Expect(item["defer_date"]).To(Equal("2999-01-01"))
		Expect(item["flag_set_by"]).To(Equal("operator"))
		Expect(item["blocked_by"]).To(ConsistOf("[[Some Blocker]]"))
	})

	It("serves the flag filter and the session id on /api/v1/goals", func() {
		goalsDir := filepath.Join(vaultPath, "24 Goals")
		write(goalsDir, "Flagged Goal", "status: in_progress\nflag: true\n"+
			"claude_session_id: 11111111-1111-1111-1111-111111111111\n")
		write(goalsDir, "Quiet Goal", "status: in_progress\n")
		Expect(index.Build(ctx, store())).To(Succeed())

		flagged := call("/api/v1/goals?flag=true")
		Expect(flagged.Code).To(Equal(http.StatusOK))
		body := decode(flagged)
		Expect(body["total"]).To(BeNumerically("==", 1))
		items, ok := body["items"].([]any)
		Expect(ok).To(BeTrue())
		item, ok := items[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(item["title"]).To(Equal("Flagged Goal"))
		Expect(item["flag"]).To(Equal(true))
		Expect(item["claude_session_id"]).
			To(Equal("11111111-1111-1111-1111-111111111111"))

		all := decode(call("/api/v1/goals"))
		Expect(all["total"]).To(BeNumerically("==", 2))
	})

	It("serves the flag filter on /api/v1/topics and one topic by id", func() {
		write(topicsDir, "Flagged Topic", "page_type: topic\nstatus: in_progress\nflag: true\n"+
			"claude_session_id: 22222222-2222-2222-2222-222222222222\n")
		write(topicsDir, "Quiet Topic", "page_type: topic\nstatus: in_progress\n")
		Expect(index.Build(ctx, store())).To(Succeed())

		flagged := decode(call("/api/v1/topics?flag=true"))
		Expect(flagged["total"]).To(BeNumerically("==", 1))
		items, ok := flagged["items"].([]any)
		Expect(ok).To(BeTrue())
		item, ok := items[0].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(item["title"]).To(Equal("Flagged Topic"))
		Expect(item["flag"]).To(Equal(true))
		Expect(item["claude_session_id"]).
			To(Equal("22222222-2222-2222-2222-222222222222"))

		byID := call("/api/v1/topics/" + "flagged-topic")
		Expect(byID.Code).To(Equal(http.StatusOK))
		Expect(decode(byID)["title"]).To(Equal("Flagged Topic"))

		missing := call("/api/v1/topics/nope")
		Expect(missing.Code).To(Equal(http.StatusNotFound))
		Expect(decode(missing)["error"]).To(Equal("not_found"))
	})

	It("refuses an unsupported filter on the goal and topic lists", func() {
		Expect(index.Build(ctx, store())).To(Succeed())

		unknownGoal := call("/api/v1/goals?status=in_progress")
		Expect(unknownGoal.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(unknownGoal)["error"]).To(Equal("unknown_filter"))

		emptyTopic := call("/api/v1/topics?flag=")
		Expect(emptyTopic.Code).To(Equal(http.StatusBadRequest))
		Expect(decode(emptyTopic)["error"]).To(Equal("invalid_filter"))
	})
})
