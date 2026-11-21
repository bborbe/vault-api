// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"sort"
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
	// SpawnedAt records the instant the holder created the claim's Job. It is
	// accepted only on renew: an acquire starts a claim whose holder has not
	// spawned yet, so a marker sent with it is dropped rather than stored.
	SpawnedAt string `json:"spawned_at"`
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
	// SpawnedAt is absent on a claim whose holder has not created its Job, and on
	// every claim written before the field existed. A reader treats absence as
	// "this claim proves no Job was started", never as an error.
	SpawnedAt string `json:"spawned_at,omitempty"`
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

// prClaimScope builds the claim address for one pull request.
//
// A per-PR claim is what lets two reviews of the same repository run at once:
// the repository key alone serialises every review of a repository, so a second
// PR's review waits behind the first for as long as it renews. The per-PR key
// narrows the lock to the pull request, and the executor pairs the two — it
// holds both, and recovers them together by task id.
//
// ⚠️ The derivation reproduces agent-task-executor's DeriveClaimKeysWithBody
// exactly, including two details that look like accidents and are not:
//
//   - owner and repo are lowercased, the number is NOT. The executor lowercases
//     only the first two; the number comes from the pull-request body and is
//     validated as-is, so lowercasing it here would accept an input the executor
//     rejects and split the key space between the two services.
//   - the number is validated as a claim component, not parsed as an integer.
//     The executor runs validateClaimComponent over it, so a stricter integer
//     check here would refuse a key the executor can write.
func prClaimScope(owner string, repo string, number string) (ClaimScope, bool) {
	owner = strings.ToLower(owner)
	repo = strings.ToLower(repo)
	if !validClaimComponent(owner) || !validClaimComponent(repo) ||
		!validClaimComponent(number) {
		return ClaimScope{}, false
	}
	return ClaimScope{
		Scope: "pr",
		ID:    owner + "/" + repo + "/" + number,
		Key:   "claims/pr/" + owner + "/" + repo + "/" + number,
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

// PRClaimScope resolves {owner}, {repo} and {number} to a per-PR claim scope.
// Like a repository, a pull request needs no index entry to be claimed.
func PRClaimScope() func(*http.Request) (ClaimScope, bool) {
	return func(r *http.Request) (ClaimScope, bool) {
		vars := mux.Vars(r)
		return prClaimScope(vars["owner"], vars["repo"], vars["number"])
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

		spawnedAt, ok := claimWriteSpawnedAt(w, r, deps, claimed, request, renew)
		if !ok {
			return
		}

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
			SpawnedAt:   spawnedAt,
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
			SpawnedAt:   spawnedAt,
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
// and a pull request have no task of their own, so the claiming run supplies one.
//
// ⚠️ The test is "is this a task claim", not "is this not a repository claim".
// Written the other way round — `if scope != "repo"` — adding the per-PR scope
// would have sent the claim's own address, "owner/repo/7", to the transport as a
// task id. That writes a claim whose task_id is not a task, and it breaks the
// executor's recovery silently: Recover pairs a repository claim with its per-PR
// claim by task id, so a bogus id makes the pair read as two unrelated singles
// that are then adopted, fenced or released separately.
func claimTransportTaskID(
	w http.ResponseWriter,
	claimed ClaimScope,
	request claimRequest,
) (string, bool) {
	if claimed.Scope == "task" {
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

// claimWriteSpawnedAt is the spawned_at a claim write carries. An acquire drops
// any value the caller sent: a marker means "this holder created a Job", which
// cannot be true at acquisition, and dropping it is what makes the field
// discriminate a post-spawn write from an acquisition-time one.
//
// A renew keeps the stored value when it names none. Every tick renew comes from
// a holder with no opinion on the field, and rewriting the ref without it would
// erase the only evidence that this claim's holder ever spawned.
//
// ok is false once a response has been written.
func claimWriteSpawnedAt(
	w http.ResponseWriter,
	r *http.Request,
	deps ClaimDeps,
	claimed ClaimScope,
	request claimRequest,
	renew bool,
) (string, bool) {
	if !renew {
		return "", true
	}
	preserved, err := renewSpawnedAt(r.Context(), deps.Refs, claimed.Key, request.SpawnedAt)
	if err != nil {
		writeJSON(
			w,
			http.StatusInternalServerError,
			map[string]string{"error": "claim_read_failed"},
		)
		return "", false
	}
	return preserved, true
}

// renewSpawnedAt is the spawned_at a renew writes. A renew that names one uses
// it; a renew that names none keeps the stored one.
//
// Keeping it is what makes the marker survive the holder's own renewal loop: the
// lease loop renews every 60 s and knows nothing about the marker, so a
// read-modify-write that dropped it would erase the evidence within a minute of
// the spawn. A failed read fails the renew rather than writing a claim without
// the marker — silently losing it is the defect, and the caller can retry.
//
// The read is not a race: the renew's compare-and-swap is against the caller's
// own lease token, so a claim that moved between the read and the write is
// refused as lease_lost, which is the correct answer.
func renewSpawnedAt(
	ctx context.Context,
	refs gitrest.RefClient,
	key string,
	requested string,
) (string, error) {
	if requested != "" {
		return requested, nil
	}
	current, err := refs.ListClaimRefs(ctx, key)
	if err != nil {
		return "", err
	}
	ref, held := exactClaim(current, key)
	if !held {
		return "", nil
	}
	return ref.SpawnedAt, nil
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
			SpawnedAt:   ref.SpawnedAt,
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

// claimsPrefix is the ref prefix every claim lives under. It is the whole
// namespace, not one scope: a listing that had to be asked per scope would need
// three calls to answer the one question it exists for.
const claimsPrefix = "claims/"

// refsPrefix is the leading path segment git-rest puts in front of every ref it
// reports, and which the keys this package builds do not carry.
const refsPrefix = "refs/"

// ClaimSummary is one claim as the listing reports it.
//
// It carries claimResponse's identity fields plus task_id, and it deliberately
// omits the transport key: the key is this API's business, never the caller's.
// A caller addresses a claim by scope and id, which is exactly what the renew
// and release routes accept — so a listed claim is actionable without the caller
// ever learning how it is stored.
//
// task_id is the field that makes the listing worth having. A repository claim
// belongs to no task, so the claiming run's own task identifier is what the
// transport stores; a restarting executor groups its claims by it to recover one
// run's holdings together rather than key by key.
type ClaimSummary struct {
	Scope       string `json:"scope"`
	ID          string `json:"id"`
	Holder      string `json:"holder"`
	TaskID      string `json:"task_id"`
	LeaseUntil  string `json:"lease_until"`
	RenewBefore string `json:"renew_before"`
	LeaseToken  string `json:"lease_token"`
	// SpawnedAt is absent on a claim whose holder has not created its Job.
	SpawnedAt string `json:"spawned_at,omitempty"`
}

// ClaimListResponse is the body of GET /api/v1/claims.
//
// It carries no index_age_seconds, unlike the task and goal listings: claims are
// read live from git-rest and no index describes them, so an age here would be a
// fabricated number a caller could gate on. Skipped counts refs under the claims
// prefix that are not addressable claims — counted rather than dropped, for the
// same reason the task index counts unparsable pages instead of coercing them.
type ClaimListResponse struct {
	Items   []ClaimSummary `json:"items"`
	Total   int            `json:"total"`
	Skipped int            `json:"skipped"`
}

// claimScopeFromKey parses a claim key back into its typed address. It is the
// inverse of taskClaimScope, repoClaimScope and the executor's per-PR
// derivation.
//
// ⚠️ This inverse is computable only because the components are
// slash-separated. Under the earlier hyphen join, "claims/repo/a-b-c" could not
// be split back into an owner and a repo at all — the same lossiness that made
// the lock exclude nothing also made a listing unable to return an addressable
// id. That is why this route was sequenced after the key repair rather than
// alongside it: a listing of unaddressable claims answers nothing.
//
// A key that is not one of the three claim shapes is reported as not a claim.
// The redrive streak keys (claims/redrive/<taskid>/<phase>/<n>) land here: they
// live under the same prefix but are counters, never acquired or released, so
// answering them as claims would invite a caller to renew something that is not
// a lock.
func claimScopeFromKey(key string) (ClaimScope, bool) {
	switch {
	case strings.HasPrefix(key, "claims/task/"):
		id := strings.TrimPrefix(key, "claims/task/")
		if id == "" {
			return ClaimScope{}, false
		}
		return ClaimScope{Scope: "task", ID: id, Key: key}, true
	case strings.HasPrefix(key, "claims/repo/"):
		parts := strings.Split(strings.TrimPrefix(key, "claims/repo/"), "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return ClaimScope{}, false
		}
		return ClaimScope{Scope: "repo", ID: parts[0] + "/" + parts[1], Key: key}, true
	case strings.HasPrefix(key, "claims/pr/"):
		parts := strings.Split(strings.TrimPrefix(key, "claims/pr/"), "/")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return ClaimScope{}, false
		}
		return ClaimScope{
			Scope: "pr",
			ID:    parts[0] + "/" + parts[1] + "/" + parts[2],
			Key:   key,
		}, true
	}
	return ClaimScope{}, false
}

// ListClaimsHandler enumerates claims, optionally narrowed to one holder.
//
// The route exists because a claim surface that can only be read one key at a
// time cannot answer the question a restarting executor actually asks: which
// claims do I already hold? Without it, an executor that restarts cannot see the
// refs it left behind and has to wait out every lease instead of re-adopting or
// fencing its own work.
//
// Filtering by holder is a convenience, not a security boundary: the gateway
// secret is what separates callers, and a holder that names someone else simply
// sees someone else's claims. It is offered because the caller's own filter is
// the common case and doing it server-side keeps the response small.
//
// An expired claim is reported with its stored lease_until rather than flagged
// or omitted. Expiry is a comparison against the caller's own clock, and
// computing it here would bake this server's clock into an answer that goes
// stale the moment it is sent — the caller already has lease_until and knows
// what now is.
func ListClaimsHandler(deps ClaimDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		holder, ok := claimListHolder(w, r)
		if !ok {
			return
		}
		refs, err := deps.Refs.ListClaimRefs(r.Context(), claimsPrefix)
		if err != nil {
			writeJSON(
				w,
				http.StatusInternalServerError,
				map[string]string{"error": "claim_read_failed"},
			)
			return
		}
		items, skipped := claimSummaries(refs, holder)
		writeJSON(w, http.StatusOK, ClaimListResponse{
			Items:   items,
			Total:   len(items),
			Skipped: skipped,
		})
	}
}

// claimListHolder reads the listing's only filter, answering the request itself
// when the query is unusable. ok is false once a response has been written.
func claimListHolder(w http.ResponseWriter, r *http.Request) (string, bool) {
	query := r.URL.Query()
	for name := range query {
		if name != "holder" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "unknown_filter",
				"filter": name,
			})
			return "", false
		}
		// A declared filter sent with an empty value is refused rather than
		// treated as unset: `?holder=` reads as "narrow to nothing" and returning
		// every claim instead is the silent widening the task listing's own
		// empty-value branch exists to prevent.
		if query.Get(name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "invalid_filter",
				"filter": name,
				"reason": "a filter must carry a value; omit the parameter to leave it unset",
			})
			return "", false
		}
	}
	return query.Get("holder"), true
}

