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
	"time"

	libtime "github.com/bborbe/time"
	"github.com/gorilla/mux"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-api/pkg/gitrest"
	"github.com/bborbe/vault-api/pkg/handler"
)

// fakeRefClient records what the handlers asked git-rest to do and answers with
// whatever the spec configured. It is deliberately a plain map rather than a
// counterfeiter fake: the claim handlers depend on three methods and the tests
// care about the key, the body and the error, not about call counts.
type fakeRefClient struct {
	putErr     error
	deleteErr  error
	listErr    error
	refs       []gitrest.ClaimRef
	lastKey    string
	lastPut    gitrest.ClaimRefRequest
	lastDelete string
	lastPrefix string
	putSHA     string
}

func (f *fakeRefClient) PutClaimRef(
	_ context.Context,
	key string,
	request gitrest.ClaimRefRequest,
) (gitrest.ClaimRef, error) {
	f.lastKey = key
	f.lastPut = request
	if f.putErr != nil {
		return gitrest.ClaimRef{}, f.putErr
	}
	sha := f.putSHA
	if sha == "" {
		sha = "sha-from-git-rest"
	}
	return gitrest.ClaimRef{Ref: "refs/" + key, SHA: sha}, nil
}

func (f *fakeRefClient) ListClaimRefs(
	_ context.Context,
	prefix string,
) ([]gitrest.ClaimRef, error) {
	f.lastPrefix = prefix
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.refs, nil
}

func (f *fakeRefClient) DeleteClaimRef(_ context.Context, key string, expectedSHA string) error {
	f.lastKey = key
	f.lastDelete = expectedSHA
	return f.deleteErr
}

