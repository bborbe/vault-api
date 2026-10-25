// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	index         *vaultapi.Index
	store         storage.PageStorage
	vaultPath     string
	tasksDir      string
	vault         string
	writer        gitrest.Client
	clock         libtime.CurrentDateTime
	publisher     publisher.TaskPublisher
	gatewaySecret string
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
		document.set("phase", string(target))
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
		document.set("status", string(target))
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
		document.set("approved_by", request.ApprovedBy)
		document.set("approved_at", now)
		document.set("phase", string(domain.TaskPhasePlanning))
		// The owner is fixed at approval time and only when it is missing: an
		// existing assignee is never overwritten, and the approver is the only
		// identity this API knows, so it stands in for vault-cli's currentUser.
		if document.item.Assignee == "" {
			document.set("assignee", request.ApprovedBy)
		}
		document.write(
			ctx, w, deps,
			document.item.Status, string(domain.TaskPhasePlanning),
		)
	}
}

// document is one task file loaded for writing: the indexed projection, the raw
// bytes it was parsed from, the frontmatter map reads go through, and the subset
// of keys this handler changed. Only `changed` is written back, so every other
// byte of the frontmatter block survives the write.
type document struct {
	item        vaultapi.TaskItem
	raw         []byte
	frontmatter map[string]any
	changed     map[string]any
}

// set records a frontmatter change. Reads keep going through frontmatter; the
// write path patches only the keys recorded here, so a phase transition does not
// re-render the keys it never touched.
func (d *document) set(key string, value any) {
	d.frontmatter[key] = value
	d.changed[key] = value
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
	// The read goes through the page storage rather than the filesystem, so it
	// increments vault_api_file_reads_total{kind="page_read"}. That counter is the
	// positive control a verification run discriminates on: a handler that read
	// the file directly would leave the criterion nothing to observe.
	page, err := deps.store.ReadPage(ctx, deps.vaultPath, deps.tasksDir, item.Title)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  "read_failed",
			"reason": err.Error(),
			"path":   item.Path,
		})
		return document{}, false
	}
	raw := []byte(string(page.Content))
	frontmatter, err := storage.ParseFrontmatterMap(ctx, raw)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error":  "invalid_frontmatter",
			"reason": err.Error(),
			"path":   item.Path,
		})
		return document{}, false
	}
	return document{
		item:        item,
		raw:         raw,
		frontmatter: frontmatter,
		changed:     map[string]any{},
	}, true
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

// write patches the changed frontmatter keys into the original bytes, hands the
// whole file to git-rest, and answers with the values the caller asked for.
func (d document) write(
	ctx context.Context,
	w http.ResponseWriter,
	deps writeDeps,
	status, phase string,
) {
	if !d.persist(ctx, w, deps) {
		return
	}
	writeJSON(w, http.StatusOK, writeResponse{
		ID:     d.item.ID,
		Path:   d.item.Path,
		Status: status,
		Phase:  phase,
	})
}

// persist renders the changed keys into the original bytes, hands the file to
// git-rest and publishes the change event. It answers its own failures and
// reports whether the caller should go on to write a response.
func (d document) persist(ctx context.Context, w http.ResponseWriter, deps writeDeps) bool {
	rendered, err := storage.PatchFrontmatter(ctx, d.changed, string(d.raw))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error":  "render_failed",
			"reason": err.Error(),
			"path":   d.item.Path,
		})
		return false
	}
	if err := deps.writer.WriteFile(ctx, d.item.Path, []byte(rendered)); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":  "git_rest_failed",
			"reason": err.Error(),
			"path":   d.item.Path,
		})
		return false
	}
	// The file has landed in git, so the response is already decided: a publish
	// failure must not turn a write that happened into an error the client retries,
	// because that retry would be refused as an already-applied transition.
	//
	// The event is therefore best-effort here, but a failure is not silent: the pull
	// path diffs the commit this write produced and publishes the same change, so a
	// broker outage or a timeout delays the event rather than dropping it. Publishing
	// under WithoutCancel keeps a client hanging up from being one of those causes —
	// only the timeout can end it.
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publishTimeout)
	err = deps.publisher.PublishChanged(publishCtx, d.task(deps.vault))
	cancel()
	if err != nil {
		// ErrNotReady is the startup window, not a fault: the producer is still being
		// built. It is still a lost event — nothing retries it — but logging it at
		// Error would put an unbounded ERROR stream on every write for the whole of a
		// broker outage, which is the noise the discard publisher exists to avoid for
		// the disabled case.
		if errors.Is(err, publisher.ErrNotReady) {
			glog.Warningf(
				"task publisher not ready, change event for %s is lost: %v",
				d.item.Path,
				err,
			)
		} else {
			glog.Errorf(
				"publish change event for task %s failed and the event is lost: %v",
				d.item.Path,
				err,
			)
		}
	}
	return true
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

// frontmatterEnd returns the offset just past the frontmatter block's closing
// delimiter, or -1 when the content carries no parseable block.
//
// The delimiter is a line that is exactly "---". Matching a bare "\n---" prefix
// instead would also match a block scalar whose first line begins with dashes —
// YAML permits one — and then cut the real frontmatter in half, moving the rest
// of it into the body. That is the one way the read and the body write can
// silently corrupt a task.
func frontmatterEnd(raw string) int {
	lines := strings.SplitAfter(raw, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r\n") != "---" {
		return -1
	}
	offset := len(lines[0])
	for _, line := range lines[1:] {
		offset += len(line)
		if strings.TrimRight(line, "\r\n") == "---" {
			return offset
		}
	}
	return -1
}

// taskBody returns the markdown body after the frontmatter's closing delimiter.
// A write re-renders only the frontmatter, so the body is taken from the bytes on
// disk rather than from the rendered string.
func taskBody(raw string) string {
	end := frontmatterEnd(raw)
	if end < 0 {
		return ""
	}
	// Both line endings, because a vault checked out on a machine with autocrlf can
	// carry CRLF and the event body must not start with a stray \r.
	return strings.TrimPrefix(strings.TrimPrefix(raw[end:], "\r"), "\n")
}

// decodeBody reads a JSON body capped at the write endpoints' limit, answering
// 400 when it is not one.
func decodeBody(
	w http.ResponseWriter,
	r *http.Request,
	target any,
) bool {
	return decodeBodyLimited(w, r, target, maxWriteBodyBytes)
}

// decodeBodyLimited reads a JSON body capped at limit bytes. The merge endpoint
// carries three complete file contents, so it cannot share the write endpoints'
// cap, which was chosen for a phase name, a status name and an approver.
func decodeBodyLimited(
	w http.ResponseWriter,
	r *http.Request,
	target any,
	limit int64,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
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
