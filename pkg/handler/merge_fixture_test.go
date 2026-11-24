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
	"strings"

	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
)

// The 21 files the 2026-11-02 OpenClaw sync quarantined, checked in verbatim as
// testdata. Each directory holds the three sides a diverged pull supplies:
// base is the file at the merge base, ours and theirs are the two sides the
// conflict markers encode. See testdata/merge/MANIFEST.tsv for the source
// commit and the sha256 of every side.
//
// This suite drives the HTTP endpoint rather than mergeBody, because the
// incident traversed the handler: git-rest posts with apply:false, which takes
// handleMerge's mergeUnapplied branch and calls MergeFile directly. A test of
// the helper alone would not have caught the quarantine.
var _ = Describe("Merge endpoint — the 21 mixed shapes from the incident", func() {
	const (
		secret = "fixture-secret"
		// Ordering of two appended sections is decided by committer time, so the
		// fixture pins both rather than reading a clock.
		oursTime   = "2026-11-08T17:00:00Z"
		theirsTime = "2026-11-08T18:00:00Z"
	)

	// The one file whose conflict is a lifecycle disagreement, not a mixed
	// shape: ours says status: completed and theirs says status: aborted, from a
	// base of status: in_progress. Both are legal successors, so no rule may
	// pick between them and the file must keep quarantining.
	const lifecycleSlug = "claude-supervisor-698"
	const lifecycleReason = "lifecycle fields are not a legal transition"

	type triple struct {
		slug   string
		path   string
		base   string
		ours   string
		theirs string
	}

	var (
		ctx       context.Context
		vaultPath string
		router    http.Handler
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-merge-fixture")
		Expect(err).To(BeNil())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "25 Tasks"), 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Goals"), 0o700)).To(Succeed())

		index := vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, fakeMetrics{})
		router = handler.NewHandler(
			ctx, index, newTestStore(), secret, vaultPath, "25 Tasks", "test",
			&fakeWriter{}, libtime.NewCurrentDateTime(), &recordingPublisher{},
			&fakeRefClient{}, nil,
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	call := func(path, base, ours, theirs string) map[string]any {
		body := map[string]any{
			"path":  path,
			"apply": false,
			"base":  map[string]string{"content": base, "committer_time": theirsTime},
			"ours":  map[string]string{"content": ours, "committer_time": oursTime},
			"theirs": map[string]string{
				"content": theirs, "committer_time": theirsTime,
			},
		}
		encoded, err := json.Marshal(body)
		Expect(err).To(BeNil())
		request := httptest.NewRequest(
			http.MethodPost, "/api/v1/merge", bytes.NewBuffer(encoded),
		)
		request.Header.Set("X-Gateway-Secret", secret)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusOK), "path %s: %s", path, recorder.Body.String())

		var decoded map[string]any
		Expect(json.Unmarshal(recorder.Body.Bytes(), &decoded)).To(Succeed())
		return decoded
	}

	loadTriples := func() []triple {
		manifest, err := os.ReadFile(filepath.Join("testdata", "merge", "MANIFEST.tsv"))
		Expect(err).To(BeNil())

		var triples []triple
		for i, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
			if i == 0 {
				continue // header
			}
			fields := strings.Split(line, "\t")
			Expect(fields).To(HaveLen(7), "malformed manifest row %d", i)
			slug, taskPath := fields[0], fields[2]
			read := func(name string) string {
				b, readErr := os.ReadFile(filepath.Join("testdata", "merge", slug, name+".md"))
				Expect(readErr).To(BeNil())
				return string(b)
			}
			triples = append(triples, triple{
				slug:   slug,
				path:   taskPath,
				base:   read("base"),
				ours:   read("ours"),
				theirs: read("theirs"),
			})
		}
		return triples
	}

	It("resolves the 20 mixed shapes and keeps the one lifecycle conflict quarantined", func() {
		triples := loadTriples()
		Expect(triples).To(HaveLen(21))

		var quarantined []string
		for _, t := range triples {
			response := call(t.path, t.base, t.ours, t.theirs)
			if t.slug == lifecycleSlug {
				Expect(response["result"]).To(Equal("quarantined"), "lifecycle control %s", t.path)
				Expect(response["reason"]).To(Equal(lifecycleReason))
				continue
			}
			if response["result"] != "merged" {
				quarantined = append(quarantined, t.path)
			}
		}

		Expect(
			quarantined,
		).To(BeEmpty(), "these mixed-shape files did not resolve: %v", quarantined)
	})

	It("still quarantines a body both sides rewrote", func() {
		response := call(
			"25 Tasks/Contradictory.md",
			"---\ntitle: Contradictory\n---\nshared opening\n",
			"---\ntitle: Contradictory\n---\nours rewrote this line\n",
			"---\ntitle: Contradictory\n---\ntheirs rewrote this line\n",
		)
		Expect(response["result"]).To(Equal("quarantined"))
		Expect(response["reason"]).To(Equal("body conflict outside the frontmatter"))
	})
})
