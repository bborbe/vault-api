// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/google/uuid"
	"github.com/gorilla/mux"

	vaultapi "github.com/bborbe/vault-api/pkg"
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
	// Reopened marks a create that replaced a terminal occupant rather than
	// writing a free path. Absent on every ordinary create, so a caller that
	// ignores it is unaffected and one that logs it can record the reopen.
	Reopened bool `json:"reopened,omitempty"`
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
//
// Reopen is opt-in and names the only case it covers: "terminal". Absent or any
// other value keeps the route's original behaviour, where any occupant is a
// conflict, so no existing caller changes. See reopenTerminal.
type createRequest struct {
	Title       string         `json:"title"`
	Frontmatter map[string]any `json:"frontmatter"`
	Body        string         `json:"body"`
	Reopen      string         `json:"reopen,omitempty"`
}

// reopenTerminal is the only accepted value of createRequest.Reopen: replace a
// task whose status is terminal, and refuse every other occupant.
const reopenTerminal = "terminal"

// typedTaskFields is the subset of frontmatter this API validates and reasons
// about — the keys whose values it checks against a transition graph, a
// normalised vocabulary or a rule. It is not the whole of what a write may name:
// the agent pipeline's own keys are accepted too, through agentPipelineFields
// below, and the two are kept apart deliberately (see that map's comment).
//
// It is an allowlist rather than a denylist because the alternative — accept
// anything the caller sends — lets a client write keys this API does not model
// and cannot validate, which is the drift the typed surface exists to stop.
// Approval is deliberately absent: approved_by and approved_at are written by
// the approve endpoint alone.
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

// agentPipelineFields is the frontmatter the agent pipeline writes through this
// API. It is a separate set from typedTaskFields on purpose: a key in
// typedTaskFields is one this API validates and reasons about (status, phase,
// assignee), while a key here is one it stores and carries without interpreting
// it. Merging the two would hide that difference from the next reader and would
// invite treating trigger_count as though this API knew what it counts.
//
// The set is closed, so the drift control typedTaskFields exists for still
// holds: a key in neither map is refused with 400 unknown_field exactly as
// before. Adding a pipeline key is a one-line change here plus a row in the
// typed-API design, and changes nothing on the pipeline side.
//
// Every entry is measured rather than guessed — each is written today by
// agent-task-executor or agent-task-controller on a path that reaches PatchTask.
var agentPipelineFields = map[string]bool{
	"trigger_scope":      true, // PublishSetTriggerScope
	"trigger_count":      true, // PublishSetTriggerScope, IncrementFrontmatterCommand
	"max_triggers":       true, // the operator's cap definition, armed on the crash class
	"current_job":        true, // PublishSpawnNotification, cleared by PublishClearCurrentJob
	"job_started_at":     true, // PublishSpawnNotification
	"spawn_notification": true, // PublishSpawnNotification
	"queued_at":          true, // PublishQueueEntry
	"aborted_reason":     true, // PublishAbort
	"gate_successor":     true, // PublishAbort
	"target_vault":       true, // the controller's routing field, healed on write
	"previous_assignee":  true, // trigger-cap escalation
	"retry_count":        true, // the probe path's spawn-time bump, reset to 0 on reuse
}

