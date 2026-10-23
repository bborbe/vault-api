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
	value float64
	stage string
	vault string
}

var _ = Describe("Metrics", func() {
	It("labels the open-task gauge and drops a task_type that has no open tasks", func() {
		metrics := vaultapi.NewMetrics("dev", "private-agent")

		metrics.SetOpenTasks(map[string]int{"pr-review": 3, "": 2})

		samples := gatherOpenTasks()
		Expect(samples).To(HaveKey("pr-review"))
		Expect(samples).To(HaveKey(vaultapi.TaskTypeUnknown))
		Expect(samples["pr-review"].value).To(Equal(3.0))
		Expect(samples[vaultapi.TaskTypeUnknown].value).To(Equal(2.0))
		for taskType, sample := range samples {
			Expect(sample.stage).To(Equal("dev"), "stage of %q", taskType)
			Expect(sample.vault).To(Equal("private-agent"), "vault of %q", taskType)
		}

		// Reset before set: a type that loses its last open task must not keep
		// reporting its previous value.
		metrics.SetOpenTasks(map[string]int{"pr-review": 1})

		samples = gatherOpenTasks()
		Expect(samples).To(HaveLen(1))
		Expect(samples["pr-review"].value).To(Equal(1.0))
	})
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
