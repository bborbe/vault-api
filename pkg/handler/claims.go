// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"time"

	libtime "github.com/bborbe/time"
	"github.com/gorilla/mux"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/gitrest"
)

// DefaultLeaseSeconds is the lease a claim gets when the caller names none. It
// is the 5-minute lease settled on 2026-10-21 and carried over from the design's
// § 10, not a value chosen here.
const DefaultLeaseSeconds = 300

// RenewLeadSeconds is how long before expiry a holder should renew. The design
// fixes renewal at 60 s, so the response states the instant rather than leaving
// the caller to re-derive it.
const RenewLeadSeconds = 60

// claimBodyLimit bounds the accepted claim request body. A claim body is three
// short strings; anything larger is a mistake, not a claim.
const claimBodyLimit = 64 * 1024

// ClaimDeps is what the claim handlers need: the index to resolve an identifier,
// the ref client that carries the claim to git-rest, and an injected clock so a
// lease instant is computed from a supplied time rather than the wall clock.
type ClaimDeps struct {
	Index *vaultapi.Index
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
func repoClaimScope(owner string, repo string) ClaimScope {
	return ClaimScope{
		Scope: "repo",
		ID:    owner + "/" + repo,
		Key:   "claims/repo/" + owner + "-" + repo,
	}
}

// TaskClaimScope resolves the {id} path variable to a task claim scope.
func TaskClaimScope(index *vaultapi.Index) func(*http.Request) (ClaimScope, bool) {
	return func(r *http.Request) (ClaimScope, bool) {
		return taskClaimScope(index, mux.Vars(r)["id"])
	}
}

// RepoClaimScope resolves the {owner} and {repo} path variables to a repo claim
// scope. It cannot fail: a repository needs no index entry to be claimed.
func RepoClaimScope() func(*http.Request) (ClaimScope, bool) {
	return func(r *http.Request) (ClaimScope, bool) {
		vars := mux.Vars(r)
		owner, repo := vars["owner"], vars["repo"]
		if owner == "" || repo == "" {
			return ClaimScope{}, false
		}
		return repoClaimScope(owner, repo), true
	}
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

		var request claimRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, claimBodyLimit)).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
			return
		}
		if request.Holder == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_holder"})
			return
		}
		if request.TTLSeconds < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_ttl"})
			return
		}
		if renew && request.LeaseToken == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_lease_token"})
			return
		}

		// A repository has no task of its own, so the claiming run supplies one.
		// A task claim always uses the addressed task, never a caller-supplied id.
		transportTaskID := claimed.ID
		if claimed.Scope == "repo" {
			transportTaskID = request.TaskID
			if transportTaskID == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing_task_id"})
				return
			}
		}

		ttl := request.TTLSeconds
		if ttl == 0 {
			ttl = DefaultLeaseSeconds
		}
		now := deps.Clock.Now().Time().UTC()
		leaseUntil := now.Add(time.Duration(ttl) * time.Second)

		ref, err := deps.Refs.PutClaimRef(r.Context(), claimed.Key, gitrest.ClaimRefRequest{
			ExpectedSHA: request.LeaseToken,
			Cluster:     request.Holder,
			TaskID:      transportTaskID,
			LeaseUntil:  leaseUntil.Format(time.RFC3339),
		})
		if err != nil {
			writeClaimError(w, err, renew)
			return
		}

		writeJSON(w, http.StatusCreated, claimResponse{
			Scope:       claimed.Scope,
			ID:          claimed.ID,
			Holder:      request.Holder,
			LeaseUntil:  leaseUntil.Format(time.RFC3339),
			RenewBefore: leaseUntil.Add(-RenewLeadSeconds * time.Second).Format(time.RFC3339),
			LeaseToken:  ref.SHA,
		})
	}
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

		var request claimRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, claimBodyLimit)).Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
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
		for _, ref := range refs {
			if ref.Ref != "refs/"+claimed.Key {
				continue
			}
			writeJSON(w, http.StatusOK, claimResponse{
				Scope:      claimed.Scope,
				ID:         claimed.ID,
				Holder:     ref.Cluster,
				LeaseUntil: ref.LeaseUntil,
				LeaseToken: ref.SHA,
			})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "claim_free"})
	}
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
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "claim_write_failed"})
}