// unionFields returns a fresh map holding every key of the given sets.
func unionFields(sets ...map[string]bool) map[string]bool {
	fields := map[string]bool{}
	for _, set := range sets {
		for key := range set {
			fields[key] = true
		}
	}
	return fields
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

// readdressRequest is the body of POST /api/v1/tasks/{id}/readdress.
type readdressRequest struct {
	TaskIdentifier string `json:"task_identifier"`
}

// handleReaddressTask serves POST /api/v1/tasks/{id}/readdress: the task's
// task_identifier changes and the file does not move.
//
// It exists because PATCH refuses task_identifier by design — *"only a create may
// name it: a PATCH that changed it would break the addressing the API rests on"*
// — and the controller's planning-retry mints a new identifier on every retry,
// because a retry is a fresh attempt rather than a continuation. That intent is
// legitimate, so it gets its own verb: one auditable operation that says "this
// task is now addressed differently", instead of smuggling the change through an
// ordinary field write.
//
// ⚠️ The cost is real and is not hidden: the old identifier stops resolving for
// every caller holding it. The refusal when the target identifier is already
// taken is therefore mandatory, not defensive — silently merging two tasks into
// one address is the worst outcome available here.
func handleReaddressTask(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request readdressRequest
		if !decodeBody(w, r, &request) {
			return
		}
		next := strings.TrimSpace(request.TaskIdentifier)
		if next == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid_task_identifier",
			})
			return
		}
		document, ok := loadDocument(ctx, w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		if document.item.ID == next {
			// Already addressed that way: a retry of the same move is a no-op, not
			// a collision with itself. Answering 409 here would make the verb
			// non-idempotent for a caller that re-sends it.
			writeJSON(w, http.StatusOK, document.view())
			return
		}
		if occupant, found, _ := deps.index.GetTask(next); found &&
			occupant.Path != document.item.Path {
			// A different page already answers to the target. The Path comparison
			// is what separates that from the task's own title or slug resolving
			// back to itself — GetTask keys identifier, name and slug in one map,
			// so re-addressing to one's own title lands here otherwise and would
			// answer 409 naming the task itself.
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":           "task_exists",
				"id":              occupant.Title,
				"task_identifier": occupant.ID,
				"status":          occupant.Status,
			})
			return
		}
		document.set("task_identifier", next)
		// Set before persist, not after: persist publishes the change event, and
		// that event's TaskIdentifier is read from the item. Assigning afterwards
		// emits a re-address announcement carrying the OLD address, which is the
		// one value a caller must not be told after the move.
		document.item.ID = next
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

// patchFields is the frontmatter a PATCH may name: the typed surface plus the
// keys the agent pipeline owns, unioned once at init. The call site below is
// handed this flat map, not the two sets — the typed/pipeline distinction lives
// at the two declarations above, not at the refusal.
var patchFields = unionFields(typedTaskFields, agentPipelineFields)

// patchFieldRefusal answers the request itself when a named field may not be
// written through PATCH, and returns "" when every field is allowed.
func patchFieldRefusal(w http.ResponseWriter, request map[string]any, path string) string {
	return fieldRefusal(w, request, patchFields, path)
}

// createFields is the PATCH allowlist plus task_identifier, which only a create
// may name: a PATCH that changed it would break the addressing the API rests on.
var createFields = unionFields(patchFields, map[string]bool{"task_identifier": true})

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
			// The title path is occupied, and the right answer depends on the
			// occupant — so the refusal names it. It used to answer with `id: title`,
			// which is the address the caller had just sent: the caller learned
			// nothing it did not already know, and could not tell "same task,
			// terminal, reopen" from "different task, disambiguate the title"
			// without a second read it had no address for.
			// ⚠️ Resolved by PATH, never through index.GetTask(title). That is a
			// shared address space — identifier, name and slug all resolve there —
			// so a page whose task_identifier equals this title wins the lookup,
			// and this branch would then judge ITS status and overwrite the file at
			// `path`, which belongs to a different task. Reachable through the API
			// in three calls: create "Bravo" carrying task_identifier "Alpha" (its
			// file is Bravo.md, so nothing collides), create "Alpha" carrying
			// task_identifier "task-1", then reopen "Alpha" — GetTask resolves to
			// Bravo, reads its terminal status, and a 200 replaces the live Alpha.md.
			occupant, found := occupantAtPath(ctx, deps, title)
			if !found || request.Reopen != reopenTerminal {
				writeTaskExists(w, path, occupant, found)
				return
			}
			if !vaultapi.IsTerminalStatus(occupant.Status) {
				// A live occupant holds the path. Reopening one would let two
				// attempts share an identity, which is the collision the vault's
				// terminal-status rule exists to prevent.
				writeTaskExists(w, path, occupant, true)
				return
			}
			reopenTerminalTask(ctx, w, deps, request, path)
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

