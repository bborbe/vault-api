// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	lib "github.com/bborbe/agent"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"
	"github.com/gorilla/mux"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/gitrest"
	"github.com/bborbe/vault-api/pkg/publisher"
	"github.com/bborbe/vault-api/pkg/rules"
)

// maxWriteBodyBytes caps a write request body. The bodies are a phase name, a
// status name and an approver, so anything approaching this is a mistake.
const maxWriteBodyBytes = 64 * 1024

// publishTimeout bounds the change-event publish on the request path. The file is
// already in git by then, so waiting on a broker can only delay a response whose
// outcome is decided.
const publishTimeout = 5 * time.Second

// writeDeps is everything the write endpoints share: the index resolves an id
// to a path, the vault path supplies the original bytes, the writer sends the
// rendered file to git-rest, the clock stamps an approval, and the publisher
// turns a landed write into a change event on the task topic.
type writeDeps struct {
	index     *vaultapi.Index
	vaultPath string
	vault     string
	writer    gitrest.Client
	clock     libtime.CurrentDateTime
	publisher publisher.TaskPublisher
}

// phaseRequest is the body of PATCH /api/v1/tasks/{id}/phase. ApprovedBy is a
// pointer so a body that carries the key at all is refused, even when blank.
type phaseRequest struct {
	Phase      string  `json:"phase"`
	ApprovedBy *string `json:"approved_by"`
}

// statusRequest is the body of PATCH /api/v1/tasks/{id}/status.
type statusRequest struct {
	Status     string  `json:"status"`
	ApprovedBy *string `json:"approved_by"`
}

// approveRequest is the body of POST /api/v1/tasks/{id}/approve.
type approveRequest struct {
	ApprovedBy string `json:"approved_by"`
}

// writeResponse is what every write endpoint answers on success. The commit SHA
// is deliberately absent: git-rest returns no SHA, and SC3 reads it from git log.
type writeResponse struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Status string `json:"status"`
	Phase  string `json:"phase"`
}

// handlePatchPhase moves a task to another phase.
func handlePatchPhase(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request phaseRequest
		if !decodeBody(w, r, &request) {
			return
		}
		if request.ApprovedBy != nil {
			writeRefusalError(w, rules.RefuseApprovalRecordWrite(), mux.Vars(r)["id"])
			return
		}
		target, ok := domain.NormalizeTaskPhase(request.Phase)
		if !ok {
			writeRefusalError(w, rules.UnknownPhase(request.Phase), mux.Vars(r)["id"])
			return
		}
		document, ok := loadDocument(ctx, w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		current := document.phase()
		if err := rules.ValidatePhaseTransition(current, target); err != nil {
			writeRefusalError(w, err, document.item.Path)
			return
		}
		document.frontmatter["phase"] = string(target)
		document.write(ctx, w, deps, document.item.Status, string(target))
	}
}

// handlePatchStatus moves a task to another status.
func handlePatchStatus(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request statusRequest
		if !decodeBody(w, r, &request) {
			return
		}
		if request.ApprovedBy != nil {
			writeRefusalError(w, rules.RefuseApprovalRecordWrite(), mux.Vars(r)["id"])
			return
		}
		target, ok := domain.NormalizeTaskStatus(request.Status)
		if !ok {
			writeRefusalError(w, rules.UnknownStatus(request.Status), mux.Vars(r)["id"])
			return
		}
		document, ok := loadDocument(ctx, w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		current := document.status()
		if err := rules.ValidateStatusTransition(document.phase(), current, target); err != nil {
			writeRefusalError(w, err, document.item.Path)
			return
		}
		document.frontmatter["status"] = string(target)
		document.write(ctx, w, deps, string(target), string(document.phase()))
	}
}

// handleApprove records an approval: it moves a task out of the approval inbox
// and writes the record, the owner and the phase in one file write. It never
// touches status — vault-cli's approve does not write status either.
func handleApprove(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request approveRequest
		if !decodeBody(w, r, &request) {
			return
		}
		document, ok := loadDocument(ctx, w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		now := deps.clock.Now().Time()
		hasRecord := document.frontmatter["approved_by"] != nil ||
			document.frontmatter["approved_at"] != nil
		if err := rules.ValidateApprove(
			request.ApprovedBy,
			document.phase(),
			hasRecord,
			now,
		); err != nil {
			writeRefusalError(w, err, document.item.Path)
			return
		}
		document.frontmatter["approved_by"] = request.ApprovedBy
		document.frontmatter["approved_at"] = now
		document.frontmatter["phase"] = string(domain.TaskPhasePlanning)
		// The owner is fixed at approval time and only when it is missing: an
		// existing assignee is never overwritten, and the approver is the only
		// identity this API knows, so it stands in for vault-cli's currentUser.
		if document.item.Assignee == "" {
			document.frontmatter["assignee"] = request.ApprovedBy
		}
		document.write(
			ctx, w, deps,
			document.item.Status, string(domain.TaskPhasePlanning),
		)
	}
}

// document is one task file loaded for writing: the indexed projection, the raw
// bytes it was parsed from, and the frontmatter map that will be re-rendered.
type document struct {
	item        vaultapi.TaskItem
	raw         []byte
	frontmatter map[string]any
}

