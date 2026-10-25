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
	"time"

	libhttp "github.com/bborbe/http"
	liblog "github.com/bborbe/log"
	libtime "github.com/bborbe/time"
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
	gatewaySecret string,
	vaultPath string,
	vaultName string,
	writer gitrest.Client,
	clock libtime.CurrentDateTime,
	taskPublisher publisher.TaskPublisher,
	refs gitrest.RefClient,
) http.Handler {
	deps := writeDeps{
		index:         index,
		vaultPath:     vaultPath,
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
	api.Path("/tasks").HandlerFunc(handleListTasks(index))
	api.Path("/tasks/{id}").HandlerFunc(handleGetTask(index))
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

	// Claims are addressed by the object they are held on — a task identifier or
	// a repository — never by a vault path. The transport under them is git-rest's
	// claim refs; a caller never sees that vocabulary.
	claimDeps := ClaimDeps{Index: index, Refs: refs, Clock: clock}
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

	return router
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

func handleListTasks(index *vaultapi.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		items, warnings, age := index.QueryTasks(vaultapi.TaskFilter{
			Assignee: query.Get("assignee"),
			Status:   query.Get("status"),
			Phase:    query.Get("phase"),
			Goal:     query.Get("goal"),
			Cluster:  query.Get("cluster"),
		})
		writeJSON(w, http.StatusOK, vaultapi.ListResponse[vaultapi.TaskItem]{
			Items:           items,
			Total:           len(items),
			Warnings:        warnings,
			IndexAgeSeconds: age,
		})
	}
}

func handleGetTask(index *vaultapi.Index) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		item, found, invalid := index.GetTask(id)
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
