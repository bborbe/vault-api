// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
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
		deps := handler.ClaimDeps{Refs: refs, Clock: clock}
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
		api.Path("/repos/{owner}/{repo}/claim/renew").Methods(http.MethodPost).
			HandlerFunc(handler.RenewClaimHandler(deps, repoScope))
		api.Path("/repos/{owner}/{repo}/claim").Methods(http.MethodDelete).
			HandlerFunc(handler.ReleaseClaimHandler(deps, repoScope))
		api.Path("/repos/{owner}/{repo}/claim").Methods(http.MethodGet).
			HandlerFunc(handler.ReadClaimHandler(deps, repoScope))
		api.Path("/claims").Methods(http.MethodGet).
			HandlerFunc(handler.ListClaimsHandler(deps))
		prScope := handler.PRClaimScope()
		api.Path("/repos/{owner}/{repo}/pulls/{number}/claim").Methods(http.MethodPost).
			HandlerFunc(handler.AcquireClaimHandler(deps, prScope))
		api.Path("/repos/{owner}/{repo}/pulls/{number}/claim/renew").Methods(http.MethodPost).
			HandlerFunc(handler.RenewClaimHandler(deps, prScope))
		api.Path("/repos/{owner}/{repo}/pulls/{number}/claim").Methods(http.MethodDelete).
			HandlerFunc(handler.ReleaseClaimHandler(deps, prScope))
		api.Path("/repos/{owner}/{repo}/pulls/{number}/claim").Methods(http.MethodGet).
			HandlerFunc(handler.ReadClaimHandler(deps, prScope))
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

		It("takes over a claim whose stored lease cannot be parsed", func() {
			// A ref carrying no trustworthy expiry would otherwise block the key
			// forever, so the unparsable case fails open like an expired one.
			refs.refs = []gitrest.ClaimRef{{
				Ref:        "refs/claims/task/task-1",
				SHA:        "broken-sha",
				Cluster:    "sun",
				LeaseUntil: "not-a-timestamp",
			}}
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastPut.ExpectedSHA).To(Equal("broken-sha"))
		})

		It("answers 500 when the claim cannot be read before the write", func() {
			refs.listErr = stderrors.New("git-rest unreachable")
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusInternalServerError))
			Expect(decode()["error"]).To(Equal("claim_read_failed"))
			Expect(refs.lastPut.Cluster).To(BeEmpty(), "no write may follow a failed read")
		})

		It("answers 400 when git-rest refuses the write as the caller's mistake", func() {
			refs.putErr = &gitrest.StatusError{
				StatusCode: http.StatusBadRequest,
				Body:       "bad ref name",
			}
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("invalid_claim"))
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

		It("refuses an unknown field instead of silently ignoring it", func() {
			// A misspelled key would otherwise fall back to the default lease
			// while the caller believes it set one.
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev","ttl_second":60}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("invalid_body"))
			Expect(refs.lastPut.Cluster).To(BeEmpty(), "a refused body must not be written")
		})

		It("ignores a sibling key that shares the requested key as a prefix", func() {
			// git-rest matches the listing by prefix, so an acquire on task-1 can
			// come back holding task-10's ref. Reading that as a live claim would
			// answer 409 for a key nobody holds.
			refs.refs = []gitrest.ClaimRef{{
				Ref:        "refs/claims/task/task-10",
				SHA:        "sibling-sha",
				Cluster:    "sun",
				LeaseUntil: "2026-10-25T12:04:59Z",
			}}
			post("/api/v1/tasks/task-1/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(
				refs.lastPut.ExpectedSHA,
			).To(BeEmpty(), "a sibling key is not a lease to take over")
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
			Expect(refs.lastKey).To(Equal("claims/repo/bborbe/git-rest"))
			Expect(refs.lastPut.TaskID).To(Equal("run-1"))
			Expect(decode()["scope"]).To(Equal("repo"))
			Expect(decode()["id"]).To(Equal("bborbe/git-rest"))
		})

		It("derives the executor's key, so both name one ref", func() {
			// The executor lowercases owner and repo before joining them with a
			// hyphen; a second spelling here would be a second lock.
			post("/api/v1/repos/BBorbe/Git-Rest/claim", `{"holder":"nukedev","task_id":"run-1"}`)
			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastKey).To(Equal("claims/repo/bborbe/git-rest"))
			Expect(decode()["id"]).To(Equal("bborbe/git-rest"))
		})

		It("answers 404 for a name that cannot be part of a claim key", func() {
			post("/api/v1/repos/.hidden/git-rest/claim", `{"holder":"nukedev","task_id":"run-1"}`)
			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(decode()["error"]).To(Equal("not_found"))
			Expect(refs.lastKey).To(BeEmpty(), "an unaddressable name must not reach the transport")
		})

		It("answers 400 when the run supplies no task id", func() {
			post("/api/v1/repos/bborbe/git-rest/claim", `{"holder":"nukedev"}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("missing_task_id"))
		})

		It("renews, reads and releases the same repository claim", func() {
			refs.refs = []gitrest.ClaimRef{{
				Ref:        "refs/claims/repo/bborbe/git-rest",
				SHA:        "held-sha",
				Cluster:    "sun",
				LeaseUntil: "2026-10-25T12:05:00Z",
			}}

			post("/api/v1/repos/bborbe/git-rest/claim/renew",
				`{"holder":"nukedev","task_id":"run-1","lease_token":"held-sha"}`)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(refs.lastPut.ExpectedSHA).To(Equal("held-sha"))

			recorder = httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/repos/bborbe/git-rest/claim",
				nil,
			)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode()["holder"]).To(Equal("sun"))

			recorder = httptest.NewRecorder()
			request = httptest.NewRequest(
				http.MethodDelete,
				"/api/v1/repos/bborbe/git-rest/claim",
				bytes.NewBufferString(`{"lease_token":"held-sha"}`),
			)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusNoContent))
			Expect(refs.lastDelete).To(Equal("held-sha"))
		})
	})

	Describe("per-PR claim", func() {
		acquire := func(path string, body string) {
			request := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
			router.ServeHTTP(recorder, request.WithContext(ctx))
		}

		It("claims the pull request, not the whole repository", func() {
			acquire(
				"/api/v1/repos/bborbe/git-rest/pulls/7/claim",
				`{"holder":"nukedev","ttl_seconds":300,"task_id":"task-9"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastKey).To(Equal("claims/pr/bborbe/git-rest/7"))
			Expect(decode()["scope"]).To(Equal("pr"))
			Expect(decode()["id"]).To(Equal("bborbe/git-rest/7"))
		})

		It("sends the caller's task id to the transport, never the claim's own address", func() {
			// The regression this exists for: with the task-id mapping written as
			// `scope != "repo"`, a per-PR claim sent "bborbe/git-rest/7" as the task
			// id. Recover pairs a repo claim with its per-PR claim BY TASK ID, so a
			// bogus id makes the pair read as two unrelated singles.
			acquire(
				"/api/v1/repos/bborbe/git-rest/pulls/7/claim",
				`{"holder":"nukedev","task_id":"task-9"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastPut.TaskID).To(Equal("task-9"))
			Expect(refs.lastPut.TaskID).NotTo(Equal("bborbe/git-rest/7"))
		})

		It("refuses a per-PR claim that carries no task id", func() {
			acquire("/api/v1/repos/bborbe/git-rest/pulls/7/claim", `{"holder":"nukedev"}`)

			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("missing_task_id"))
		})

		It("lowercases owner and repo but leaves the number alone, as the executor does", func() {
			acquire(
				"/api/v1/repos/BBorbe/Git-Rest/pulls/7/claim",
				`{"holder":"nukedev","task_id":"task-9"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusCreated))
			Expect(refs.lastKey).To(Equal("claims/pr/bborbe/git-rest/7"))
		})

		It("refuses a number outside the claim component set rather than inventing a key", func() {
			// The executor validates the number as a claim component, not as an
			// integer, and does not lowercase it. Uppercase is therefore rejected on
			// both sides; accepting it here would split the key space.
			acquire(
				"/api/v1/repos/bborbe/git-rest/pulls/7X/claim",
				`{"holder":"nukedev","task_id":"task-9"}`,
			)

			Expect(recorder.Code).To(Equal(http.StatusNotFound))
			Expect(decode()["error"]).To(Equal("not_found"))
		})

		It("renews and releases the per-PR claim through its own address", func() {
			refs.refs = []gitrest.ClaimRef{
				{
					Ref:        "refs/claims/pr/bborbe/git-rest/7",
					SHA:        "pr-sha",
					Cluster:    "nukedev",
					TaskID:     "task-9",
					LeaseUntil: "2026-10-25T12:05:00Z",
				},
			}

			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/repos/bborbe/git-rest/pulls/7/claim",
				nil,
			)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(decode()["lease_token"]).To(Equal("pr-sha"))

			recorder = httptest.NewRecorder()
			request = httptest.NewRequest(
				http.MethodDelete,
				"/api/v1/repos/bborbe/git-rest/pulls/7/claim",
				bytes.NewBufferString(`{"lease_token":"pr-sha"}`),
			)
			router.ServeHTTP(recorder, request.WithContext(ctx))
			Expect(recorder.Code).To(Equal(http.StatusNoContent))
			Expect(refs.lastKey).To(Equal("claims/pr/bborbe/git-rest/7"))
			Expect(refs.lastDelete).To(Equal("pr-sha"))
		})
	})

	Describe("list", func() {
		var get func(path string)

		listBody := func() handler.ClaimListResponse {
			var body handler.ClaimListResponse
			Expect(json.Unmarshal(recorder.Body.Bytes(), &body)).To(Succeed())
			return body
		}

		BeforeEach(func() {
			get = func(path string) {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				router.ServeHTTP(recorder, request.WithContext(ctx))
			}
		})

		It("lists every claim under the claims prefix, not one scope at a time", func() {
			refs.refs = []gitrest.ClaimRef{
				{
					Ref:        "refs/claims/task/task-1",
					SHA:        "sha-task",
					Cluster:    "nukedev",
					TaskID:     "task-1",
					LeaseUntil: "2026-10-25T12:05:00Z",
				},
				{
					Ref:        "refs/claims/repo/bborbe/git-rest",
					SHA:        "sha-repo",
					Cluster:    "nukedev",
					TaskID:     "task-2",
					LeaseUntil: "2026-10-25T12:05:00Z",
				},
				{
					Ref:        "refs/claims/pr/bborbe/git-rest/7",
					SHA:        "sha-pr",
					Cluster:    "nukedev",
					TaskID:     "task-2",
					LeaseUntil: "2026-10-25T12:05:00Z",
				},
			}

			get("/api/v1/claims")

			Expect(recorder.Code).To(Equal(http.StatusOK))
			// One call, whole namespace: the prefix is the claims root, not a scope.
			Expect(refs.lastPrefix).To(Equal("claims/"))

			body := listBody()
			Expect(body.Total).To(Equal(3))
			Expect(body.Skipped).To(Equal(0))
			Expect(body.Items).To(HaveLen(3))

			// Sorted by scope then id, so a caller diffing two listings does not
			// see churn that is only ordering.
			Expect(body.Items[0].Scope).To(Equal("pr"))
			Expect(body.Items[0].ID).To(Equal("bborbe/git-rest/7"))
			Expect(body.Items[1].Scope).To(Equal("repo"))
			Expect(body.Items[1].ID).To(Equal("bborbe/git-rest"))
			Expect(body.Items[2].Scope).To(Equal("task"))
			Expect(body.Items[2].ID).To(Equal("task-1"))
		})

		It("answers the fields a restarting executor needs to act on a listed claim", func() {
			refs.refs = []gitrest.ClaimRef{
				{
					Ref:        "refs/claims/repo/bborbe/git-rest",
					SHA:        "sha-repo",
					Cluster:    "nukedev",
					TaskID:     "task-2",
					LeaseUntil: "2026-10-25T12:05:00Z",
				},
			}

			get("/api/v1/claims")

			item := listBody().Items[0]
			// task_id is the field the listing exists for: Recover groups by it, so
			// without it a repo claim cannot be attributed to the run that holds it.
			Expect(item.TaskID).To(Equal("task-2"))
			Expect(item.Holder).To(Equal("nukedev"))
			Expect(item.LeaseUntil).To(Equal("2026-10-25T12:05:00Z"))
			Expect(item.RenewBefore).To(Equal("2026-10-25T12:04:00Z"))
			// The compare-and-swap token, so a listed claim can be renewed or
			// released without a second read.
			Expect(item.LeaseToken).To(Equal("sha-repo"))
		})

		It("addresses a repo claim as owner/name, which the slash join made possible", func() {
			// Under the hyphen join this key was "claims/repo/bborbe-git-rest" and
			// could not be split back into owner and repo at all.
			refs.refs = []gitrest.ClaimRef{
				{Ref: "refs/claims/repo/bborbe/git-rest", SHA: "s", Cluster: "nukedev"},
			}

			get("/api/v1/claims")

			Expect(listBody().Items[0].ID).To(Equal("bborbe/git-rest"))
		})

		It("narrows to one holder and leaves the rest out", func() {
			refs.refs = []gitrest.ClaimRef{
				{Ref: "refs/claims/task/task-1", SHA: "a", Cluster: "nukedev", TaskID: "task-1"},
				{Ref: "refs/claims/task/task-2", SHA: "b", Cluster: "sun", TaskID: "task-2"},
			}

			get("/api/v1/claims?holder=sun")

			body := listBody()
			Expect(body.Total).To(Equal(1))
			Expect(body.Items[0].ID).To(Equal("task-2"))
			Expect(body.Items[0].Holder).To(Equal("sun"))
			// A claim filtered out by holder is not "skipped": skipped means "not a
			// claim", and conflating the two would hide a real key-shape change.
			Expect(body.Skipped).To(Equal(0))
		})

		It("counts refs under the prefix that are not claims instead of answering them", func() {
			refs.refs = []gitrest.ClaimRef{
				{Ref: "refs/claims/task/task-1", SHA: "a", Cluster: "nukedev", TaskID: "task-1"},
				// A redrive streak key: a counter under the same prefix, never
				// acquired or released, so answering it as a claim would invite a
				// caller to renew something that is not a lock.
				{Ref: "refs/claims/redrive/task-1/planning/1", SHA: "b", Cluster: "nukedev"},
				{Ref: "refs/claims/redrive-reason/task-1", SHA: "c", Cluster: "nukedev"},
			}

			get("/api/v1/claims")

			body := listBody()
			Expect(body.Total).To(Equal(1))
			Expect(body.Skipped).To(Equal(2))
			Expect(body.Items[0].ID).To(Equal("task-1"))
		})

		It("counts a malformed claim key as skipped rather than splitting it wrongly", func() {
			refs.refs = []gitrest.ClaimRef{
				// Two components where three are required: not addressable, so not
				// answerable as a per-PR claim.
				{Ref: "refs/claims/pr/bborbe/git-rest", SHA: "a", Cluster: "nukedev"},
				{Ref: "refs/claims/task/", SHA: "b", Cluster: "nukedev"},
			}

			get("/api/v1/claims")

			body := listBody()
			Expect(body.Total).To(Equal(0))
			Expect(body.Skipped).To(Equal(2))
			Expect(body.Items).To(BeEmpty())
		})

		It("refuses an unknown filter instead of ignoring it", func() {
			get("/api/v1/claims?cluster=nukedev")

			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("unknown_filter"))
			Expect(decode()["filter"]).To(Equal("cluster"))
		})

		It("refuses an empty holder rather than returning every claim", func() {
			get("/api/v1/claims?holder=")

			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(decode()["error"]).To(Equal("invalid_filter"))
		})

		It("answers 500 when the transport read fails, not an empty list", func() {
			refs.listErr = stderrors.New("git-rest unavailable")

			get("/api/v1/claims")

			Expect(recorder.Code).To(Equal(http.StatusInternalServerError))
			Expect(decode()["error"]).To(Equal("claim_read_failed"))
		})
	})
})
