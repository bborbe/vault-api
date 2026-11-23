// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"time"

	"github.com/bborbe/vault-cli/pkg/domain"
)

// TaskItem is the typed projection of one task page. It is served by
// GET /api/v1/tasks and GET /api/v1/tasks/{id}.
type TaskItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Path     string   `json:"path"`
	Status   string   `json:"status"`
	Phase    string   `json:"phase"`
	TaskType string   `json:"task_type"`
	ShortID  string   `json:"short_id,omitempty"`
	Stage    string   `json:"stage"`
	Assignee string   `json:"assignee"`
	Goals    []string `json:"goals"`
	Cluster  *string  `json:"cluster"`
	Flag     bool     `json:"flag"`
	Vault    string   `json:"vault"`
	// CurrentJob is the task's raw frontmatter current_job — the id of the job
	// that claimed it, empty when none has. The gauge reports its presence as
	// job_state; the id itself is served because a reader chasing a stuck record
	// needs the job, not just the fact of one.
	CurrentJob string `json:"current_job,omitempty"`
	// PRState is the task's last observed PR state, written into the frontmatter
	// by the daily close-obsolete sweep: open, merged, closed or none. Empty means
	// the sweep has not looked at this record yet.
	PRState string `json:"pr_state,omitempty"`
	// UpdatedAt is the page's last-modified time, which is what `updated_since`
	// filters on. It is a pointer because a page the storage layer reported no
	// mtime for must be distinguishable from one modified at the zero instant.
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// DeferDate is the task's frontmatter defer_date, nil when the field is
	// absent. It is served because the `due` filter reads it and a caller checking
	// the boundary needs the value the filter saw.
	DeferDate *time.Time `json:"defer_date,omitempty"`

	// The fields below are the rest of what the board renders. Without them a
	// consumer serving the board from this list must read each task page itself —
	// an N+1 over the whole vault — or drop the field from the card.
	//
	// BlockedBy is the page's blocked_by list, always non-nil so a row with no
	// blockers serialises as [] rather than null. The board's blocked badge reads
	// the list itself, not a precomputed boolean, because it also names the
	// blockers on the card.
	BlockedBy []string `json:"blocked_by"`
	// Priority is the frontmatter priority as an integer, 0 when absent. It is
	// always emitted so a caller ordering the board never has to infer "missing"
	// from an absent key.
	Priority int `json:"priority"`
	// DueDate and PlannedDate are the frontmatter dates rendered by vault-cli's
	// own rule: YYYY-MM-DD for a date-only value, RFC3339 for one carrying a time,
	// nil when absent. The board's urgency tiers compare the date-only form
	// against today, which is why this is a string and not a timestamp.
	DueDate     *string `json:"due_date"`
	PlannedDate *string `json:"planned_date"`
	// ClaudeSessionID is the page's recorded session, null when it carries none.
	// The board's live-session badge resolves liveness from it.
	ClaudeSessionID *string `json:"claude_session_id"`
	// Jira is the page's own `jira:` value, carried verbatim — in the vaults it is
	// as often a bare key as a browse URL — and null when absent.
	Jira *string `json:"jira"`
	// Mode is the page's own `mode:` value, the execution mode the row opens with,
	// null when absent. It is carried verbatim because the field is free-form on
	// disk.
	Mode *string `json:"mode"`
	// OpenQuestions is the task's Open Questions section in section order, always
	// non-nil so a task with none serialises as [] rather than null.
	OpenQuestions []domain.OpenQuestion `json:"open_questions"`
}

// IsTerminalStatus reports whether a task status ends the task's life. The vault
// has exactly two, and this is the one definition of them: the open-task gauge
// uses it to decide what counts as open (openTasksByState), and the create route
// uses it to decide whether an occupying task may be reopened. A second copy of
// this predicate is how the two answers drift apart.
func IsTerminalStatus(status string) bool {
	return status == "completed" || status == "aborted"
}

// GoalItem is the typed projection of one goal page. It is served by
// GET /api/v1/goals and GET /api/v1/goals/{id}.
type GoalItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Path     string   `json:"path"`
	Status   string   `json:"status"`
	Themes   []string `json:"themes"`
	Assignee string   `json:"assignee,omitempty"`
	Vault    string   `json:"vault"`
	// Flag is the page's frontmatter `flag:`, served so a consumer selecting the
	// operator-flagged rows can filter server-side instead of re-reading the page.
	// Without it a goal's flag was invisible to every API consumer, which is why
	// the flagged tick had to parse goal pages itself.
	Flag bool `json:"flag"`
	// SessionID is the page's recorded `claude_session_id`, empty when none. A
	// consumer resolving a subject's live manager needs the recorded session, and
	// this is the only field that carries it.
	SessionID string `json:"claude_session_id"`
	// The five fields below exist so a goal or topic can be enriched from this
	// projection alone. A consumer that selects flagged rows needs the same
	// per-kind facts a task's frontmatter carries — role, mode, due date,
	// unresolved blockers and flag provenance — and without them it must either
	// read the page itself or silently apply the task rule to goals and topics.
	// Both outcomes are wrong: the first defeats the index, the second lets a
	// future-deferred goal open a manager early.
	Role      string   `json:"role"`
	Mode      string   `json:"mode"`
	DeferDate string   `json:"defer_date,omitempty"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	// FlagSetBy is the page's `flag_set_by`, empty when absent. Empty is a real
	// value here — it means the page carries no provenance, which a consumer may
	// treat as a refusal — so it is not omitted.
	FlagSetBy string `json:"flag_set_by"`
}

// TopicItem is the typed projection of one topic page. It is served by
// GET /api/v1/topics.
type TopicItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Path     string   `json:"path"`
	Status   string   `json:"status"`
	Goals    []string `json:"goals"`
	Themes   []string `json:"themes"`
	Assignee string   `json:"assignee,omitempty"`
	Vault    string   `json:"vault"`
	// Flag and SessionID carry the same two fields GoalItem documents, for the
	// same reason: a topic can be flagged, and its manager's recorded session is
	// what a consumer resolves a subject's liveness from. The enrichment fields
	// are documented on GoalItem and apply identically here.
	Flag      bool     `json:"flag"`
	SessionID string   `json:"claude_session_id"`
	Role      string   `json:"role"`
	Mode      string   `json:"mode"`
	DeferDate string   `json:"defer_date,omitempty"`
	BlockedBy []string `json:"blocked_by,omitempty"`
	FlagSetBy string   `json:"flag_set_by"`
}

// ListResponse is the envelope every list endpoint returns. Total is the number
// of items in the response; Warnings counts files excluded from the index
// because they could not be parsed or carry a value outside the enum.
type ListResponse[T any] struct {
	Items           []T   `json:"items"`
	Total           int   `json:"total"`
	Warnings        int   `json:"warnings"`
	IndexAgeSeconds int64 `json:"index_age_seconds"`
}

// Readiness is the body of GET /readiness.
type Readiness struct {
	Ready           bool   `json:"ready"`
	Vault           string `json:"vault"`
	IndexAgeSeconds int64  `json:"index_age_seconds"`
	// TreeAgeSeconds is how long ago the working tree was last fetched from its
	// remote, and is null when the tree carries no FETCH_HEAD to measure. It is
	// separate from IndexAgeSeconds because the two fail independently: the index
	// can stop being confirmed on a live tree, and a tree can freeze under a
	// healthy loop. A caller gating on Ready does not need to read it, but an
	// operator diagnosing a red pod does: it says which half went stale.
	TreeAgeSeconds *int64 `json:"tree_age_seconds"`
	Head           string `json:"head"`
	Warnings       int    `json:"warnings"`
}
