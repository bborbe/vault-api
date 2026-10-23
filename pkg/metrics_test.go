// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestSetOpenTasksLabelsAndReset covers what the index fake cannot: that the
// real gauge carries the stage/vault constant labels, maps an empty task_type
// to the unknown bucket, and drops a task_type that no longer has open tasks.
func TestSetOpenTasksLabelsAndReset(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := newMetrics(reg, "dev", "private-agent")

	m.SetOpenTasks(map[string]int{"pr-review": 3, "": 2})

	got := gatherOpenTasks(t, reg)
	want := map[string]float64{"pr-review": 3, TaskTypeUnknown: 2}
	assertOpenTasks(t, got, want)

	m.SetOpenTasks(map[string]int{"pr-review": 1})

	got = gatherOpenTasks(t, reg)
	want = map[string]float64{"pr-review": 1}
	assertOpenTasks(t, got, want)
}

type openTaskSample struct {
	value float64
	stage string
	vault string
}

func gatherOpenTasks(t *testing.T, reg *prometheus.Registry) map[string]openTaskSample {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	samples := map[string]openTaskSample{}
	for _, family := range families {
		if family.GetName() != "vault_api_open_tasks" {
			continue
		}
		for _, metric := range family.GetMetric() {
			sample := openTaskSample{value: metric.GetGauge().GetValue()}
			taskType := ""
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case "task_type":
					taskType = label.GetValue()
				case "stage":
					sample.stage = label.GetValue()
				case "vault":
					sample.vault = label.GetValue()
				}
			}
			samples[taskType] = sample
		}
	}
	return samples
}

func assertOpenTasks(t *testing.T, got map[string]openTaskSample, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("series count = %d, want %d (got %v)", len(got), len(want), got)
	}
	for taskType, wantValue := range want {
		sample, ok := got[taskType]
		if !ok {
			t.Fatalf("missing task_type %q in %v", taskType, got)
		}
		if sample.value != wantValue {
			t.Errorf("task_type %q = %v, want %v", taskType, sample.value, wantValue)
		}
		if sample.stage != "dev" {
			t.Errorf("task_type %q stage = %q, want %q", taskType, sample.stage, "dev")
		}
		if sample.vault != "private-agent" {
			t.Errorf("task_type %q vault = %q, want %q", taskType, sample.vault, "private-agent")
		}
	}
}
