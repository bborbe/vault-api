// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"context"
	"errors"
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
	// themes is a modelled task field, not pipeline metadata: every hand-written
	// task in every vault carries it and the vault UI renders it. It arrives on
	// the CREATE path from every recurring template, so refusing it refused the
	// whole instance — measured 2026-10-29, the daily recurring cadence produced
	// zero task files that day, and the field named in the 400 was this one.
	"themes": true,
	// page_type is how every vault page declares what it is (`task`, `goal`,
	// `runbook`, …). recurring-task-creator stamps it on every instance, and
	// vault-cli reads it to decide which folder and template a record belongs
	// to, so it is modelled rather than stored blind.
	"page_type": true,
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
// The pipeline's entries are enumerated from those producers' source in
// task_content_allowlist_internal_test.go, which asserts the patch allowlist
// covers them; a key added to one side without the other fails the build. A
// producer that starts writing a new key is still transcribed there by hand,
// because it lives in another repository.
var agentPipelineFields = map[string]bool{
	"trigger_scope":      true, // PublishSetTriggerScope
	"trigger_count":      true, // PublishSetTriggerScope, IncrementFrontmatterCommand
	"max_triggers":       true, // the operator's cap definition, armed on the crash class
	"current_job":        true, // PublishSpawnNotification, cleared by PublishClearCurrentJob
	"claude_session_id":  true, // the spawn's session anchor, written in the same update-frontmatter patch
	"job_started_at":     true, // PublishSpawnNotification
	"spawn_notification": true, // PublishSpawnNotification
	"queued_at":          true, // PublishQueueEntry
	"aborted_reason":     true, // PublishAbort
	"gate_successor":     true, // PublishAbort
	"target_vault":       true, // the controller's routing field, healed on write
	"previous_assignee":  true, // trigger-cap escalation
	"retry_count":        true, // the probe path's spawn-time bump, reset to 0 on reuse

	// The run's outcome, cleared at spawn by PublishSpawnNotification so the park
	// gate reads THIS run rather than the last one that delivered. It rides the
	// same update-frontmatter patch as current_job and claude_session_id, and
	// fieldRefusal refuses the whole patch on the first unknown key — measured
	// 2026-11-12 on agent-task-controller-personal-0: the spawn of task
	// f28ae749 wrote `unknown_field: last_result_status` and lost current_job and
	// claude_session_id with it, which is why a probe that ran and completed
	// carried no session anchor at all.
	"last_result_status": true, // PublishSpawnNotification clears it; the agent delivery stamps it

	// The complete-task and auto-supersede patches, both of which reach PatchTask
	// from agent-task-controller. They were in neither allowlist, so no
	// complete-task or supersede command could ever have succeeded:
	// buildCompletePatch sends completed_date and processed_at unconditionally,
	// and transitionPriorTyped sends superseded_by on every supersede. Neither
	// path had fired on dev in the 48 hours this was measured, which is why a log
	// sample could not find them — the outage was latent, not silent.
	"completed_date": true, // buildCompletePatch, transitionPriorTyped
	"processed_at":   true, // buildCompletePatch
	"superseded_by":  true, // transitionPriorTyped (auto-supersede)

	// Written by github-pr-watcher into every task it creates, and by the
	// release watcher into every release task, so they arrive on the CREATE path
	// and were refused there. The executor/controller entries above never covered
	// them because those are different producers: the watchers are the only
	// components that name the repo, the base branch and the commit a task is
	// about, and no typed key carries any of that.
	//
	// ⚠️ fieldRefusal names ONE key and Go map iteration is random, so a create
	// missing several reports a different field each attempt — measured
	// 2026-10-28, the same outage logged `title` 9×, `clone_url` 5×, `base_ref`
	// 5×, `ref` 4×, `repo` 1×, `current_version` 1×. Widening by the field named
	// in one log line fixes one sixth of it; this list is the whole set.
	"base_ref":        true, // the PR's base branch
	"clone_url":       true, // the repo to clone
	"ref":             true, // the commit under review / the release ref
	"repo":            true, // owner/repo, on release tasks
	"current_version": true, // the version a release task starts from
	// override_reason names the mechanical rule on a pr-override. The
	// pr-override agent posts its mechanical APPROVE only when this key reads
	// "trivial-bot-bump" and falls back to the label body when it is absent, so
	// refusing it here lost the whole override rather than only its metadata
	// (measured 2026-10-29: the create 400'd, the command was consumed and
	// dropped, and the PR was left with neither an override nor a review).
	"override_reason": true,
	// title is ALSO a top-level create field that owns the filename. The
	// pipeline sends it in the frontmatter as well, and refusing that copy
	// refused the whole create. It is stored here, not interpreted: the filename
	// still comes from the request's own title, so the two cannot drift apart
	// through this key.
	"title": true,
	// Written by recurring-task-creator on every instance it materializes, and
	// by the sentry-analyzer producer on the tasks it files. All three arrive on
	// the CREATE path from a template, and `fieldRefusal` names one key while Go
	// map iteration is random — so a create carrying several of these reported a
	// different field each attempt and looked like three separate bugs. Measured
	// 2026-10-29 on prod: `themes` ×2, `created_by` ×1, `date` ×1, each refusing
	// a whole recurring instance whose body was then discarded with it.
	"created_by":       true, // recurring-task-creator, sentry producers
	"auto_abort_prior": true, // recurring-task-creator's supersede flag
	"date":             true, // the sentry producers' event date
	"launcher":         true, // recurring-task-creator, on 9 of the prod templates

	// Written by the watchers' own CreateCommand frontmatter. Each key names the
	// builder that sets it; the watchers are the only producers that know the
	// repo, branch, commit or release a task is about, and no typed key carries
	// any of that. Enumerated from those builders rather than from a log sample:
	// `fieldRefusal` names one key per attempt and Go map iteration is random,
	// so a log reports a different key each time and cannot enumerate the set.
	"branch":            true, // github-dark-factory-watcher buildFrontmatter
	"pr_number":         true, // github-dark-factory-watcher buildFrontmatter
	"current_go":        true, // github-update-go-watcher buildFrontmatter
	"latest_go":         true, // github-update-go-watcher buildFrontmatter
	"episode_sha":       true, // github-build-watcher buildCreateTaskCommand
	"vuln_count":        true, // github-vuln-watcher buildFrontmatter
	"vulns":             true, // github-vuln-watcher buildFrontmatter
	"short_id":          true, // sentry-watcher buildFrontmatter
	"project":           true, // sentry-watcher buildFrontmatter
	"issue_url":         true, // sentry-watcher buildFrontmatter
	"new_version":       true, // go-version-watcher buildFrontmatter
	"previous_version":  true, // go-version-watcher buildFrontmatter
	"release_kind":      true, // go-version-watcher buildFrontmatter
	"release_notes_url": true, // go-version-watcher buildFrontmatter
	// recovery_sha rides the COMPLETE-TASK patch, not a create: the controller
	// sets patch["recovery_sha"] in task_complete_task_executor.go. It belongs
	// here because createFields unions patchFields — there is no second list.
	"recovery_sha": true, // agent-task-controller complete-task patch
	// run_outcome and failure_reason ride the COMPLETE-TASK patch from
	// bborbe/github-pr-review-agent: stampRunOutcome writes them on every
	// terminal routing (post_failed / no_review / budget_expired), so they
	// arrive on any run that ends without posting a review.
	//
	// Refusing them failed the whole read-modify-write, not just the metadata —
	// measured 2026-11-17 on the dev stage, agent v0.18.2, task f88cced8:
	// `patch attempt 3 of 3 failed … 400 unknown_field field=run_outcome`, and
	// the record kept its pre-run state with no ## Diagnostics block. The prod
	// stage writes to a vault whose writer already accepts them, which is why
	// this only surfaced on dev.
	"run_outcome":    true, // github-pr-review-agent stampRunOutcome
	"failure_reason": true, // github-pr-review-agent stampRunOutcome

	// The strategy-prototype agent's own run record, written on the delivery
	// that carries its ## Result and ## Ledger. run_id is the agent's publish
	// step's guard against running one idea twice; metrics_runs is the
	// controller's transcription of the run's per-variant metrics. Neither was
	// in this set, so the whole result delivery was refused and the page kept
	// its pre-run state.
	//
	// Measured 2026-11-17 on the dev stage: three prototype runs each ended
	// `status: completed` / `phase: done` with NO ## Result and NO ## Ledger —
	// `patch attempt 1 of 3 failed … 400 unknown_field field=run_id`, then
	// `field=metrics_runs`. The frontmatter transition landed and the body was
	// dropped with it, so the run's verdict reached its page not at all.
	"run_id":       true, // the strategy-prototype agent's publish step, carried by the controller's patch
	"metrics_runs": true, // the controller's result writer, transcribed from the run's metrics
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
//
// ?ref=<ref> serves the same document read from the committed history instead of
// the working tree. The two views differ for up to one git-rest commit window:
// a write is in the working tree the moment vault-api accepts it, while the
// commit that carries it lands on git-rest's cadence. A caller verifying that a
// write landed must ask for the committed view, because the working-tree view
// already reports a write that has not been committed at all.
func handleGetTaskDocument(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ref := strings.TrimSpace(r.URL.Query().Get("ref")); ref != "" {
			document, ok := loadCommittedDocument(r.Context(), w, deps, mux.Vars(r)["id"], ref)
			if !ok {
				return
			}
			writeJSON(w, http.StatusOK, document.view())
			return
		}
		document, ok := loadDocument(r.Context(), w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, document.view())
	}
}

