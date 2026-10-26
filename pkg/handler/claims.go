// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strings"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/gorilla/mux"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/gitrest"
)

// DefaultLeaseSeconds is the lease a claim gets when the caller names none. Five
// minutes is long enough that a working run renews once rather than continuously,
// and short enough that a run that died frees its key quickly instead of blocking
// it until someone notices.
const DefaultLeaseSeconds = 300

// RenewLeadSeconds is how long before expiry a holder should renew. The response
// states the instant rather than leaving the caller to re-derive it.
const RenewLeadSeconds = 60

// MaxLeaseSeconds bounds a requested lease. The bound exists because
// time.Duration(ttl) * time.Second overflows int64 nanoseconds above ~9.2e9 s,
// and an overflowed lease renders lease_until in the past — a claim that is
// expired the moment it is taken. A day is longer than any run needs.
const MaxLeaseSeconds = 86400

// claimBodyLimit bounds the accepted claim request body. A claim body is three
// short strings; anything larger is a mistake, not a claim.
const claimBodyLimit = 64 * 1024

// ClaimDeps is what the claim handlers need: the index to resolve an identifier,
// the ref client that carries the claim to git-rest, and an injected clock so a
// lease instant is computed from a supplied time rather than the wall clock.
type ClaimDeps struct {
	Refs  gitrest.RefClient
	Clock libtime.CurrentDateTime
}

// claimRequest is the typed claim body. Its names are the typed API's, not
// git-rest's: holder/task_id/ttl_seconds are mapped onto cluster/task_id/
// lease_until by the handler, so a caller never sees the transport.
type claimRequest struct {
	// Holder is the actor taking the claim. git-rest calls this field cluster.
	Holder string `json:"holder"`
	// TTLSeconds is how long the lease lasts; absent means DefaultLeaseSeconds.
	TTLSeconds int `json:"ttl_seconds"`
	// LeaseToken is the compare-and-swap token from the acquire response. Empty
	// on acquire, mandatory on renew and release.
	LeaseToken string `json:"lease_token"`
	// TaskID is the claiming run's own task identifier. It is required for a
	// repository claim, which has no task of its own, and ignored for a task
	// claim where the addressed task supplies it.
	TaskID string `json:"task_id"`
}

// claimResponse is what every claim route answers with. It is deliberately the
// same shape for a task and a repository: the caller reads a holder and a lease,
// and the scope tells it which object it holds.
type claimResponse struct {
	Scope       string `json:"scope"`
	ID          string `json:"id"`
	Holder      string `json:"holder"`
	LeaseUntil  string `json:"lease_until"`
	RenewBefore string `json:"renew_before"`
	LeaseToken  string `json:"lease_token"`
}

// ClaimScope is one claim's address: the key the transport stores it under and
// the identifier the typed API answers with.
type ClaimScope struct {
	Scope string
	ID    string
	Key   string
}

// NewClaimScope builds an address for a claim. It is exported so the handlers
// can be exercised without an index: the resolver's job is to produce one of
// these, and a test that supplies it directly is testing the handler, not the
// resolver.
func NewClaimScope(scope string, id string, key string) ClaimScope {
	return ClaimScope{Scope: scope, ID: id, Key: key}
}

// taskClaimScope resolves a task identifier or filename slug to its claim
// address. found is false when the index knows no such task, which is a 404
// rather than a claim on a made-up key.
func taskClaimScope(index *vaultapi.Index, id string) (ClaimScope, bool) {
	item, found, _ := index.GetTask(id)
	if !found {
		return ClaimScope{}, false
	}
	return ClaimScope{Scope: "task", ID: item.ID, Key: "claims/task/" + item.ID}, true
}

