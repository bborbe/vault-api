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
)

// OpenTaskKey identifies one vault_api_open_tasks series. Stage is the task's own
// frontmatter stage — the environment its work targets — not the stage of the
// vault-api instance serving it; that one is the constant instance_stage label.
type OpenTaskKey struct {
	Stage    string
	TaskType string
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
			Help: "Number of open tasks in the vault — status neither completed nor aborted — by the task's own frontmatter stage and by task_type. Unit: tasks.",
			ConstLabels: prometheus.Labels{
				"instance_stage": instanceStage,
				"vault":          vault,
			},
		}, []string{"stage", "task_type"}),
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

// SetOpenTasks replaces the whole (stage, task_type) series set. Resetting first
// is what drops a series that no longer has open tasks — a gauge that only Set()
// the keys it saw would leave the vanished one reporting its last value forever.
func (m *metrics) SetOpenTasks(counts map[OpenTaskKey]int) {
	m.openTasks.Reset()
	for key, count := range counts {
		m.openTasks.WithLabelValues(openTaskStage(key), openTaskType(key)).Set(float64(count))
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