// loadCommittedDocument resolves the id through the index and reads the page as
// it exists at ref, rather than as the working tree currently holds it.
//
// The index still supplies the path: a record that has never been committed has
// no path at the ref either, and answering from the index is what keeps the
// route's identifier addressing identical on both views.
//
// ⚠️ ID and Title are the working-tree projection, not the committed ones. In the
// window this route exists to serve — a working-tree write that has not committed
// — a re-addressed or renamed task therefore reports the working-tree ID/Title
// alongside the committed frontmatter and body. Callers comparing ID against
// frontmatter.task_identifier must treat a mismatch as that window, not as
// corruption.
func loadCommittedDocument(
	ctx context.Context,
	w http.ResponseWriter,
	deps writeDeps,
	id string,
	ref string,
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
	raw, err := deps.differ.ReadAt(ctx, ref, item.Path)
	if err != nil {
		// A path absent from a ref that does resolve is the ordinary
		// not-committed-yet state, and 404 is what lets the caller treat it as
		// "not landed" rather than as a broken read.
		if errors.Is(err, vaultapi.ErrNotAtRef) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error":  "not_found",
				"reason": "not present at ref " + ref,
				"path":   item.Path,
			})
			return document{}, false
		}
		if errors.Is(err, vaultapi.ErrInvalidRef) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid_ref",
			})
			return document{}, false
		}
		// A fixed reason: the underlying error carries git's argv and stderr,
		// which would echo the vault's on-disk paths back to the caller. The
		// detail is logged by the differ.
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "read_failed",
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
	return document{
		item:        item,
		raw:         raw,
		frontmatter: frontmatter,
		changed:     map[string]any{},
	}, true
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
		// "body" is the one key that is not frontmatter: it replaces the file's
		// body in the same commit as the field changes, so a caller that must move
		// frontmatter and body together (the controller's planning retry appends a
		// bullet and bumps a counter) does not have to make two writes and lose the
		// atomicity between them.
		if raw, ok := request["body"]; ok {
			body, isString := raw.(string)
			if !isString {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error":  "invalid_body",
					"reason": "body must be a string",
				})
				return
			}
			document.setBody(body)
			delete(request, "body")
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

