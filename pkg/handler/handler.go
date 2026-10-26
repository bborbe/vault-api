// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package handler owns the HTTP surface: the five canonical admin endpoints and
// the typed /api/v1 read endpoints.
package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	libhttp "github.com/bborbe/http"
	liblog "github.com/bborbe/log"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/gitrest"
	"github.com/bborbe/vault-api/pkg/publisher"
)

// NewHandler builds the service router. When gatewaySecret is non-empty every
// /api/v1/* request must carry it in X-Gateway-Secret; the admin endpoints stay
// unauthenticated, matching the fleet's contract.
//
// The write endpoints need five things the read endpoints do not: vaultPath, to
// read the original bytes before re-rendering them; vaultName, which stamps the
// change event so a consumer sharing one topic across vaults can tell its own
// tasks apart; writer, to hand the rendered file to git-rest; clock, so an
// approval's instant is injected rather than read from the wall clock; and
// taskPublisher, which turns a landed write into a change event.
func NewHandler(
	ctx context.Context,
	index *vaultapi.Index,
	store storage.PageStorage,
	gatewaySecret string,
	vaultPath string,
	tasksDir string,
	vaultName string,
	writer gitrest.Client,
	clock libtime.CurrentDateTime,
	taskPublisher publisher.TaskPublisher,
	refs gitrest.RefClient,
) http.Handler {
	deps := writeDeps{
		index:         index,
		store:         store,
		vaultPath:     vaultPath,
		tasksDir:      tasksDir,
		vault:         vaultName,
		writer:        writer,
		clock:         clock,
		publisher:     taskPublisher,
		gatewaySecret: gatewaySecret,
	}

	router := mux.NewRouter()
	router.Path("/healthz").Handler(libhttp.NewPrintHandler("OK"))
	router.Path("/readiness").HandlerFunc(handleReadiness(index))
	router.Path("/metrics").Handler(promhttp.Handler())
	router.Path("/setloglevel/{level}").
		Handler(liblog.NewSetLoglevelHandler(ctx, liblog.NewLogLevelSetter(2, 5*time.Minute)))
	router.Path("/gc").Handler(libhttp.NewGarbageCollectorHandler())

	api := router.PathPrefix("/api/v1").Subrouter()
	if gatewaySecret != "" {
		api.Use(gatewaySecretMiddleware(gatewaySecret))
	}
	api.Path("/tasks").Methods(http.MethodGet).HandlerFunc(handleListTasks(index))
	api.Path("/tasks").Methods(http.MethodPost).HandlerFunc(handleCreateTask(deps))
	api.Path("/tasks/{id}").Methods(http.MethodGet).HandlerFunc(handleGetTaskDocument(deps))
	api.Path("/tasks/{id}").Methods(http.MethodPatch).HandlerFunc(handlePatchTask(deps))
	api.Path("/tasks/{id}/content").
		Methods(http.MethodPut).
		HandlerFunc(handlePutTaskContent(deps))
	api.Path("/tasks/{id}/phase").
		Methods(http.MethodPatch).
		HandlerFunc(handlePatchPhase(deps))
	api.Path("/tasks/{id}/status").
		Methods(http.MethodPatch).
		HandlerFunc(handlePatchStatus(deps))
	api.Path("/tasks/{id}/approve").
		Methods(http.MethodPost).
		HandlerFunc(handleApprove(deps))
	api.Path("/merge").
		Methods(http.MethodPost).
		HandlerFunc(handleMerge(deps))
	api.Path("/goals").HandlerFunc(handleListGoals(index))
	api.Path("/goals/{id}").HandlerFunc(handleGetGoal(index))

	registerClaimRoutes(api, ClaimDeps{Refs: refs, Clock: clock}, index)

	return router
}

// registerClaimRoutes wires the claim surface: the collection read plus the
// identifier-addressed acquire, renew, release and read for each scope.
//
// Claims are addressed by the object they are held on — a task identifier or a
// repository — never by a vault path. The transport under them is git-rest's
// claim refs; a caller never sees that vocabulary.
//
// The collection read is registered first because it is what tells a caller
// which of the identifier-addressed routes it needs to call at all.
func registerClaimRoutes(api *mux.Router, claimDeps ClaimDeps, index *vaultapi.Index) {
	api.Path("/claims").Methods(http.MethodGet).HandlerFunc(ListClaimsHandler(claimDeps))

	taskScope := TaskClaimScope(index)
	api.Path("/tasks/{id}/claim").Methods(http.MethodPost).
		HandlerFunc(AcquireClaimHandler(claimDeps, taskScope))
	api.Path("/tasks/{id}/claim/renew").Methods(http.MethodPost).
		HandlerFunc(RenewClaimHandler(claimDeps, taskScope))
	api.Path("/tasks/{id}/claim").Methods(http.MethodDelete).
		HandlerFunc(ReleaseClaimHandler(claimDeps, taskScope))
	api.Path("/tasks/{id}/claim").Methods(http.MethodGet).
		HandlerFunc(ReadClaimHandler(claimDeps, taskScope))

	repoScope := RepoClaimScope()
	api.Path("/repos/{owner}/{repo}/claim").Methods(http.MethodPost).
		HandlerFunc(AcquireClaimHandler(claimDeps, repoScope))
	api.Path("/repos/{owner}/{repo}/claim/renew").Methods(http.MethodPost).
		HandlerFunc(RenewClaimHandler(claimDeps, repoScope))
	api.Path("/repos/{owner}/{repo}/claim").Methods(http.MethodDelete).
		HandlerFunc(ReleaseClaimHandler(claimDeps, repoScope))
	api.Path("/repos/{owner}/{repo}/claim").Methods(http.MethodGet).
		HandlerFunc(ReadClaimHandler(claimDeps, repoScope))

	// A per-PR claim narrows the lock from the repository to one pull request,
	// so two reviews of the same repository can run at once. The executor holds
	// this claim alongside the repository one and recovers the pair by task id.
	prScope := PRClaimScope()
	api.Path("/repos/{owner}/{repo}/pulls/{number}/claim").Methods(http.MethodPost).
		HandlerFunc(AcquireClaimHandler(claimDeps, prScope))
	api.Path("/repos/{owner}/{repo}/pulls/{number}/claim/renew").Methods(http.MethodPost).
		HandlerFunc(RenewClaimHandler(claimDeps, prScope))
	api.Path("/repos/{owner}/{repo}/pulls/{number}/claim").Methods(http.MethodDelete).
		HandlerFunc(ReleaseClaimHandler(claimDeps, prScope))
	api.Path("/repos/{owner}/{repo}/pulls/{number}/claim").Methods(http.MethodGet).
		HandlerFunc(ReadClaimHandler(claimDeps, prScope))
}

