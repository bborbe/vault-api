// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

// openTaskSample is one gathered vault_api_open_tasks series.
type openTaskSample struct {
	value         float64
	stage         string
	instanceStage string
	taskType      string
	vault         string
}

// seriesKey renders the two variable labels so a test can address one series.
func seriesKey(stage string, taskType string) string {
	return stage + "/" + taskType
}

var _ = Describe("Metrics", func() {
	It(
		"labels the open-task gauge by the task's stage and keeps the instance stage constant",
		func() {
			metrics := vaultapi.NewMetrics("prod", "private-agent")

			metrics.SetOpenTasks(map[vaultapi.OpenTaskKey]int{
				{Stage: "prod", TaskType: "pr-review"}:  3,
				{Stage: "dev", TaskType: "pr-review"}:   1,
				{Stage: "", TaskType: "github-release"}: 2,
				{Stage: "dev", TaskType: ""}:            4,
			})

			samples := gatherOpenTasks()
			Expect(samples).To(HaveLen(4))
			Expect(samples[seriesKey("prod", "pr-review")].value).To(Equal(3.0))
			Expect(samples[seriesKey("dev", "pr-review")].value).To(Equal(1.0))
			// An absent stage and an absent task_type each land in their own unknown bucket.
			Expect(samples[seriesKey(vaultapi.StageUnknown, "github-release")].value).To(Equal(2.0))
			Expect(samples[seriesKey("dev", vaultapi.TaskTypeUnknown)].value).To(Equal(4.0))

			for key, sample := range samples {
				Expect(sample.instanceStage).To(Equal("prod"), "instance_stage of %q", key)
				Expect(sample.vault).To(Equal("private-agent"), "vault of %q", key)
			}

			// Reset before set: a series that loses its last open task must not keep
			// reporting its previous value.
			metrics.SetOpenTasks(map[vaultapi.OpenTaskKey]int{
				{Stage: "prod", TaskType: "pr-review"}: 1,
			})

			samples = gatherOpenTasks()
			Expect(samples).To(HaveLen(1))
			Expect(samples[seriesKey("prod", "pr-review")].value).To(Equal(1.0))
		},
	)
})

func gatherOpenTasks() map[string]openTaskSample {
	families, err := prometheus.DefaultGatherer.Gather()
	Expect(err).To(BeNil())
	samples := map[string]openTaskSample{}
	for _, family := range families {
		if family.GetName() != "vault_api_open_tasks" {
			continue
		}
		for _, metric := range family.GetMetric() {
			sample := openTaskSample{value: metric.GetGauge().GetValue()}
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case "task_type":
					sample.taskType = label.GetValue()
				case "stage":
					sample.stage = label.GetValue()
				case "instance_stage":
					sample.instanceStage = label.GetValue()
				case "vault":
					sample.vault = label.GetValue()
				}
			}
			samples[seriesKey(sample.stage, sample.taskType)] = sample
		}
	}
	return samples
}
