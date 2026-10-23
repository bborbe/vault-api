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

// TaskTypeUnknown is the label value used for a task whose frontmatter carries
// no task_type. It keeps the gauge's total equal to the real open-task count
// instead of silently dropping untyped pages.
const TaskTypeUnknown = "unknown"

// Metrics is the instrumentation the storage decorator and the index write to.
//
//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics
type Metrics interface {
	AddFileReads(kind string, count int64)
	SetOpenTasks(counts map[string]int)
}

// NewMetrics creates the service metrics and registers them. The file-read
// counter is pre-initialised with Add(0) so its series exists from boot —
// otherwise rate() is empty until the first read, and a counter diff taken
// across an index-served query window would be ambiguous.
//
// stage and vault are constant labels: both are fixed for the lifetime of the
// process, and vault keeps two instances of the same stage from merging their
// per-type counts into one series at query time.
func NewMetrics(stage string, vault string) Metrics {
	return newMetrics(prometheus.DefaultRegisterer, stage, vault)
}

// newMetrics builds the metrics against an explicit registerer so tests can use
// a private registry instead of the process-global one. It returns the concrete
// type so callers that need the unexported vecs (tests) need no assertion.
func newMetrics(reg prometheus.Registerer, stage string, vault string) *metrics {
	m := &metrics{
		fileReads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vault_api_file_reads_total",
			Help: "Total number of vault files read from disk, by read kind.",
		}, []string{"kind"}),
		openTasks: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "vault_api_open_tasks",
			Help: "Number of open tasks in the vault — status neither completed nor aborted — by task_type. Unit: tasks.",
			ConstLabels: prometheus.Labels{
				"stage": stage,
				"vault": vault,
			},
		}, []string{"task_type"}),
	}
	reg.MustRegister(m.fileReads, m.openTasks)
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

// SetOpenTasks replaces the whole per-type series set. Resetting first is what
// drops a task_type that no longer has open tasks — a gauge that only Set() the
// types it saw would leave the vanished one reporting its last value forever.
func (m *metrics) SetOpenTasks(counts map[string]int) {
	m.openTasks.Reset()
	for taskType, count := range counts {
		if taskType == "" {
			taskType = TaskTypeUnknown
		}
		m.openTasks.WithLabelValues(taskType).Set(float64(count))
	}
}