// readdressRetry answers a readdress whose target the task already carries. With
// no previous there is nothing to write; otherwise previous is recorded alone.
func readdressRetry(
	ctx context.Context,
	w http.ResponseWriter,
	deps writeDeps,
	document *document,
	previous string,
) {
	if previous == "" {
		writeJSON(w, http.StatusOK, document.view())
		return
	}
	document.set("task_identifier_previous", previous)
	if !document.persist(ctx, w, deps) {
		return
	}
	writeJSON(w, http.StatusOK, document.view())
}

// readdressRequest is the body of POST /api/v1/tasks/{id}/readdress.
//
// Previous is optional and additive: a request that omits it behaves exactly as
// this route always has. When supplied it is recorded as the file's
// task_identifier_previous in the SAME write as the new identifier, so a result
// already in flight under the replaced address still resolves to this file. The
// scanner's identifier repair needs that pairing — writing the two in separate
// requests would leave a window where neither address resolves.
type readdressRequest struct {
	TaskIdentifier string `json:"task_identifier"`
	Previous       string `json:"previous,omitempty"`
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
		previous := strings.TrimSpace(request.Previous)
		// A "previous" equal to the new address records nothing: it would tell a
		// later resolver that the task used to answer to the name it now carries.
		if previous == next {
			previous = ""
		}
		document, ok := loadDocument(ctx, w, deps, mux.Vars(r)["id"])
		if !ok {
			return
		}
		if document.item.ID == next {
			// Already addressed that way: a retry of the same move is a no-op, not
			// a collision with itself. Answering 409 here would make the verb
			// non-idempotent for a caller that re-sends it.
			//
			// `previous` is still applied, because a caller re-sending the whole
			// move must not lose the in-flight identity just because the
			// identifier half already landed. With no previous there is nothing
			// to write, so the no-op stays a no-op.
			readdressRetry(ctx, w, deps, &document, previous)
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
		// Recorded in the same patch as the identifier, so the move and the
		// in-flight address are one commit rather than two.
		if previous != "" {
			document.set("task_identifier_previous", previous)
		}
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

// metricsFieldPrefix admits the strategy-prototype agent's run-record namespace
// as a prefix rather than as enumerated keys.
//
// The two sets above stay closed, and every other unknown key still answers 400
// unknown_field. This one namespace is prefix-matched because its key list is
// not this API's to freeze: the executor's per-run token budget and the
// controller's accumulatedCounters both live in another repository, and a new
// metrics_ key there must not require a vault-api release before the agent can
// publish a verdict. A closed list refuses the WHOLE read-modify-write patch on
// the first unknown key, so one new counter costs the entire ## Data and
// ## Result, not just that counter.
//
// Measured 2026-11-18 on dev vault-api v0.27.1: six metrics_ keys refused,
// 240 attempts in 24 h, every one of them a successful prototype run that could
// not write its page. run_id and metrics_runs were already enumerated (they were
// the 2026-11-17 instance of this same class); the next key was the next
// outage, which is why the fix is the prefix and not six more lines.
//
// The namespace is narrow on purpose: only "metrics_" prefixed keys bypass the
// closed sets. A non-metrics_ unknown key is still refused, so the drift control
// the two allowlists exist for survives.
const metricsFieldPrefix = "metrics_"

func prefixAdmitted(key string) bool {
	return strings.HasPrefix(key, metricsFieldPrefix)
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
		if !allowed[key] && !prefixAdmitted(key) {
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
// patchBodyKey is the one request key on PATCH that is not a frontmatter field:
// it replaces the file's body, in the same commit as the field changes.
const patchBodyKey = "body"

// patchAcceptedFields is the PATCH allowlist plus the body key. It is separate
// from patchFields so the body never becomes a writable FRONTMATTER field — the
// create route and every other consumer keep seeing the fields-only list.
var patchAcceptedFields = unionFields(patchFields, map[string]bool{patchBodyKey: true})

func patchFieldRefusal(w http.ResponseWriter, request map[string]any, path string) string {
	return fieldRefusal(w, request, patchAcceptedFields, path)
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
			answerOccupiedPath(ctx, w, deps, request, title, path)
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
		indexCreatedTask(deps, path, frontmatter, rendered)
		writeJSON(w, http.StatusCreated, TaskDocument{
			ID:          fmt.Sprint(frontmatter["task_identifier"]),
			Title:       title,
			Frontmatter: frontmatter,
			Body:        request.Body,
		})
	}
}

// answerOccupiedPath answers a create whose title path is already taken, and the
// right answer depends on the occupant — so the refusal names it. It used to
// answer with `id: title`, which is the address the caller had just sent: the
// caller learned nothing it did not already know, and could not tell "same task,
// terminal, reopen" from "different task, disambiguate the title" without a
// second read it had no address for.
//
// ⚠️ The occupant is resolved by PATH, never through index.GetTask(title). That is
// a shared address space — identifier, name and slug all resolve there — so a page
// whose task_identifier equals this title wins the lookup, and this branch would
// then judge ITS status and overwrite the file at `path`, which belongs to a
// different task. Reachable through the API in three calls: create "Bravo"
// carrying task_identifier "Alpha" (its file is Bravo.md, so nothing collides),
// create "Alpha" carrying task_identifier "task-1", then reopen "Alpha" — GetTask
// resolves to Bravo, reads its terminal status, and a 200 replaces the live
// Alpha.md.
func answerOccupiedPath(
	ctx context.Context,
	w http.ResponseWriter,
	deps writeDeps,
	request createRequest,
	title string,
	path string,
) {
	occupant, found := occupantAtPath(ctx, deps, title)
	if !found || request.Reopen != reopenTerminal {
		writeTaskExists(w, path, occupant, found)
		return
	}
	if vaultapi.IsTerminalStatus(occupant.Status) {
		reopenTerminalTask(ctx, w, deps, request, path)
		return
	}
	// A live occupant holds the path. Reopening one would let two attempts share an
	// identity, which is the collision the vault's terminal-status rule exists to
	// prevent.
	//
	// A probe record is the one exception, and it is not an exception to that rule
	// but the case the rule mis-reads: a probe is re-driven on every tick with the
	// SAME identifier, so a live probe occupant is not a second attempt sharing an
	// identity — it is this attempt, re-issued. Refusing it kills the lane's probe
	// silently for as long as the record parks (measured 2026-11-11:
	// `probe-service-analysis-agent-dev` parked at human_review, every later tick
	// refused with `title … occupied`). isProbeReopen holds the exception to the
	// identifier test.
	if isProbeReopen(request, occupant) {
		reopenTerminalTask(ctx, w, deps, request, path)
		return
	}
	writeTaskExists(w, path, occupant, true)
}

// indexCreatedTask records the task a create or reopen just wrote, so the read
// path resolves it at once.
//
// It is called only after the write landed, and only with the bytes that landed:
// the index is fed the same frontmatter and content the file holds, through the
// same taskItem conversion a full build uses, so the two cannot describe the
// task differently.
//
// Skipping it is what made a create and the read that follows it disagree for up
// to one refresh interval: the index is rebuilt only when the working tree's
// HEAD moves, and a caller that creates a task and then addresses it — the
// executor's probe publishes create-task and update-frontmatter back to back —
// got a 404 for a task that existed. Measured on dev 2026-11-09: the update ran
// 4 ms after the create and was dropped with "task not found".
func indexCreatedTask(
	deps writeDeps,
	path string,
	frontmatter map[string]any,
	rendered string,
) {
	modified := deps.clock.Now().Time()
	deps.index.UpsertTask(domain.NewPage(
		frontmatter,
		domain.FileMetadata{
			Name:         strings.TrimSuffix(filepath.Base(path), ".md"),
			FilePath:     filepath.Join(deps.vaultPath, filepath.FromSlash(path)),
			ModifiedDate: &modified,
		},
		domain.Content(rendered),
	))
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
	// task_type is carried for isProbeReopen, which compares it to one literal. It
	// is read raw rather than normalised: this handler owns no task_type vocabulary,
	// and inventing one here would make the probe predicate disagree with whatever
	// writes the field.
	taskType := ""
	if raw, present := frontmatter["task_type"]; present {
		taskType = fmt.Sprint(raw)
	}
	return vaultapi.TaskItem{
		ID:       identifier,
		Title:    title,
		Status:   status,
		TaskType: taskType,
	}, true
}

// probeTaskType is the task_type of a healthcheck probe record: a page that is
// rewritten in place by every tick of the probe that owns it, rather than once
// per attempt.
const probeTaskType = "healthcheck"

// isProbeReopen reports whether a NON-terminal occupant may be replaced because it
// is a probe record being re-driven by its own tick.
//
// The identity guarantee is the identifier test: the request must carry the
// occupant's OWN task_identifier. Two different identifiers at one path still
// collide exactly as before, and the recurring carve-out is untouched — it lives
// in the controller and is keyed on terminal status, so it never reaches here.
func isProbeReopen(request createRequest, occupant vaultapi.TaskItem) bool {
	if occupant.TaskType != probeTaskType || occupant.ID == "" {
		return false
	}
	supplied, present := request.Frontmatter["task_identifier"]
	return present && fmt.Sprint(supplied) == occupant.ID
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

// reopenTerminal replaces a task whose status is terminal — or a live probe
// occupant re-driven by its own tick, see isProbeReopen — with the request's own
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
	indexCreatedTask(deps, path, frontmatter, rendered)
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
	//
	// ⚠️ The page AT `path` is exempt, because this write replaces it. A reopen
	// reaches here with the occupant's own identifier — the whole point is to
	// rewrite that record — and counting it as a duplicate of itself refused every
	// reopen with a 409 that named the caller's own identifier. That answer is
	// indistinguishable at the caller from "the title is occupied", so a probe
	// re-creating the record it already owns could never get past it (measured
	// 2026-11-09 on nuke prod: all 17 `-prod` probe lanes were refused in one
	// 27-second window, and only three failure lines reached the log because the
	// result sender's failure line passes a 10-second rate sampler). An identifier
	// carried by a DIFFERENT page is still refused — that is the collision this
	// guard exists to catch.
	if supplied, present := frontmatter["task_identifier"]; present {
		identifier := fmt.Sprint(supplied)
		if existing, found, _ := deps.index.GetTask(identifier); found &&
			filepath.ToSlash(existing.Path) != path {
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