// claimSummaries projects transport refs onto the listing and counts the refs
// that are not addressable claims.
func claimSummaries(refs []gitrest.ClaimRef, holder string) ([]ClaimSummary, int) {
	items := make([]ClaimSummary, 0, len(refs))
	skipped := 0
	for _, ref := range refs {
		claimed, ok := claimScopeFromKey(strings.TrimPrefix(ref.Ref, refsPrefix))
		if !ok {
			// Counted whether or not a holder filter is active: a ref that is not
			// a claim is not a claim for anybody, and a filter that hid the count
			// would make an unexpected key shape invisible exactly when someone
			// is looking.
			skipped++
			continue
		}
		if holder != "" && ref.Cluster != holder {
			continue
		}
		items = append(items, ClaimSummary{
			Scope:       claimed.Scope,
			ID:          claimed.ID,
			Holder:      ref.Cluster,
			TaskID:      ref.TaskID,
			LeaseUntil:  ref.LeaseUntil,
			RenewBefore: renewBefore(ref.LeaseUntil),
			LeaseToken:  ref.SHA,
			SpawnedAt:   ref.SpawnedAt,
		})
	}
	// Sorted by scope then id so the order is a property of the claims, not of
	// git-rest's ref iteration. A caller diffing two listings — which is what a
	// recovery loop does — must not see churn that is only ordering.
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return items[i].Scope < items[j].Scope
		}
		return items[i].ID < items[j].ID
	})
	return items, skipped
}
