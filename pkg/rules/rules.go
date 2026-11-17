// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rules carries the write rules the API enforces: which phase and
// status transitions are legal, and which guards gate the approve endpoint.
//
// It reproduces vault-cli's approve guards rather than importing them. Those
// guards are unexported methods of the approve operation and return plain
// errors with no type to map onto 400/409/422, so this package carries its own
// typed RefusalError with the same three conditions and the same refusal wording.
// Injecting a git-rest-backed storage.TaskStorage into
// ops.NewTaskApproveOperation would give exact guard parity, but its errors are
// untyped strings and the handler would have to match on message text to pick a
// status code.
package rules

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bborbe/vault-cli/pkg/domain"
)

// RefusalError codes. Each names one refusal class; the HTTP layer maps a code to a
// status, so the code is the contract and the message is for humans.
const (
	// CodeInvalidTransition is an edge the graph does not carry.
	CodeInvalidTransition = "invalid_transition"
	// CodeApprovalRequired is a write that only an approval may perform.
	CodeApprovalRequired = "approval_required"
	// CodeApprovalIsAVerb is an attempt to write the approval record by edit.
	CodeApprovalIsAVerb = "approval_is_a_verb"
	// CodeNotApprovable is an approval of a task that is not in the inbox.
	CodeNotApprovable = "not_approvable"
	// CodeAlreadyApproved is an approval of a task that already carries one.
	CodeAlreadyApproved = "already_approved"
	// CodeBlankApprover is an approval naming no approver.
	CodeBlankApprover = "blank_approver"
	// CodeInvalidClock is an approval whose instant is the zero value.
	CodeInvalidClock = "invalid_clock"
	// CodeUnknownPhase is a phase value outside the enum.
	CodeUnknownPhase = "unknown_phase"
	// CodeUnknownStatus is a status value outside the enum.
	CodeUnknownStatus = "unknown_status"
)

// ApproveEndpoint is the only route that may write an approval record. It
// appears in the allowed list of every refusal that says approval is a verb.
const ApproveEndpoint = "POST /api/v1/tasks/{id}/approve"

// RefusalError is the typed error every rule in this package returns. Code names the
// refusal class; From, To and Allowed carry the detail a caller needs to act on
// the refusal rather than merely read it.
type RefusalError struct {
	Code    string
	Detail  string
	From    string
	To      string
	Allowed []string
}

// Error returns the human-readable refusal message.
func (r *RefusalError) Error() string {
	return r.Detail
}

// phaseEdges is the legal phase graph. todo -> planning is deliberately absent:
// leaving the approval inbox is an approval, not an edit, so it is reachable
// only through the approve endpoint.
//
// planning and execution reach human_review directly, and that edge is not a
// shortcut around review. It exists for the park: a record the redrive cap has
// stopped driving needs a human now, from whatever phase it happens to be in.
// Routing it through ai_review first would assert that an AI review took place
// when none did — the park is the absence of progress, not the end of it. The
// ai_review -> human_review edge stays for the ordinary case, where a review
// really did run.
//
// Every working phase (planning, execution, ai_review) also reaches done
// directly. That edge is the succeeded park's: a Job that exits 0 without
// advancing the phase is finished work, not work that needs a human, so the
// executor's PublishParkSucceeded closes the record from the phase it still
// sits at. Without it that write is refused (`invalid_transition: planning ->
// done`) and a finished record never terminalises — it is re-driven until the
// re-drive cap parks it again. This is distinct from the human_review edges
// above, which serve the cap park (a record that needs a human); done is the
// succeeded park (a record that does not). todo is still excluded: leaving the
// approval inbox is an approval, not a close.
//
// planning -> ai_review stays absent on purpose: a record that has never left
// planning has nothing to review yet.
var phaseEdges = map[domain.TaskPhase][]domain.TaskPhase{
	domain.TaskPhasePlanning: {
		domain.TaskPhaseExecution,
		domain.TaskPhaseHumanReview,
		domain.TaskPhaseDone,
	},
	domain.TaskPhaseExecution: {
		domain.TaskPhaseAIReview,
		domain.TaskPhasePlanning,
		domain.TaskPhaseHumanReview,
		domain.TaskPhaseDone,
	},
	domain.TaskPhaseAIReview: {
		domain.TaskPhaseHumanReview,
		domain.TaskPhaseDone,
	},
	domain.TaskPhaseHumanReview: {domain.TaskPhaseDone},
}

// statusEdges is the legal status graph. completed and aborted are terminal, so
// they carry no outgoing edge.
var statusEdges = map[domain.TaskStatus][]domain.TaskStatus{
	domain.TaskStatusNext: {domain.TaskStatusInProgress},
	domain.TaskStatusInProgress: {
		domain.TaskStatusCompleted,
		domain.TaskStatusAborted,
		domain.TaskStatusHold,
	},
	domain.TaskStatusHold: {domain.TaskStatusInProgress},
}

// ValidatePhaseTransition reports whether moving a task from one phase to
// another is legal. The returned error is always a *RefusalError.
func ValidatePhaseTransition(from, to domain.TaskPhase) error {
	if from == domain.TaskPhaseTodo && to == domain.TaskPhasePlanning {
		return &RefusalError{
			Code: CodeApprovalRequired,
			Detail: fmt.Sprintf(
				"refusing phase transition %q -> %q: leaving the approval inbox is an approval, not an edit; use %s",
				from,
				to,
				ApproveEndpoint,
			),
			From:    string(from),
			To:      string(to),
			Allowed: []string{ApproveEndpoint},
		}
	}
	allowed := phaseEdges[from]
	if containsPhase(allowed, to) {
		return nil
	}
	return &RefusalError{
		Code: CodeInvalidTransition,
		Detail: fmt.Sprintf(
			"refusing phase transition %q -> %q: allowed from %q is %v",
			from, to, from, phaseStrings(allowed),
		),
		From:    string(from),
		To:      string(to),
		Allowed: phaseStrings(allowed),
	}
}