func handleReadiness(index *vaultapi.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		readiness := index.Readiness()
		status := http.StatusOK
		if !readiness.Ready {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, readiness)
	}
}

// taskListFilters is every query parameter GET /api/v1/tasks serves. A parameter
// outside this set is refused rather than ignored: the handler used to drop what
// it did not recognise and return every task, so a caller whose filter was
// misspelled or unsupported read the whole vault as its match set — the exact
// silent-wrongness this surface exists to remove.
var taskListFilters = map[string]bool{
	"assignee":      true,
	"status":        true,
	"phase":         true,
	"goal":          true,
	"cluster":       true,
	"task_type":     true,
	"short_id":      true,
	"updated_since": true,
}

func handleListTasks(index *vaultapi.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		for name := range query {
			if !taskListFilters[name] {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error":  "unknown_filter",
					"filter": name,
				})
				return
			}
			// A declared filter sent with an empty value is refused too. query.Get
			// cannot tell `?task_type=` from an absent parameter, so treating it as
			// unset would return the whole vault for a caller who meant to narrow it
			// — the same silent widening the unknown_filter branch prevents.
			if query.Get(name) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error":  "invalid_filter",
					"filter": name,
					"reason": "a filter must carry a value; omit the parameter to leave it unset",
				})
				return
			}
		}
		// A task_type that survives the empty check above but yields no values —
		// `?task_type=,` or `?task_type=,,` — would otherwise apply no filter at all
		// and return the whole vault, which is the silent widening the empty check
		// exists to prevent. Splitting happens here so the refusal can name it.
		taskTypes := taskTypeValues(query)
		if _, present := query["task_type"]; present && len(taskTypes) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "invalid_filter",
				"filter": "task_type",
				"reason": "a filter must name at least one type; omit the parameter to leave it unset",
			})
			return
		}
		updatedSince, ok := parseUpdatedSince(w, query.Get("updated_since"))
		if !ok {
			return
		}
		items, warnings, age := index.QueryTasks(vaultapi.TaskFilter{
			Assignee:     query.Get("assignee"),
			Status:       query.Get("status"),
			Phase:        query.Get("phase"),
			Goal:         query.Get("goal"),
			Cluster:      query.Get("cluster"),
			TaskTypes:    taskTypes,
			ShortID:      query.Get("short_id"),
			UpdatedSince: updatedSince,
		})
		writeJSON(w, http.StatusOK, vaultapi.ListResponse[vaultapi.TaskItem]{
			Items:           items,
			Total:           len(items),
			Warnings:        warnings,
			IndexAgeSeconds: age,
		})
	}
}

// taskTypeValues collects every task_type value a query carries, so a caller can
// name several in one request — repeated (?task_type=a&task_type=b) or
// comma-separated (?task_type=a,b). Empty segments are dropped here; a parameter
// carrying no value at all is refused before this runs.
func taskTypeValues(query url.Values) []string {
	var values []string
	for _, raw := range query["task_type"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				values = append(values, part)
			}
		}
	}
	return values
}

// parseUpdatedSince reads an RFC3339 bound for the updated_since filter. An
// unparseable value is refused rather than dropped, because a filter that
// silently disappears returns every task and reads as a successful query.
func parseUpdatedSince(w http.ResponseWriter, raw string) (*time.Time, bool) {
	if raw == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":  "invalid_filter",
			"filter": "updated_since",
			"reason": "expected an RFC3339 timestamp",
		})
		return nil, false
	}
	return &parsed, true
}

func handleListGoals(index *vaultapi.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		items, warnings, age := index.QueryGoals()
		writeJSON(w, http.StatusOK, vaultapi.ListResponse[vaultapi.GoalItem]{
			Items:           items,
			Total:           len(items),
			Warnings:        warnings,
			IndexAgeSeconds: age,
		})
	}
}

func handleGetGoal(index *vaultapi.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		item, found, invalid := index.GetGoal(id)
		if found {
			writeJSON(w, http.StatusOK, item)
			return
		}
		if invalid != "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":  "invalid_frontmatter",
				"reason": invalid,
				"path":   id,
			})
			return
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	}
}

func gatewaySecretMiddleware(secret string) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := r.Header.Get("X-Gateway-Secret")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