// repoClaimScope builds the claim address for a repository. A repository is
// named by its owner and name rather than by any vault path.
//
// The key reproduces the one the task executor derives (lowercased owner and
// repo, slash-joined) rather than inventing a second spelling: the executor is
// the other holder of these refs, and two spellings of one repository would be
// two locks that do not exclude each other.
//
// ⚠️ The join is "/", not "-", and the difference is a correctness fix, not
// cosmetics. A hyphen is legal inside both components (validClaimComponent
// accepts it), so the hyphen join was lossy: owner "a-b" with repo "c" and
// owner "a" with repo "b-c" both derived "claims/repo/a-b-c" — two distinct
// repositories aliasing one lock, so the mutual exclusion silently did nothing.
// "/" is rejected inside a component, so the slash join cannot collide, and it
// matches the streak keys (claims/redrive/<taskid>/<phase>/<n>), which were
// already slash-separated.
//
// ⚠️ This must change in lockstep with agent-task-executor's DeriveClaimKey. A
// period where one side writes "claims/repo/a/b" and the other reads
// "claims/repo/a-b" is a period where the lock excludes nothing. In-flight
// claims carry a 5-minute lease, so a rolling deploy orphans each one for at
// most one lease and the next acquire takes it over.
func repoClaimScope(owner string, repo string) (ClaimScope, bool) {
	owner = strings.ToLower(owner)
	repo = strings.ToLower(repo)
	if !validClaimComponent(owner) || !validClaimComponent(repo) {
		return ClaimScope{}, false
	}
	return ClaimScope{
		Scope: "repo",
		ID:    owner + "/" + repo,
		Key:   "claims/repo/" + owner + "/" + repo,
	}, true
}

// validClaimComponent reports whether a name can be part of a claim key. The
// transport refuses anything outside this set anyway; checking here answers for
// an object that cannot exist instead of proxying the transport's refusal.
func validClaimComponent(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	if strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".lock") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// TaskClaimScope resolves the {id} path variable to a task claim scope.
func TaskClaimScope(index *vaultapi.Index) func(*http.Request) (ClaimScope, bool) {
	return func(r *http.Request) (ClaimScope, bool) {
		return taskClaimScope(index, mux.Vars(r)["id"])
	}
}

// RepoClaimScope resolves the {owner} and {repo} path variables to a repo claim
// scope. It fails only for a name that cannot be part of a claim key: a
// repository needs no index entry to be claimed.
func RepoClaimScope() func(*http.Request) (ClaimScope, bool) {
	return func(r *http.Request) (ClaimScope, bool) {
		vars := mux.Vars(r)
		return repoClaimScope(vars["owner"], vars["repo"])
	}
}

// exactClaim finds the claim for key among a prefix listing. git-rest answers a
// prefix match, so claims/task/task-1 also lists claims/task/task-10; only the
// exact ref is this object's claim.
func exactClaim(refs []gitrest.ClaimRef, key string) (gitrest.ClaimRef, bool) {
	for _, ref := range refs {
		if ref.Ref == "refs/"+key {
			return ref, true
		}
	}
	return gitrest.ClaimRef{}, false
}

// leaseExpired reports whether a stored lease_until has passed. An unparsable
// lease counts as expired: the ref carries no trustworthy expiry, and the
// alternative is a key nothing can claim again.
func leaseExpired(leaseUntil string, now time.Time) bool {
	until, err := time.Parse(time.RFC3339, leaseUntil)
	if err != nil {
		return true
	}
	return !until.After(now)
}

// renewBefore states when a holder should renew, or "" when the stored lease is
// unparsable and no instant can be derived from it.
func renewBefore(leaseUntil string) string {
	until, err := time.Parse(time.RFC3339, leaseUntil)
	if err != nil {
		return ""
	}
	return until.Add(-RenewLeadSeconds * time.Second).Format(time.RFC3339)
}

// AcquireClaimHandler takes a claim. A conflict means another holder has it and
// answers 409 claim_held with the holder that won.
func AcquireClaimHandler(
	deps ClaimDeps,
	scope func(*http.Request) (ClaimScope, bool),
) http.HandlerFunc {
	return claimWriteHandler(deps, scope, false)
}

// RenewClaimHandler extends a held claim. A conflict means the lease was lost
// and answers 409 lease_lost.
func RenewClaimHandler(
	deps ClaimDeps,
	scope func(*http.Request) (ClaimScope, bool),
) http.HandlerFunc {
	return claimWriteHandler(deps, scope, true)
}

