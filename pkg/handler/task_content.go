// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/bborbe/vault-api/pkg/rules"
)

// TaskDocument is one task read in full: the identifier the index resolves, the
// title, the whole frontmatter block and the markdown body. The listing serves
// the flat projection; this serves the page, which is what a caller that needs
// to read or rewrite a task's content actually wants.
type TaskDocument struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
}

// contentRequest is the body of PUT /api/v1/tasks/{id}/content. It carries the
// markdown body only: the caller names a task, never a path, and the frontmatter
// is not its to replace through this route.
// Body is a pointer so a request that omits it is refused. With a plain string a
// caller that sent the wrong field name — `frontmatter`, say — would decode to an
// empty body and blank the task's content, which is the one destructive outcome
// this route can have.
type contentRequest struct {
	Body *string `json:"body"`
}

// createRequest is the body of POST /api/v1/tasks. The caller supplies the
// title; the server owns the filename, the directory and the serialisation.
type createRequest struct {
	Title       string         `json:"title"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
}

// typedTaskFields is the frontmatter a PATCH may name. It is an allowlist rather
// than a denylist because the alternative — accept anything the caller sends —
// lets a client write keys this API does not model and cannot validate, which is
// the drift the typed surface exists to stop. Approval is deliberately absent:
// approved_by and approved_at are written by the approve endpoint alone.
var typedTaskFields = map[string]bool{
	"status":       true,
	"phase":        true,
	"assignee":     true,
	"task_type":    true,
	"stage":        true,
	"cluster":      true,
	"flag":         true,
	"goals":        true,
	"priority":     true,
	"category":     true,
	"role":         true,
	"mode":         true,
	"defer_date":   true,
	"due_date":     true,
	"target_date":  true,
	"planned_date": true,
	"start_date":   true,
	"blocked_by":   true,
}

// view renders the document as the typed read.
func (d document) view() TaskDocument {
	return TaskDocument{
		ID:          d.item.ID,
		Title:       d.item.Title,
		Frontmatter: d.frontmatter,
		Body:        taskBody(string(d.raw)),
	}
}

// handleGetTaskDocument serves GET /api/v1/tasks/{id}.
func handleGetTaskDocument(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		document, ok := loadDocument(r.Context(), w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, document.view())
	}
}

// handlePatchTask serves PATCH /api/v1/tasks/{id}: a typed partial update of the
// frontmatter. Status and phase go through the same transition graph the
// dedicated endpoints use, so a PATCH cannot reach a state those refuse.
func handlePatchTask(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request map[string]any
		if !decodeBody(w, r, &request) {
			return
		}
		document, ok := loadDocument(ctx, w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		if code := patchFieldRefusal(w, request, document.item.Path); code != "" {
			return
		}
		if !patchTransitions(w, request, document) {
			return
		}
		for key, value := range request {
			document.set(key, value)
		}
		if !document.persist(ctx, w, deps) {
			return
		}
		writeJSON(w, http.StatusOK, document.view())
	}
}

// fieldRefusal answers the request itself when a named field may not be written
// through this route, and returns "" when every field is allowed. Both write
// routes go through it, so a key one of them refuses cannot be written through
// the other — the asymmetry that made a create a way around the PATCH allowlist.
func fieldRefusal(
	w http.ResponseWriter,
	fields map[string]any,
	allowed map[string]bool,
	path string,
) string {
	for key := range fields {
		if key == "approved_by" || key == "approved_at" {
			writeRefusalError(w, rules.RefuseApprovalRecordWrite(), path)
			return "approval"
		}
		if !allowed[key] {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "unknown_field",
				"field": key,
				"path":  path,
			})
			return "unknown"
		}
	}
	return ""
}

// patchFieldRefusal answers the request itself when a named field may not be
// written through PATCH, and returns "" when every field is allowed.
func patchFieldRefusal(w http.ResponseWriter, request map[string]any, path string) string {
	return fieldRefusal(w, request, typedTaskFields, path)
}

// createFields is the PATCH allowlist plus task_identifier, which only a create
// may name: a PATCH that changed it would break the addressing the API rests on.
var createFields = func() map[string]bool {
	fields := make(map[string]bool, len(typedTaskFields)+1)
	for key := range typedTaskFields {
		fields[key] = true
	}
	fields["task_identifier"] = true
	return fields
}()

// createValues normalises the status and phase a create carries. It deliberately
// does NOT run the transition graph — a create is not a transition — but it does
// refuse an unknown value, because the index excludes a task whose status or
// phase does not parse: such a task answers 201 and then never appears in any
// listing, which is the silent-wrongness this surface exists to remove.
func createValues(w http.ResponseWriter, fields map[string]any, path string) bool {
	if raw, present := fields["status"]; present {
		target, ok := domain.NormalizeTaskStatus(fmt.Sprint(raw))
		if !ok {
			writeRefusalError(w, rules.UnknownStatus(fmt.Sprint(raw)), path)
			return false
		}
		fields["status"] = string(target)
	}
	if raw, present := fields["phase"]; present {
		target, ok := domain.NormalizeTaskPhase(fmt.Sprint(raw))
		if !ok {
			writeRefusalError(w, rules.UnknownPhase(fmt.Sprint(raw)), path)
			return false
		}
		fields["phase"] = string(target)
	}
	return true
}

// patchTransitions validates the status and phase a PATCH carries against the
// transition graph, answering the request itself on a refusal. It reports
// whether the caller should go on to write.
func patchTransitions(w http.ResponseWriter, request map[string]any, document document) bool {
	if raw, present := request["status"]; present {
		target, ok := domain.NormalizeTaskStatus(fmt.Sprint(raw))
		if !ok {
			writeRefusalError(w, rules.UnknownStatus(fmt.Sprint(raw)), document.item.Path)
			return false
		}
		if err := rules.ValidateStatusTransition(
			document.phase(), document.status(), target,
		); err != nil {
			writeRefusalError(w, err, document.item.Path)
			return false
		}
		request["status"] = string(target)
	}
	if raw, present := request["phase"]; present {
		target, ok := domain.NormalizeTaskPhase(fmt.Sprint(raw))
		if !ok {
			writeRefusalError(w, rules.UnknownPhase(fmt.Sprint(raw)), document.item.Path)
			return false
		}
		if err := rules.ValidatePhaseTransition(document.phase(), target); err != nil {
			writeRefusalError(w, err, document.item.Path)
			return false
		}
		request["phase"] = string(target)
	}
	return true
}

// handlePutTaskContent serves PUT /api/v1/tasks/{id}/content. It replaces the
// markdown body and leaves the frontmatter block byte for byte as it was, which
// is why it goes through the raw bytes rather than through the patch path.
func handlePutTaskContent(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request contentRequest
		if !decodeBody(w, r, &request) {
			return
		}
		if request.Body == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "invalid_body",
				"reason": "body is required; send {\"body\": \"...\"} to replace the content",
			})
			return
		}
		document, ok := loadDocument(ctx, w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		replaced := replaceBody(string(document.raw), *request.Body)
		if err := deps.writer.WriteFile(ctx, document.item.Path, []byte(replaced)); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":  "git_rest_failed",
				"reason": err.Error(),
				"path":   document.item.Path,
			})
			return
		}
		document.raw = []byte(replaced)
		writeJSON(w, http.StatusOK, document.view())
	}
}

// replaceBody swaps the markdown body and keeps the frontmatter block untouched.
// A file with no parseable frontmatter is replaced wholesale, because there is
// no block to preserve and guessing one would invent a document.
func replaceBody(raw, body string) string {
	end := frontmatterEnd(raw)
	if end < 0 {
		return body
	}
	// raw[:end] ends with the closing delimiter and its newline, so the body
	// follows directly: taskBody strips exactly one newline there, and adding a
	// second would come back as a leading blank line on every later read.
	return raw[:end] + body
}

// handleCreateTask serves POST /api/v1/tasks. The caller names the task and the
// server owns the filename: a title that cannot be a filename is refused rather
// than sanitised, because a silently rewritten name is a task the caller cannot
// find again.
func handleCreateTask(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request createRequest
		if !decodeBody(w, r, &request) {
			return
		}
		title := strings.TrimSpace(request.Title)
		if !validTaskTitle(title) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid_title",
				"title": title,
			})
			return
		}
		path := filepath.ToSlash(filepath.Join(deps.tasksDir, title+".md"))
		if _, err := os.Stat(filepath.Join(deps.vaultPath, filepath.FromSlash(path))); err == nil {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "task_exists",
				"id":    title,
				"path":  path,
			})
			return
		}
		frontmatter, ok := prepareCreateFrontmatter(w, deps, request.Frontmatter, path)
		if !ok {
			return
		}
		head, err := storage.SerializeFrontmatter(ctx, frontmatter, "")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error":  "render_failed",
				"reason": err.Error(),
			})
			return
		}
		rendered := head + "\n" + request.Body
		if err := deps.writer.WriteFile(ctx, path, []byte(rendered)); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":  "git_rest_failed",
				"reason": err.Error(),
				"path":   path,
			})
			return
		}
		writeJSON(w, http.StatusCreated, TaskDocument{
			ID:          fmt.Sprint(frontmatter["task_identifier"]),
			Title:       title,
			Frontmatter: frontmatter,
			Body:        request.Body,
		})
	}
}

// prepareCreateFrontmatter validates the caller's frontmatter and returns it
// ready to serialise, answering the request itself when any of it is refused.
func prepareCreateFrontmatter(
	w http.ResponseWriter,
	deps writeDeps,
	fields map[string]any,
	path string,
) (map[string]any, bool) {
	frontmatter := fields
	if frontmatter == nil {
		frontmatter = map[string]any{}
	}
	if code := fieldRefusal(w, frontmatter, createFields, path); code != "" {
		return nil, false
	}
	if !createValues(w, frontmatter, path) {
		return nil, false
	}
	// A caller-supplied identifier is accepted only when nothing answers to it. Two
	// pages sharing one identifier make one of them unaddressable: the index
	// resolves whichever it saw last, so the other silently disappears.
	if supplied, present := frontmatter["task_identifier"]; present {
		identifier := fmt.Sprint(supplied)
		if _, found, _ := deps.index.GetTask(identifier); found {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "task_exists",
				"id":    identifier,
				"path":  path,
			})
			return nil, false
		}
		return frontmatter, true
	}
	// A task without an identifier is addressable only by its filename, which
	// changes the moment someone renames the page. The server mints one so the
	// created task is addressable by identifier from the first request.
	frontmatter["task_identifier"] = uuid.NewString()
	return frontmatter, true
}

// validTaskTitle reports whether a title can be a vault filename. The vault uses
// the page title as the filename, so a separator or a traversal segment would
// write outside the tasks directory.
func validTaskTitle(title string) bool {
	if title == "" || title == "." || title == ".." {
		return false
	}
	return !strings.ContainsAny(title, "/\\\x00")
}
