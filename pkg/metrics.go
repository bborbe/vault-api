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

// Metrics is the instrumentation the storage decorator writes to.
//
//counterfeiter:generate -o ../mocks/metrics.go --fake-name Metrics . Metrics
type Metrics interface {
	AddFileReads(kind string, count int64)
}

// NewMetrics creates the service metrics and registers them. The file-read
// counter is pre-initialised with Add(0) so its series exists from boot —
// otherwise rate() is empty until the first read, and a counter diff taken
// across an index-served query window would be ambiguous.
func NewMetrics() Metrics {
	m := &metrics{
		fileReads: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "vault_api_file_reads_total",
			Help: "Total number of vault files read from disk, by read kind.",
		}, []string{"kind"}),
	}
	prometheus.MustRegister(m.fileReads)
	for _, kind := range []string{FileReadKindList, FileReadKindRead} {
		m.fileReads.WithLabelValues(kind).Add(0)
	}
	return m
}

type metrics struct {
	fileReads *prometheus.CounterVec
}

func (m *metrics) AddFileReads(kind string, count int64) {
	if count <= 0 {
		return
	}
	m.fileReads.WithLabelValues(kind).Add(float64(count))
}
