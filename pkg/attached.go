// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

// NewNoopMetrics returns metrics that record nothing.
//
// Attached vaults use it. The service's gauges carry the primary vault as a
// constant label, so an attached vault's index build must not publish through
// them: Build calls SetOpenTasks with that vault's counts, which would overwrite
// the primary vault's open-task series with another vault's numbers.
func NewNoopMetrics() Metrics {
	return noopMetrics{}
}

type noopMetrics struct{}

func (noopMetrics) AddFileReads(string, int64) {}

func (noopMetrics) SetOpenTasks(map[OpenTaskKey]int) {}

// UnavailableLine is the log token that names a configured vault whose index
// could not be built. It is a fixed shape so an operator and a probe can grep it:
// an attached vault that fails silently would make its rows vanish from every
// list query, which reads exactly like "that vault has nothing flagged".
func UnavailableLine(vault string) string {
	return "vault=" + vault + " rows_source=unavailable"
}
