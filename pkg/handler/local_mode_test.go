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
			clock, &recordingPublisher{}, &fakeRefClient{},
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
})