var _ = Describe("Claim handlers", func() {
	var (
		ctx      context.Context
		refs     *fakeRefClient
		clock    libtime.CurrentDateTime
		router   *mux.Router
		recorder *httptest.ResponseRecorder
	)

	// The handlers take a scope resolver, so these specs exercise them without an
	// index: the task scope is a fixed address, which is what the resolver would
	// have produced for a known task.
	taskScope := func(_ *http.Request) (handler.ClaimScope, bool) {
		return handler.NewClaimScope("task", "task-1", "claims/task/task-1"), true
	}
	missingScope := func(_ *http.Request) (handler.ClaimScope, bool) {
		return handler.ClaimScope{}, false
	}

	buildRouter := func(scope func(*http.Request) (handler.ClaimScope, bool)) *mux.Router {
		deps := handler.ClaimDeps{Index: nil, Refs: refs, Clock: clock}
		r := mux.NewRouter()
		api := r.PathPrefix("/api/v1").Subrouter()
		api.Path("/tasks/{id}/claim").Methods(http.MethodPost).
			HandlerFunc(handler.AcquireClaimHandler(deps, scope))
		api.Path("/tasks/{id}/claim/renew").Methods(http.MethodPost).
			HandlerFunc(handler.RenewClaimHandler(deps, scope))
		api.Path("/tasks/{id}/claim").Methods(http.MethodDelete).
			HandlerFunc(handler.ReleaseClaimHandler(deps, scope))
		api.Path("/tasks/{id}/claim").Methods(http.MethodGet).
			HandlerFunc(handler.ReadClaimHandler(deps, scope))
		repoScope := handler.RepoClaimScope()
		api.Path("/repos/{owner}/{repo}/claim").Methods(http.MethodPost).
			HandlerFunc(handler.AcquireClaimHandler(deps, repoScope))
		return r
	}

	BeforeEach(func() {
		ctx = context.Background()
		refs = &fakeRefClient{}
		clock = libtime.NewCurrentDateTime()
		clock.SetNow(libtime.NewDateTime(2026, 10, 25, 12, 0, 0, 0, time.UTC))
		router = buildRouter(taskScope)
		recorder = httptest.NewRecorder()
	})

	decode := func() map[string]string {
		var body map[string]string
		Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
		return body
	}

	post := func(path string, body string) {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		request = request.WithContext(ctx)
		router.ServeHTTP(recorder, request)
	}

	Describe("acquire", func() {
		It("takes the claim and answers the typed lease, not the transport body", func() {
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev","ttl_seconds":300}`)

			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastKey).To(Equal("claims/task/task-1"))
			Expect(refs.lastPut.Cluster).To(Equal("nukedev"))
			Expect(refs.lastPut.TaskID).To(Equal("task-1"))
			Expect(refs.lastPut.ExpectedSHA).To(BeEmpty())
			Expect(refs.lastPut.LeaseUntil).To(Equal("2026-10-25T12:05:00Z"))

			body := decode()
			Expect(body["scope"]).To(Equal("task"))
			Expect(body["holder"]).To(Equal("nukedev"))
			Expect(body["lease_until"]).To(Equal("2026-10-25T12:05:00Z"))
			Expect(body["renew_before"]).To(Equal("2026-10-25T12:04:00Z"))
			Expect(body["lease_token"]).To(Equal("sha-from-git-rest"))
		})

		It("defaults the lease when the caller names no ttl", func() {
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastPut.LeaseUntil).To(Equal("2026-10-25T12:05:00Z"))
		})

		It("answers 409 claim_held when another holder won the compare-and-swap", func() {
			refs.putErr = gitrest.ErrClaimConflict
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode()["error"]).To(Equal("claim_held"))
		})

		It("answers 409 naming the holder when a live claim is already held", func() {
			refs.refs = []gitrest.ClaimRef{{
				Ref:        "refs/claims/task/task-1",
				SHA:        "held-sha",
				Cluster:    "sun",
				LeaseUntil: "2026-10-25T12:04:59Z",
			}}
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode()["error"]).To(Equal("claim_held"))
			Expect(decode()["holder"]).To(Equal("sun"))
			Expect(decode()["lease_until"]).To(Equal("2026-10-25T12:04:59Z"))
			Expect(refs.lastPut.Cluster).To(BeEmpty(), "a live claim must not be written over")
		})

		It("takes over a claim whose lease has expired", func() {
			// git-rest stores lease_until and never compares it, so without this
			// takeover a dead holder would block the key forever.
			refs.refs = []gitrest.ClaimRef{{
				Ref:        "refs/claims/task/task-1",
				SHA:        "expired-sha",
				Cluster:    "sun",
				LeaseUntil: "2026-10-25T11:00:00Z",
			}}
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastPut.ExpectedSHA).To(Equal("expired-sha"))
			Expect(decode()["holder"]).To(Equal("nukedev"))
		})

		It("answers 400 for a ttl above the bound", func() {
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev","ttl_seconds":86401}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("invalid_ttl"))
		})

		It("answers 400 when acquire carries a lease token", func() {
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev","lease_token":"tok"}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("unexpected_lease_token"))
		})

		It("answers 400 when the holder is missing", func() {
			post("/api/v1/tasks/task-1/claim", `{}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("invalid_holder"))
		})

		It("answers 404 when the addressed object does not exist", func() {
			router = buildRouter(missingScope)
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(decode()["error"]).To(Equal("not_found"))
		})
	})

	Describe("renew", func() {
		It("extends the lease using the token as the compare-and-swap", func() {
			post("/api/v1/tasks/task-1/claim/renew", `{"holder":"nukedev","lease_token":"tok"}`)
			Expect(recorder.Code).To(Equal(http.StatusOK), "renewing creates nothing")
			Expect(refs.lastPut.ExpectedSHA).To(Equal("tok"))
		})

		It("answers 409 lease_lost, never claim_held, when the lease expired", func() {
			refs.putErr = gitrest.ErrClaimConflict
			post("/api/v1/tasks/task-1/claim/renew", `{"holder":"nukedev","lease_token":"tok"}`)
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode()["error"]).To(Equal("lease_lost"))
		})

		It("answers 400 without a token", func() {
			post("/api/v1/tasks/task-1/claim/renew", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("missing_lease_token"))
		})
	})

	Describe("release", func() {
		It("releases with the token and answers 204", func() {
			request := httptest.NewRequest(
				http.MethodDelete,
				"/api/v1/tasks/task-1/claim",
				bytes.NewBufferString(`{"lease_token":"tok"}`),
			)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusNoContent))
			Expect(refs.lastDelete).To(Equal("tok"))
		})

		It("answers 409 lease_lost on a stale token", func() {
			refs.deleteErr = gitrest.ErrClaimConflict
			request := httptest.NewRequest(
				http.MethodDelete,
				"/api/v1/tasks/task-1/claim",
				bytes.NewBufferString(`{"lease_token":"stale"}`),
			)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusConflict))
			Expect(decode()["error"]).To(Equal("lease_lost"))
		})
	})

	Describe("read", func() {
		It("reports the current holder", func() {
			refs.refs = []gitrest.ClaimRef{{
				Ref:        "refs/claims/task/task-1",
				SHA:        "abc",
				Cluster:    "sun",
				LeaseUntil: "2026-10-25T12:05:00Z",
			}}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-1/claim", nil)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			body := decode()
			Expect(body["holder"]).To(Equal("sun"))
			Expect(body["lease_token"]).To(Equal("abc"))
			Expect(body["renew_before"]).To(Equal("2026-10-25T12:04:00Z"))
		})

		It("answers 404 claim_free when nobody holds it", func() {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/task-1/claim", nil)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(decode()["error"]).To(Equal("claim_free"))
		})
	})

	Describe("repository claims", func() {
		It("addresses the claim by owner and repo, and requires the run's task id", func() {
			post("/api/v1/repos/bborbe/git-rest/claim", `{"holder":"nukedev","task_id":"run-1"}`)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastKey).To(Equal("claims/repo/bborbe-git-rest"))
			Expect(refs.lastPut.TaskID).To(Equal("run-1"))
			Expect(decode()["scope"]).To(Equal("repo"))
			Expect(decode()["id"]).To(Equal("bborbe/git-rest"))
		})

		It("answers 400 when the run supplies no task id", func() {
			post("/api/v1/repos/bborbe/git-rest/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("missing_task_id"))
		})
	})
})