// loadDocument resolves the id through the index, reads the file, and parses its
// frontmatter. It writes the failure response itself and reports whether the
// caller should continue.
func loadDocument(
	ctx context.Context,
	w http.ResponseWriter,
	deps writeDeps,
	id string,
) (document, bool) {
	item, found, invalid := deps.index.GetTask(id)
	if !found {
		if invalid != "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error":  "invalid_frontmatter",
				"reason": invalid,
				"path":   id,
			})
			return document{}, false
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return document{}, false
	}
	fullPath := filepath.Join(deps.vaultPath, filepath.FromSlash(item.Path))
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  "read_failed",
			"reason": err.Error(),
			"path":   item.Path,
		})
		return document{}, false
	}
	frontmatter, err := storage.ParseFrontmatterMap(ctx, raw)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":  "invalid_frontmatter",
			"reason": err.Error(),
			"path":   item.Path,
		})
		return document{}, false
	}
	return document{item: item, raw: raw, frontmatter: frontmatter}, true
}

// phase returns the task's current phase, normalized. An absent or unparseable
// phase reads as empty, which every graph treats as not past todo.
func (d document) phase() domain.TaskPhase {
	raw, ok := d.frontmatter["phase"].(string)
	if !ok {
		return ""
	}
	normalized, ok := domain.NormalizeTaskPhase(raw)
	if !ok {
		return ""
	}
	return normalized
}

// status returns the task's current status, normalized.
func (d document) status() domain.TaskStatus {
	raw, ok := d.frontmatter["status"].(string)
	if !ok {
		return ""
	}
	normalized, ok := domain.NormalizeTaskStatus(raw)
	if !ok {
		return ""
	}
	return normalized
}

// write renders the frontmatter back over the original bytes, hands the whole
// file to git-rest, and answers with the values the caller asked for.
func (d document) write(
	ctx context.Context,
	w http.ResponseWriter,
	deps writeDeps,
	status, phase string,
) {
	rendered, err := storage.SerializeFrontmatter(ctx, d.frontmatter, string(d.raw))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  "render_failed",
			"reason": err.Error(),
			"path":   d.item.Path,
		})
		return
	}
	if err := deps.writer.WriteFile(ctx, d.item.Path, []byte(rendered)); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":  "git_rest_failed",
			"reason": err.Error(),
			"path":   d.item.Path,
		})
		return
	}
	// The file has landed in git, so the response is already decided: a publish
	// failure must not turn a write that happened into an error the client retries,
	// because that retry would be refused as an already-applied transition. Log it
	// and answer 200 — the pull path republishes the same change from the commit
	// diff, so the stream converges without the caller.
	//
	// Bounded, because the deferred handle only covers startup: a broker that goes
	// unreachable later must not hold this request open past a response that is
	// already decided.
	publishCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	err = deps.publisher.PublishChanged(publishCtx, d.task(deps.vault))
	cancel()
	if err != nil {
		glog.Errorf("publish change event for task %s failed: %v", d.item.Path, err)
	}
	writeJSON(w, http.StatusOK, writeResponse{
		ID:     d.item.ID,
		Path:   d.item.Path,
		Status: status,
		Phase:  phase,
	})
}

// task renders the document as a change-event payload. The identifier is the
// index's id, which is the task's own task_identifier and falls back to a
// filename slug only when the file carries none — the same value GET
// /api/v1/tasks serves, so a consumer keys on one id across both paths.
func (d document) task(vault string) lib.Task {
	return lib.Task{
		TaskIdentifier: lib.TaskIdentifier(d.item.ID),
		Vault:          vault,
		Frontmatter:    lib.TaskFrontmatter(d.frontmatter),
		Content:        lib.TaskContent(taskBody(string(d.raw))),
	}
}

// taskBody returns the markdown body after the frontmatter's closing delimiter.
// A write re-renders only the frontmatter, so the body is taken from the bytes on
// disk rather than from the rendered string.
func taskBody(raw string) string {
	rest, found := strings.CutPrefix(raw, "---")
	if !found {
		return ""
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return ""
	}
	return strings.TrimPrefix(rest[end+len("---")+1:], "\n")
}

// decodeBody reads a JSON body, answering 400 when it is not one.
func decodeBody(
	w http.ResponseWriter,
	r *http.Request,
	target any,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxWriteBodyBytes))
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":  "invalid_body",
			"reason": err.Error(),
		})
		return false
	}
	return true
}

// refusalStatus maps a refusal code to the status the caller should see.
func refusalStatus(code string) int {
	switch code {
	case rules.CodeApprovalIsAVerb, rules.CodeBlankApprover:
		return http.StatusBadRequest
	case rules.CodeUnknownPhase, rules.CodeUnknownStatus:
		return http.StatusUnprocessableEntity
	case rules.CodeInvalidClock:
		return http.StatusInternalServerError
	}
	return http.StatusConflict
}

// writeRefusalError renders a *rules.RefusalError as the endpoint's error body. A refusal
// that is not one of this package's typed errors is reported as a bad request
// rather than silently answered 200.
func writeRefusalError(w http.ResponseWriter, err error, path string) {
	var refusal *rules.RefusalError
	if !errors.As(err, &refusal) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":  "refused",
			"reason": err.Error(),
			"path":   path,
		})
		return
	}
	body := map[string]any{
		"error":  refusal.Code,
		"reason": refusal.Detail,
		"path":   path,
	}
	if refusal.From != "" {
		body["from"] = refusal.From
	}
	if refusal.To != "" {
		body["to"] = refusal.To
	}
	if len(refusal.Allowed) > 0 {
		body["allowed"] = refusal.Allowed
	}
	writeJSON(w, refusalStatus(refusal.Code), body)
}