// ValidateStatusTransition reports whether moving a task from one status to
// another is legal at the task's current phase. A task enters in_progress only
// once its phase is past todo, so that one edge is refused while the task still
// sits in the approval inbox. The returned error is always a *RefusalError.
func ValidateStatusTransition(
	phase domain.TaskPhase,
	from, to domain.TaskStatus,
) error {
	if to == domain.TaskStatusInProgress && !phasePastTodo(phase) {
		return &RefusalError{
			Code: CodeApprovalRequired,
			Detail: fmt.Sprintf(
				"refusing status transition %q -> %q while phase is %q: a task enters %q only after approval; use %s",
				from,
				to,
				phase,
				domain.TaskStatusInProgress,
				ApproveEndpoint,
			),
			From:    string(from),
			To:      string(to),
			Allowed: []string{ApproveEndpoint},
		}
	}
	allowed := statusEdges[from]
	if containsStatus(allowed, to) {
		return nil
	}
	return &RefusalError{
		Code: CodeInvalidTransition,
		Detail: fmt.Sprintf(
			"refusing status transition %q -> %q: allowed from %q is %v",
			from, to, from, statusStrings(allowed),
		),
		From:    string(from),
		To:      string(to),
		Allowed: statusStrings(allowed),
	}
}

// ValidateApprove applies the three guards the approve endpoint enforces, in the
// order vault-cli applies them: a blank approver, a clock that is not an
// instant, a task outside the approval inbox, and a task that already carries an
// approval record. The returned error is always a *RefusalError.
func ValidateApprove(
	approvedBy string,
	phase domain.TaskPhase,
	hasApprovalRecord bool,
	approvedAt time.Time,
) error {
	if strings.TrimSpace(approvedBy) == "" {
		return &RefusalError{
			Code:   CodeBlankApprover,
			Detail: "approved_by must not be empty: refusing to record a blank approver",
		}
	}
	if approvedAt.IsZero() {
		return &RefusalError{
			Code:   CodeInvalidClock,
			Detail: "approved_at is not a valid instant: refusing to record a zero timestamp",
		}
	}
	if phase != domain.TaskPhaseTodo {
		current := string(phase)
		if current == "" {
			current = "(none)"
		}
		return &RefusalError{
			Code: CodeNotApprovable,
			Detail: fmt.Sprintf(
				"refusing to approve: task is at phase %q, not %q; only a task in the approval inbox (phase %q) can be approved",
				current,
				domain.TaskPhaseTodo,
				domain.TaskPhaseTodo,
			),
			From: current,
			To:   string(domain.TaskPhasePlanning),
		}
	}
	if hasApprovalRecord {
		return &RefusalError{
			Code:   CodeAlreadyApproved,
			Detail: "refusing to approve: task already carries an approval record (approved_by, approved_at); re-approving is refused",
		}
	}
	return nil
}

// RefuseApprovalRecordWrite is the refusal for an edit that tries to write the
// approval record directly instead of going through the approve endpoint.
func RefuseApprovalRecordWrite() error {
	return &RefusalError{
		Code: CodeApprovalIsAVerb,
		Detail: "approved_by is not writable here: the approval record is written only by " +
			ApproveEndpoint,
		Allowed: []string{ApproveEndpoint},
	}
}

// UnknownPhase is the refusal for a phase value outside the enum.
func UnknownPhase(raw string) error {
	return &RefusalError{
		Code: CodeUnknownPhase,
		Detail: fmt.Sprintf(
			"unknown phase %q: %v are the valid values",
			raw,
			phaseStrings(domain.AvailableTaskPhases),
		),
	}
}

// UnknownStatus is the refusal for a status value outside the enum.
func UnknownStatus(raw string) error {
	return &RefusalError{
		Code: CodeUnknownStatus,
		Detail: fmt.Sprintf(
			"unknown status %q: %v are the valid values",
			raw,
			statusStrings(domain.AvailableTaskStatuses),
		),
	}
}

// phasePastTodo reports whether a phase is at or beyond planning. An unset phase
// is not past todo: a task without a phase has never been approved either.
func phasePastTodo(phase domain.TaskPhase) bool {
	switch phase {
	case domain.TaskPhasePlanning,
		domain.TaskPhaseExecution,
		domain.TaskPhaseInProgress,
		domain.TaskPhaseAIReview,
		domain.TaskPhaseHumanReview,
		domain.TaskPhaseDone:
		return true
	case domain.TaskPhaseTodo:
		return false
	}
	return false
}

func containsPhase(allowed []domain.TaskPhase, want domain.TaskPhase) bool {
	return slices.Contains(allowed, want)
}

func containsStatus(allowed []domain.TaskStatus, want domain.TaskStatus) bool {
	return slices.Contains(allowed, want)
}

func phaseStrings(phases []domain.TaskPhase) []string {
	out := make([]string, 0, len(phases))
	for _, phase := range phases {
		out = append(out, string(phase))
	}
	return out
}

func statusStrings(statuses []domain.TaskStatus) []string {
	out := make([]string, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, string(status))
	}
	return out
}