// claimWriteHandler is acquire and renew in one body: the two differ only in the
// error a lost compare-and-swap reports, and in whether a token is required.
func claimWriteHandler(
	deps ClaimDeps,
	scope func(*http.Request) (ClaimScope, bool),
	renew bool,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claimed, ok := scope(r)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}

		request, ok := readClaimRequest(w, r, renew)
		if !ok {
			return
		}
		transportTaskID, ok := claimTransportTaskID(w, claimed, request)
		if !ok {
			return
		}

		ttl := claimLeaseSeconds(request.TTLSeconds)
		now := deps.Clock.Now().Time().UTC()
		leaseUntil := now.Add(time.Duration(ttl) * time.Second)

		expectedSHA := request.LeaseToken
		if !renew {
			sha, held, err := acquireToken(r.Context(), deps.Refs, claimed.Key, now)
			if err != nil {
				writeJSON(
					w,
					http.StatusInternalServerError,
					map[string]string{"error": "claim_read_failed"},
				)
				return
			}
			if held != nil {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error":       "claim_held",
					"holder":      held.Cluster,
					"lease_until": held.LeaseUntil,
				})
				return
			}
			expectedSHA = sha
		}

		ref, err := deps.Refs.PutClaimRef(r.Context(), claimed.Key, gitrest.ClaimRefRequest{
			ExpectedSHA: expectedSHA,
			Cluster:     request.Holder,
			TaskID:      transportTaskID,
			LeaseUntil:  leaseUntil.Format(time.RFC3339),
		})
		if err != nil {
			writeClaimWriteError(r.Context(), w, err, renew, deps.Refs, claimed.Key)
			return
		}

		writeJSON(w, claimWriteStatus(renew), claimResponse{
			Scope:       claimed.Scope,
			ID:          claimed.ID,
			Holder:      request.Holder,
			LeaseUntil:  leaseUntil.Format(time.RFC3339),
			RenewBefore: leaseUntil.Add(-RenewLeadSeconds * time.Second).Format(time.RFC3339),
			LeaseToken:  ref.SHA,
		})
	}
}

// decodeClaimRequest decodes the typed claim body, answering the request itself
// when it is unusable. ok is false once a response has been written.
//
// Unknown fields are refused rather than ignored. Ignoring them makes a
// misspelled key silent: a caller sending ttl_second instead of ttl_seconds
// would get the default lease and no indication that its value was dropped.
func decodeClaimRequest(w http.ResponseWriter, r *http.Request) (claimRequest, bool) {
	var request claimRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, claimBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return claimRequest{}, false
	}
	return request, true
}

// readClaimRequest decodes and validates the typed claim body, answering the
// request itself when it is unusable. ok is false once a response is written.
func readClaimRequest(
	w http.ResponseWriter,
	r *http.Request,
	renew bool,
) (claimRequest, bool) {
	request, ok := decodeClaimRequest(w, r)
	if !ok {
		return claimRequest{}, false
	}
	code := claimRequestError(request, renew)
	if code != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
		return claimRequest{}, false
	}
	return request, true
}

// claimRequestError names what is wrong with the body, or "" when nothing is.
func claimRequestError(request claimRequest, renew bool) string {
	switch {
	case request.Holder == "":
		return "invalid_holder"
	case request.TTLSeconds < 0 || request.TTLSeconds > MaxLeaseSeconds:
		return "invalid_ttl"
	case renew && request.LeaseToken == "":
		return "missing_lease_token"
	case !renew && request.LeaseToken != "":
		// Acquire starts from nothing. A token here would make the call a renew
		// with the wrong error vocabulary for a lost race.
		return "unexpected_lease_token"
	}
	return ""
}

// claimTransportTaskID is the task id the transport requires on every ref write.
// A task claim uses the addressed task, never a caller-supplied id; a repository
// has no task of its own, so the claiming run supplies one.
func claimTransportTaskID(
	w http.ResponseWriter,
	claimed ClaimScope,
	request claimRequest,
) (string, bool) {
	if claimed.Scope != "repo" {
		return claimed.ID, true
	}
	if request.TaskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_task_id"})
		return "", false
	}
	return request.TaskID, true
}

// acquireToken returns the compare-and-swap token an acquire must carry. held is
// the live claim that blocks it, or nil when the key is free — or when its lease
// has passed, in which case the token is the expired ref's own SHA and the
// acquire takes the claim over.
func acquireToken(
	ctx context.Context,
	refs gitrest.RefClient,
	key string,
	now time.Time,
) (string, *gitrest.ClaimRef, error) {
	current, err := refs.ListClaimRefs(ctx, key)
	if err != nil {
		return "", nil, err
	}
	ref, held := exactClaim(current, key)
	if !held {
		return "", nil, nil
	}
	if !leaseExpired(ref.LeaseUntil, now) {
		return "", &ref, nil
	}
	// An expired claim is free. git-rest stores lease_until and never compares
	// it, so without this takeover a holder that died would block the key
	// forever: every later acquire would lose the compare-and-swap and answer
	// 409 to a claim nobody holds. Carrying the expired ref's own SHA keeps
	// exactly one of several concurrent acquirers the winner.
	return ref.SHA, nil, nil
}

