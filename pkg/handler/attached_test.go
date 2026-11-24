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
	"net/url"
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

// The claude-wrapper flagged tick lists eligible tasks and then reads each one's
// detail. In multi-vault mode a listed task can live in an attached vault, so its
// detail read must resolve there too — one 404 fails the whole tick.
var _ = Describe("Multi-vault task reads", func() {
	const secret = "test-secret"

	var (
		ctx      context.Context
		roots    []string
		primary  *vaultapi.Index
		attached *vaultapi.Index
		router   http.Handler
	)

	newVault := func(name, task string) (*vaultapi.Index, string) {
		root, err := os.MkdirTemp("", "vault-api-handler-"+name)
		Expect(err).To(BeNil())
		tasks := filepath.Join(root, "25 Tasks")
		Expect(os.MkdirAll(tasks, 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "24 Goals"), 0o700)).To(Succeed())
		Expect(os.WriteFile(
			filepath.Join(tasks, task+".md"),
			[]byte("---\nstatus: in_progress\nflag: true\nrole: agent\n---\n\nBody\n"),
			0o600,
		)).To(Succeed())
		config := storage.DefaultConfig()
		config.TasksDir = "25 Tasks"
		config.GoalsDir = "24 Goals"
		index := vaultapi.NewIndex(vaultapi.Config{
			VaultPath: root,
			VaultName: name,
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, fakeMetrics{})
		Expect(index.Build(ctx, storage.NewPageStorage(config))).To(Succeed())
		return index, root
	}

	call := func(path string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, bytes.NewBufferString(""))
		request.Header.Set("X-Gateway-Secret", secret)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	listedIDs := func() map[string]string {
		recorder := call("/api/v1/tasks?flag=true&status=in_progress")
		Expect(recorder.Code).To(Equal(http.StatusOK))
		var body struct {
			Items []struct {
				ID    string `json:"id"`
				Vault string `json:"vault"`
			} `json:"items"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
		ids := map[string]string{}
		for _, item := range body.Items {
			ids[item.Vault] = item.ID
		}
		return ids
	}

	BeforeEach(func() {
		ctx = context.Background()
		var primaryRoot, attachedRoot string
		primary, primaryRoot = newVault("private-personal", "Primary Task")
		attached, attachedRoot = newVault("seibert-brogrammers", "Attached Task")
		roots = []string{primaryRoot, attachedRoot}
		primary.Attach(attached)

		clock := libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(2026, 11, 23, 12, 0, 0, 0, time.UTC))
		router = handler.NewHandler(
			ctx, primary, newTestStore(), secret, primaryRoot, "25 Tasks", "private-personal",
			&fakeWriter{}, clock, &recordingPublisher{}, &fakeRefClient{}, nil,
		)
	})

	AfterEach(func() {
		for _, root := range roots {
			Expect(os.RemoveAll(root)).To(Succeed())
		}
	})

	It("lists rows from both vaults, each with its own vault", func() {
		ids := listedIDs()
		Expect(ids).To(HaveKey("private-personal"))
		Expect(ids).To(HaveKey("seibert-brogrammers"))
	})

	It("serves the detail read for a task listed from an attached vault", func() {
		id := listedIDs()["seibert-brogrammers"]
		Expect(id).NotTo(BeEmpty())

		recorder := call("/api/v1/tasks/" + url.PathEscape(id))
		Expect(recorder.Code).To(Equal(http.StatusOK), recorder.Body.String())
		Expect(recorder.Body.String()).To(ContainSubstring("Attached Task"))
	})

	It("still serves the primary vault's detail read", func() {
		id := listedIDs()["private-personal"]
		recorder := call("/api/v1/tasks/" + url.PathEscape(id))
		Expect(recorder.Code).To(Equal(http.StatusOK), recorder.Body.String())
		Expect(recorder.Body.String()).To(ContainSubstring("Primary Task"))
	})

	It("answers 404 for an id no vault holds", func() {
		recorder := call("/api/v1/tasks/" + url.PathEscape("No Such Task"))
		Expect(recorder.Code).To(Equal(http.StatusNotFound))
	})
})