// occupantAtPath reads the page that actually occupies a title path and returns
// its identifier and status.
//
// ⚠️ It deliberately does not use index.GetTask(title): that resolves identifier,
// name and slug in one key space, so a page whose task_identifier happens to equal
// this title shadows the real occupant. Judging the shadow's status and then
// writing the path would destroy a live task — the sequence is spelled out at the
// call site. Reading the page the caller is about to overwrite is the only
// answer that cannot be shadowed.
func occupantAtPath(
	ctx context.Context,
	deps writeDeps,
	title string,
) (vaultapi.TaskItem, bool) {
	page, err := deps.store.ReadPage(ctx, deps.vaultPath, deps.tasksDir, title)
	if err != nil {
		return vaultapi.TaskItem{}, false
	}
	frontmatter, err := storage.ParseFrontmatterMap(ctx, []byte(string(page.Content)))
	if err != nil {
		return vaultapi.TaskItem{}, false
	}
	// Both values are normalised the way the index normalises them, because
	// IsTerminalStatus is fed the index's TaskItem.Status elsewhere: comparing a
	// raw string here and a normalised one there is exactly the drift one shared
	// predicate is supposed to prevent. An absent or unparseable status normalises
	// to "" — not terminal — so a page whose status cannot be read can never
	// authorise replacing itself.
	status := ""
	if raw, present := frontmatter["status"]; present {
		if normalized, ok := domain.NormalizeTaskStatus(fmt.Sprint(raw)); ok {
			status = string(normalized)
		}
	}
	// A page with no task_identifier is addressed by its slug, and saying so is
	// better than advertising the string "<nil>" in the refusal.
	identifier := ""
	if raw, present := frontmatter["task_identifier"]; present {
		identifier = fmt.Sprint(raw)
	}
	return vaultapi.TaskItem{
		ID:     identifier,
		Title:  title,
		Status: status,
	}, true
}

// writeTaskExists answers a create that collided, naming the occupant rather than
// echoing the title the caller sent. `id` keeps its original meaning — the title —
// so an existing caller reading it is unaffected; task_identifier and status are
// additive, and they are what let a caller decide reopen-versus-disambiguate
// without a second read it has no address for.
func writeTaskExists(
	w http.ResponseWriter,
	path string,
	occupant vaultapi.TaskItem,
	found bool,
) {
	payload := map[string]string{
		"error": "task_exists",
		"id":    strings.TrimSuffix(filepath.Base(path), ".md"),
		"path":  path,
	}
	if found {
		// Only the fields that are actually present: a page addressed by its slug
		// has no task_identifier, and advertising "<nil>" would be the misleading
		// payload this refusal exists to replace.
		if occupant.ID != "" {
			payload["task_identifier"] = occupant.ID
		}
		if occupant.Status != "" {
			payload["status"] = occupant.Status
		}
	}
	writeJSON(w, http.StatusConflict, payload)
}

// reopenTerminal replaces a task whose status is terminal with the request's own
// frontmatter and body.
//
// ⚠️ It REPLACES rather than merges, and that is the whole reason this is a mode
// on create rather than a read-then-PATCH: PATCH merges an allowlist, so a
// reopened task would keep the previous attempt's completed_date, recovery_sha,
// superseded_by and terminal phase. That is not a reopen, it is a live task
// wearing a dead task's history.
func reopenTerminalTask(
	ctx context.Context,
	w http.ResponseWriter,
	deps writeDeps,
	request createRequest,
	path string,
) {
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
	writeJSON(w, http.StatusOK, TaskDocument{
		ID:          fmt.Sprint(frontmatter["task_identifier"]),
		Title:       strings.TrimSpace(request.Title),
		Frontmatter: frontmatter,
		Body:        request.Body,
		Reopened:    true,
	})
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