// ReleaseClaimHandler releases a claim. The token is mandatory: releasing
// someone else's claim because you hold a stale one is the failure the
// compare-and-swap exists to prevent.
func ReleaseClaimHandler(
	deps ClaimDeps,
	scope func(*http.Request) (ClaimScope, bool),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claimed, ok := scope(r)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}

		request, ok := decodeClaimRequest(w, r)
		if !ok {
			return
		}
		if request.LeaseToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_lease_token"})
			return
		}

		if err := deps.Refs.DeleteClaimRef(r.Context(), claimed.Key, request.LeaseToken); err != nil {
			writeClaimError(w, err, true)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ReadClaimHandler reports the current holder, or 404 when the claim is free.
func ReadClaimHandler(
	deps ClaimDeps,
	scope func(*http.Request) (ClaimScope, bool),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claimed, ok := scope(r)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		refs, err := deps.Refs.ListClaimRefs(r.Context(), claimed.Key)
		if err != nil {
			writeJSON(
				w,
				http.StatusInternalServerError,
				map[string]string{"error": "claim_read_failed"},
			)
			return
		}
		ref, held := exactClaim(refs, claimed.Key)
		if !held {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "claim_free"})
			return
		}
		writeJSON(w, http.StatusOK, claimResponse{
			Scope:       claimed.Scope,
			ID:          claimed.ID,
			Holder:      ref.Cluster,
			LeaseUntil:  ref.LeaseUntil,
			RenewBefore: renewBefore(ref.LeaseUntil),
			LeaseToken:  ref.SHA,
		})
	}
}

// claimLeaseSeconds is the requested lease, or the default when the caller names
// none.
func claimLeaseSeconds(ttl int) int {
	if ttl == 0 {
		return DefaultLeaseSeconds
	}
	return ttl
}

// claimWriteStatus is 201 for an acquire and 200 for a renew — renewing a claim
// creates nothing.
func claimWriteStatus(renew bool) int {
	if renew {
		return http.StatusOK
	}
	return http.StatusCreated
}

// writeClaimWriteError answers a failed claim write. An acquire that lost the
// compare-and-swap is the one failure whose answer names the winner.
func writeClaimWriteError(
	ctx context.Context,
	w http.ResponseWriter,
	err error,
	renew bool,
	refs gitrest.RefClient,
	key string,
) {
	if !renew && stderrors.Is(err, gitrest.ErrClaimConflict) {
		writeAcquireConflict(ctx, w, refs, key)
		return
	}
	writeClaimError(w, err, renew)
}

// writeAcquireConflict answers an acquire whose compare-and-swap lost, naming
// the holder that won when the claim is readable. The re-read can still find
// nothing — the winner may have released in between — so the bare code remains a
// legal answer, and the caller's reaction is the same either way.
func writeAcquireConflict(
	ctx context.Context,
	w http.ResponseWriter,
	refs gitrest.RefClient,
	key string,
) {
	body := map[string]string{"error": "claim_held"}
	if current, err := refs.ListClaimRefs(ctx, key); err == nil {
		if ref, held := exactClaim(current, key); held {
			body["holder"] = ref.Cluster
			body["lease_until"] = ref.LeaseUntil
		}
	}
	writeJSON(w, http.StatusConflict, body)
}

// writeClaimError maps a transport failure onto the typed error vocabulary. A
// lost compare-and-swap is not a server fault: on acquire another holder won, on
// renew or release this holder's lease expired.
func writeClaimError(w http.ResponseWriter, err error, renew bool) {
	if stderrors.Is(err, gitrest.ErrClaimConflict) {
		code := "claim_held"
		if renew {
			code = "lease_lost"
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": code})
		return
	}
	// A 4xx from git-rest is this caller's mistake — a ref name it refused, a
	// body it rejected — and answering 500 would tell the client to retry a
	// request that can never succeed.
	var statusErr *gitrest.StatusError
	if stderrors.As(err, &statusErr) && statusErr.StatusCode >= 400 && statusErr.StatusCode < 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_claim"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "claim_write_failed"})
}
