// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import "github.com/prometheus/client_golang/prometheus"

// File-read kinds. They are the label values of vault_api_file_reads_total and
// are bounded by construction, so the counter is pre-initialised for each.
const (
	// FileReadKindList counts files read while listing a directory.
	FileReadKindList = "page_list"
	// FileReadKindRead counts files read by a single-page lookup.
	FileReadKindRead = "page_read"
)

const (
	// TaskTypeUnknown is the label value used for a task whose frontmatter carries
	// no task_type. It keeps the gauge's total equal to the real open-task count
	// instead of silently dropping untyped pages.
	TaskTypeUnknown = "unknown"
	// StageUnknown is the label value used for a task whose frontmatter carries no
	// stage, for the same reason as TaskTypeUnknown.
	StageUnknown = "unknown"
	// PhaseUnknown is the label value used for a task whose frontmatter carries no
	// phase, for the same reason as TaskTypeUnknown.
	PhaseUnknown = "unknown"
	// JobRunning is the job_state value for a task carrying a non-empty
	// current_job. It means "a job was claimed", not "the job is alive": nothing
	// here polls the executor, so a job that died leaves its id behind and still
	// reads running.
	JobRunning = "running"
	// JobNone is the job_state value for a task with no current_job. It is a value
	// rather than an absent label because only a value is queryable: PromQL has no
	// positive matcher for label absence.
	JobNone = "none"
	// PRStateUnknown is the pr_state value for a task no sweep has observed yet. It
	// is deliberately distinct from PRStateNone (no PR at all): unknown means "ask
	// again after the next sweep", none means "this task will never have one".
	PRStateUnknown = "unknown"
	// AssigneeUnassigned is the assignee value for a task whose frontmatter names no
	// owner. Like JobNone it is a positive reading — nobody owns this task — not a
	// missing one, so it is a value rather than an absent label: only a value is
	// queryable, and PromQL has no positive matcher for label absence.
	//
	// Spelled "unassigned" rather than the "none" used by job_state and pr_state
	// because this label carries an identity, not an object: "none" in a column of
	// names reads as a person called none, while "unassigned" is also the word the
	// panel legend has to show to make the split legible.
	AssigneeUnassigned = "unassigned"
)

// OpenTaskKey identifies one vault_api_open_tasks series. Stage is the task's own
// frontmatter stage — the environment its work targets — not the stage of the
// vault-api instance serving it; that one is the constant instance_stage label.
// Phase is the task's normalized frontmatter phase: the index normalises through
// domain.NormalizeTaskPhase before storing it, so the label never carries both
// spellings of one phase (in_progress is reported as execution).
//
// JobState and PRState are the two labels the four-state split needs. JobState
// separates a task an agent is working from one merely marked open; PRState makes
// "the PR is already merged" provable from this metric alone.
//
// Assignee is the task's own frontmatter assignee, carried raw. It is what
// separates an owned record from an abandoned one at the same phase: without it
// two tasks in identical states are indistinguishable on the dashboard, and the
// unowned one — the one that needs a human — cannot be picked out.
type OpenTaskKey struct {
	Stage    string
	TaskType string
	Phase    string
	JobState string
	PRState  string
	Assignee string
}

// Metrics is the instrumentation the storage decorator and the index write to.
//
//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics
type Metrics interface {
	AddFileReads(kind string, count int64)
	SetOpenTasks(counts map[OpenTaskKey]int)
}

