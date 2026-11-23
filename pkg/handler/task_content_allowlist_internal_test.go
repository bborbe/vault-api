// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"sort"

	lib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// controllerEmittedFrontmatterKeys is every frontmatter key agent-task-controller
// and agent-task-executor write on a path that reaches PatchTask, enumerated
// from their source rather than from a log.
//
// ⚠️ This list, not the log, is the input. `fieldRefusal` names ONE refused key
// per attempt and Go map iteration is random, so an outage that refuses several
// keys reports a different one on every retry. Widening by the field a log line
// names fixes a fraction of the class and ships the next outage late.
//
// The enumeration, by producer:
//
//	executor PublishSpawnNotification:      spawn_notification, current_job,
//	                                        job_started_at, claude_session_id,
//	                                        last_result_status
//	executor PublishClearCurrentJob:        current_job
//	executor PublishSetTriggerScope:        trigger_scope, trigger_count
//	executor PublishIncrementTriggerCount:  trigger_count
//	executor crashFailureUpdates:           current_job, max_triggers
//	executor PublishTypeMismatchFailure:    assignee, current_job, previous_assignee
//	executor PublishRestoreAssignee:        assignee
//	executor PublishParkLostClaim:          phase
//	executor PublishAbort:                  status, aborted_reason, gate_successor,
//	                                        current_job
//	executor PublishQueueEntry:             queued_at
//	executor probe create/update:           task_type, status, phase, assignee,
//	                                        stage, trigger_count, current_job,
//	                                        job_started_at, spawn_notification
//	controller buildCompletePatch:          status, phase, completed_date,
//	                                        processed_at, recovery_sha, target_vault
//	controller transitionPriorTyped:        status, phase, completed_date,
//	                                        superseded_by, created_by
//	controller buildUpdatePatch doctrines:  previous_assignee, max_triggers,
//	                                        assignee, target_vault, current_job,
//	                                        status, phase
//	controller result writer:               run_id, metrics_runs
//
// The guarantee is one-way: a key added to this list or to agentPipelineFields
// without the other fails the build. A producer that starts writing a NEW key
// cannot be detected from here — it lives in another repo — so transcribing it
// into this list stays a hand step, taken from the producer's source.
var controllerEmittedFrontmatterKeys = []string{
	"aborted_reason",
	"assignee",
	"claude_session_id",
	"completed_date",
	"created_by",
	"current_job",
	"gate_successor",
	"job_started_at",
	"last_result_status",
	"max_triggers",
	"metrics_runs",
	"phase",
	"previous_assignee",
	"processed_at",
	"queued_at",
	"recovery_sha",
	"run_id",
	"spawn_notification",
	"stage",
	"status",
	"superseded_by",
	"target_vault",
	"task_type",
	"trigger_count",
	"trigger_scope",
}

// otherProducerFields are the allowlist's remaining entries, written by the
// watchers, the recurring templates and the pr-override agent rather than by the
// controller/executor. They are recorded here so the attribution assertion can
// account for every key; their own pinning test lives in the external package
// (task_content_test.go), which exercises each one through a real PATCH.
var otherProducerFields = map[string]bool{
	"auto_abort_prior": true, // recurring-task-creator's supersede flag
	"base_ref":         true, // github-pr-watcher
	"branch":           true, // github-dark-factory-watcher
	"clone_url":        true, // github-pr-watcher
	"current_go":       true, // github-update-go-watcher
	"current_version":  true, // the release watcher
	"date":             true, // the sentry producers
	"episode_sha":      true, // github-build-watcher
	"failure_reason":   true, // github-pr-review-agent stampRunOutcome
	"issue_url":        true, // sentry-watcher
	"latest_go":        true, // github-update-go-watcher
	"launcher":         true, // recurring-task-creator
	// created is stamped by recurring-task-creator's AI Knowledge Sharing
	// template. Measured 2026-11-22 on agent-task-controller-seibert-personal-0:
	// every create answered 400 unknown_field field=created and was dropped
	// whole, so the weekly instance never materialized.
	"created": true, // recurring-task-creator, AI Knowledge Sharing template
	// max_retries is carried, not written, by the controller's result writer: a
	// result PATCH is a read-modify-write, so the operator-set value rides along
	// in the payload and refusing it refused the whole write.
	"max_retries":       true,
	"new_version":       true, // go-version-watcher
	"override_reason":   true, // the pr-override agent
	"pr_number":         true, // github-dark-factory-watcher
	"previous_version":  true, // go-version-watcher
	"project":           true, // sentry-watcher
	"ref":               true, // the release watcher
	"release_kind":      true, // go-version-watcher
	"release_notes_url": true, // go-version-watcher
	"repo":              true, // the release watcher
	"retry_count":       true, // the controller's result writer, on the git path
	"run_outcome":       true, // github-pr-review-agent stampRunOutcome
	"short_id":          true, // sentry-watcher
	"title":             true, // every watcher's create
	"vuln_count":        true, // github-vuln-watcher
	"vulns":             true, // github-vuln-watcher
}

var _ = Describe("agent pipeline frontmatter allowlist", func() {
	It("admits every key the controller and executor emit", func() {
		seen := map[string]bool{}
		for _, key := range controllerEmittedFrontmatterKeys {
			Expect(seen).NotTo(HaveKey(key), "listed twice: %s", key)
			seen[key] = true
			Expect(lib.TaskPatchFieldAdmitted(key)).To(
				BeTrue(),
				"the controller/executor write %q but the task PATCH rule refuses it: "+
					"every patch carrying it answers 400 unknown_field and is dropped whole",
				key,
			)
		}
	})

	It("records a producer for every pipeline-only key", func() {
		attributed := map[string]bool{}
		for _, key := range controllerEmittedFrontmatterKeys {
			attributed[key] = true
		}
		for key := range otherProducerFields {
			attributed[key] = true
			// A key attributed only here must still be writable: otherwise the
			// attribution silences the check above while the key answers 400.
			Expect(lib.TaskPatchFieldAdmitted(key)).To(
				BeTrue(),
				"otherProducerFields records %q but the task PATCH rule refuses it",
				key,
			)
		}
		keys := make([]string, 0, len(agentPipelineFields))
		for key := range agentPipelineFields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			Expect(attributed).To(
				HaveKey(key),
				"agentPipelineFields admits %q but records no producer for it: "+
					"add it to controllerEmittedFrontmatterKeys or otherProducerFields",
				key,
			)
		}
	})
})