// NewMetrics creates the service metrics and registers them. The file-read
// counter is pre-initialised with Add(0) so its series exists from boot —
// otherwise rate() is empty until the first read, and a counter diff taken
// across an index-served query window would be ambiguous.
//
// instanceStage and vault are constant labels: both are fixed for the lifetime of
// the process, and vault keeps two instances of the same stage from merging their
// per-type counts into one series at query time. instanceStage is deliberately not
// called stage: that label carries each task's own frontmatter stage, which varies
// within one scrape.
func NewMetrics(instanceStage string, vault string) Metrics {
	m := &metrics{
		fileReads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vault_api_file_reads_total",
			Help: "Total number of vault files read from disk, by read kind.",
		}, []string{"kind"}),
		openTasks: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vault_api_open_tasks",
			Help: "Number of open tasks in the vault — status neither completed nor aborted — by the task's own frontmatter stage, by task_type, by phase, by whether a job is claimed (job_state), by the PR's last observed state (pr_state) and by the owner named in the task's frontmatter (assignee, with the sentinel \"unassigned\" for a task nobody owns). Unit: tasks.",
			ConstLabels: prometheus.Labels{
				"instance_stage": instanceStage,
				"vault":          vault,
			},
			// job_state, not job: the scrape config supplies its own `job` label
			// (kubernetes-pods), and an exporter label of the same name is renamed
			// to exported_job by the scrape — the series would read correctly on
			// /metrics and wrongly in Prometheus.
		}, []string{"stage", "task_type", "phase", "job_state", "pr_state", "assignee"}),
	}
	prometheus.MustRegister(m.fileReads, m.openTasks)
	for _, kind := range []string{FileReadKindList, FileReadKindRead} {
		m.fileReads.WithLabelValues(kind).Add(0)
	}
	return m
}

type metrics struct {
	fileReads *prometheus.CounterVec
	openTasks *prometheus.GaugeVec
}

func (m *metrics) AddFileReads(kind string, count int64) {
	if count <= 0 {
		return
	}
	m.fileReads.WithLabelValues(kind).Add(float64(count))
}

// SetOpenTasks replaces the whole series set. Resetting first is what drops a
// series that no longer has open tasks — a gauge that only Set() the keys it saw
// would leave the vanished one reporting its last value forever.
func (m *metrics) SetOpenTasks(counts map[OpenTaskKey]int) {
	m.openTasks.Reset()
	for key, count := range counts {
		m.openTasks.WithLabelValues(
			openTaskStage(key),
			openTaskType(key),
			openTaskPhase(key),
			openTaskJobState(key),
			openTaskPRState(key),
			openTaskAssignee(key),
		).Set(float64(count))
	}
}

// openTaskStage maps an absent stage onto StageUnknown so an unlabelled task still
// counts, mirroring openTaskType. A non-empty stage passes through verbatim: the
// population is operator-authored, and coercing an unexpected value would hide it.
func openTaskStage(key OpenTaskKey) string {
	if key.Stage == "" {
		return StageUnknown
	}
	return key.Stage
}

func openTaskType(key OpenTaskKey) string {
	if key.TaskType == "" {
		return TaskTypeUnknown
	}
	return key.TaskType
}

// openTaskJobState maps an absent job_state onto JobNone. Unlike the other
// sentinels this one is not "unknown": an empty current_job is a positive reading
// — no job is claimed — not a missing one.
func openTaskJobState(key OpenTaskKey) string {
	if key.JobState == "" {
		return JobNone
	}
	return key.JobState
}

// openTaskPRState maps an absent pr_state onto PRStateUnknown: a record the sweep
// has never written is unobserved, not PR-less. The sweep writes PRStateNone for a
// record with no PR, so the two never collapse.
func openTaskPRState(key OpenTaskKey) string {
	if key.PRState == "" {
		return PRStateUnknown
	}
	return key.PRState
}

// openTaskAssignee maps an absent assignee onto AssigneeUnassigned, mirroring
// openTaskJobState: an empty assignee is a positive reading — nobody owns this
// task — not a missing one. A non-empty assignee passes through verbatim; the
// population is operator-authored and bounded, and coercing an unexpected value
// would hide it.
func openTaskAssignee(key OpenTaskKey) string {
	if key.Assignee == "" {
		return AssigneeUnassigned
	}
	return key.Assignee
}

// openTaskPhase maps an absent phase onto PhaseUnknown, mirroring openTaskType. A
// non-empty phase passes through verbatim: it has already been normalised by the
// index, and coercing an unexpected value would hide it.
func openTaskPhase(key OpenTaskKey) string {
	if key.Phase == "" {
		return PhaseUnknown
	}
	return key.Phase
}
