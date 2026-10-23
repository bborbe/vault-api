// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rules_test

import (
	"errors"
	"fmt"
	"time"

	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/vault-api/pkg/rules"
)

// phaseCase is one ordered (from, to) pair of the phase enum with the verdict
// this test expects. Every verdict is written here as a literal — never read
// from the production graph — so this table can disagree with the
// implementation and fail.
type phaseCase struct {
	from  domain.TaskPhase
	to    domain.TaskPhase
	legal bool
	code  string
}

// statusCase is one ordered (from, to) pair of the status enum with the verdict
// this test expects, judged at a phase past todo.
type statusCase struct {
	from  domain.TaskStatus
	to    domain.TaskStatus
	legal bool
	code  string
}

var phaseCases = []phaseCase{
	{domain.TaskPhaseTodo, domain.TaskPhaseTodo, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseTodo, domain.TaskPhasePlanning, false, rules.CodeApprovalRequired},
	{domain.TaskPhaseTodo, domain.TaskPhaseExecution, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseTodo, domain.TaskPhaseAIReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseTodo, domain.TaskPhaseHumanReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseTodo, domain.TaskPhaseDone, false, rules.CodeInvalidTransition},

	{domain.TaskPhasePlanning, domain.TaskPhaseTodo, false, rules.CodeInvalidTransition},
	{domain.TaskPhasePlanning, domain.TaskPhasePlanning, false, rules.CodeInvalidTransition},
	{domain.TaskPhasePlanning, domain.TaskPhaseExecution, true, ""},
	{domain.TaskPhasePlanning, domain.TaskPhaseAIReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhasePlanning, domain.TaskPhaseHumanReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhasePlanning, domain.TaskPhaseDone, false, rules.CodeInvalidTransition},

	{domain.TaskPhaseExecution, domain.TaskPhaseTodo, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseExecution, domain.TaskPhasePlanning, true, ""},
	{domain.TaskPhaseExecution, domain.TaskPhaseExecution, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseExecution, domain.TaskPhaseAIReview, true, ""},
	{domain.TaskPhaseExecution, domain.TaskPhaseHumanReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseExecution, domain.TaskPhaseDone, false, rules.CodeInvalidTransition},

	{domain.TaskPhaseAIReview, domain.TaskPhaseTodo, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseAIReview, domain.TaskPhasePlanning, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseAIReview, domain.TaskPhaseExecution, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseAIReview, domain.TaskPhaseAIReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseAIReview, domain.TaskPhaseHumanReview, true, ""},
	{domain.TaskPhaseAIReview, domain.TaskPhaseDone, false, rules.CodeInvalidTransition},

	{domain.TaskPhaseHumanReview, domain.TaskPhaseTodo, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseHumanReview, domain.TaskPhasePlanning, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseHumanReview, domain.TaskPhaseExecution, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseHumanReview, domain.TaskPhaseAIReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseHumanReview, domain.TaskPhaseHumanReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseHumanReview, domain.TaskPhaseDone, true, ""},

	{domain.TaskPhaseDone, domain.TaskPhaseTodo, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseDone, domain.TaskPhasePlanning, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseDone, domain.TaskPhaseExecution, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseDone, domain.TaskPhaseAIReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseDone, domain.TaskPhaseHumanReview, false, rules.CodeInvalidTransition},
	{domain.TaskPhaseDone, domain.TaskPhaseDone, false, rules.CodeInvalidTransition},
}

var statusCases = []statusCase{
	{domain.TaskStatusNext, domain.TaskStatusNext, false, rules.CodeInvalidTransition},
	{domain.TaskStatusNext, domain.TaskStatusInProgress, true, ""},
	{domain.TaskStatusNext, domain.TaskStatusBacklog, false, rules.CodeInvalidTransition},
	{domain.TaskStatusNext, domain.TaskStatusCompleted, false, rules.CodeInvalidTransition},
	{domain.TaskStatusNext, domain.TaskStatusHold, false, rules.CodeInvalidTransition},
	{domain.TaskStatusNext, domain.TaskStatusAborted, false, rules.CodeInvalidTransition},

	{domain.TaskStatusInProgress, domain.TaskStatusNext, false, rules.CodeInvalidTransition},
	{domain.TaskStatusInProgress, domain.TaskStatusInProgress, false, rules.CodeInvalidTransition},
	{domain.TaskStatusInProgress, domain.TaskStatusBacklog, false, rules.CodeInvalidTransition},
	{domain.TaskStatusInProgress, domain.TaskStatusCompleted, true, ""},
	{domain.TaskStatusInProgress, domain.TaskStatusHold, true, ""},
	{domain.TaskStatusInProgress, domain.TaskStatusAborted, true, ""},

	{domain.TaskStatusBacklog, domain.TaskStatusNext, false, rules.CodeInvalidTransition},
	{domain.TaskStatusBacklog, domain.TaskStatusInProgress, false, rules.CodeInvalidTransition},
	{domain.TaskStatusBacklog, domain.TaskStatusBacklog, false, rules.CodeInvalidTransition},
	{domain.TaskStatusBacklog, domain.TaskStatusCompleted, false, rules.CodeInvalidTransition},
	{domain.TaskStatusBacklog, domain.TaskStatusHold, false, rules.CodeInvalidTransition},
	{domain.TaskStatusBacklog, domain.TaskStatusAborted, false, rules.CodeInvalidTransition},

	{domain.TaskStatusCompleted, domain.TaskStatusNext, false, rules.CodeInvalidTransition},
	{domain.TaskStatusCompleted, domain.TaskStatusInProgress, false, rules.CodeInvalidTransition},
	{domain.TaskStatusCompleted, domain.TaskStatusBacklog, false, rules.CodeInvalidTransition},
	{domain.TaskStatusCompleted, domain.TaskStatusCompleted, false, rules.CodeInvalidTransition},
	{domain.TaskStatusCompleted, domain.TaskStatusHold, false, rules.CodeInvalidTransition},
	{domain.TaskStatusCompleted, domain.TaskStatusAborted, false, rules.CodeInvalidTransition},

	{domain.TaskStatusHold, domain.TaskStatusNext, false, rules.CodeInvalidTransition},
	{domain.TaskStatusHold, domain.TaskStatusInProgress, true, ""},
	{domain.TaskStatusHold, domain.TaskStatusBacklog, false, rules.CodeInvalidTransition},
	{domain.TaskStatusHold, domain.TaskStatusCompleted, false, rules.CodeInvalidTransition},
	{domain.TaskStatusHold, domain.TaskStatusHold, false, rules.CodeInvalidTransition},
	{domain.TaskStatusHold, domain.TaskStatusAborted, false, rules.CodeInvalidTransition},

	{domain.TaskStatusAborted, domain.TaskStatusNext, false, rules.CodeInvalidTransition},
	{domain.TaskStatusAborted, domain.TaskStatusInProgress, false, rules.CodeInvalidTransition},
	{domain.TaskStatusAborted, domain.TaskStatusBacklog, false, rules.CodeInvalidTransition},
	{domain.TaskStatusAborted, domain.TaskStatusCompleted, false, rules.CodeInvalidTransition},
	{domain.TaskStatusAborted, domain.TaskStatusHold, false, rules.CodeInvalidTransition},
	{domain.TaskStatusAborted, domain.TaskStatusAborted, false, rules.CodeInvalidTransition},
}

// legalPhasePairs is the legal set asserted literally, so the table above and
// the graph must both agree with this list rather than with each other.
var legalPhasePairs = []string{
	"planning->execution",
	"execution->ai_review",
	"execution->planning",
	"ai_review->human_review",
	"human_review->done",
}

// legalStatusPairs is the legal set asserted literally.
var legalStatusPairs = []string{
	"next->in_progress",
	"in_progress->completed",
	"in_progress->hold",
	"in_progress->aborted",
	"hold->in_progress",
}

var _ = Describe("ValidatePhaseTransition", func() {
	DescribeTable("the full ordered phase matrix", phaseTable()...)

	It("carries one case per ordered pair of the phase enum", func() {
		Expect(phaseCases).To(HaveLen(36))
		Expect(len(phaseCases)).To(Equal(
			len(domain.AvailableTaskPhases) * len(domain.AvailableTaskPhases),
		))
	})

	It("allows exactly the five literal legal phase edges", func() {
		Expect(legalPairsOfPhases()).To(ConsistOf(legalPhasePairs))
	})

	It("names the approve endpoint as the only route out of the inbox", func() {
		err := rules.ValidatePhaseTransition(
			domain.TaskPhaseTodo, domain.TaskPhasePlanning,
		)
		var refusal *rules.RefusalError
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Allowed).To(Equal([]string{rules.ApproveEndpoint}))
	})

	It("lists the single legal target in an invalid transition's allowed set", func() {
		err := rules.ValidatePhaseTransition(
			domain.TaskPhasePlanning, domain.TaskPhaseDone,
		)
		var refusal *rules.RefusalError
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(rules.CodeInvalidTransition))
		Expect(refusal.From).To(Equal("planning"))
		Expect(refusal.To).To(Equal("done"))
		Expect(refusal.Allowed).To(Equal([]string{"execution"}))
	})
})

var _ = Describe("ValidateStatusTransition", func() {
	DescribeTable("the full ordered status matrix at phase execution", statusTable()...)

	It("carries one case per ordered pair of the status enum", func() {
		Expect(statusCases).To(HaveLen(36))
		Expect(len(statusCases)).To(Equal(
			len(domain.AvailableTaskStatuses) * len(domain.AvailableTaskStatuses),
		))
	})

	It("allows exactly the five literal legal status edges", func() {
		Expect(legalPairsOfStatuses()).To(ConsistOf(legalStatusPairs))
	})

	DescribeTable(
		"refuses in_progress while the task still sits in the approval inbox",
		func(phase domain.TaskPhase) {
			err := rules.ValidateStatusTransition(
				phase, domain.TaskStatusNext, domain.TaskStatusInProgress,
			)
			var refusal *rules.RefusalError
			Expect(errors.As(err, &refusal)).To(BeTrue())
			Expect(refusal.Code).To(Equal(rules.CodeApprovalRequired))
			Expect(refusal.Allowed).To(Equal([]string{rules.ApproveEndpoint}))
		},
		Entry("phase todo", domain.TaskPhaseTodo),
		Entry("phase unset", domain.TaskPhase("")),
	)

	It("allows in_progress once the phase is past todo", func() {
		Expect(rules.ValidateStatusTransition(
			domain.TaskPhasePlanning,
			domain.TaskStatusNext,
			domain.TaskStatusInProgress,
		)).To(BeNil())
	})
})

// approvalInstant is a fixed, non-zero instant. It is a package-level value
// rather than a BeforeEach assignment because table entries are built once, at
// tree construction, before any BeforeEach runs.
var approvalInstant = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

var _ = Describe("ValidateApprove", func() {
	It("accepts an inbox task with an approver and a real instant", func() {
		Expect(rules.ValidateApprove(
			"operator", domain.TaskPhaseTodo, false, approvalInstant,
		)).To(BeNil())
	})

	DescribeTable(
		"refuses with its named code",
		func(approvedBy string, phase domain.TaskPhase, hasRecord bool, at time.Time, code string) {
			err := rules.ValidateApprove(approvedBy, phase, hasRecord, at)
			var refusal *rules.RefusalError
			Expect(errors.As(err, &refusal)).To(BeTrue())
			Expect(refusal.Code).To(Equal(code))
		},
		Entry("blank approver", "", domain.TaskPhaseTodo, false, approvalInstant,
			rules.CodeBlankApprover),
		Entry("whitespace approver", "   ", domain.TaskPhaseTodo, false, approvalInstant,
			rules.CodeBlankApprover),
		Entry("zero instant", "operator", domain.TaskPhaseTodo, false, time.Time{},
			rules.CodeInvalidClock),
		Entry("phase planning", "operator", domain.TaskPhasePlanning, false, approvalInstant,
			rules.CodeNotApprovable),
		Entry("phase execution", "operator", domain.TaskPhaseExecution, false, approvalInstant,
			rules.CodeNotApprovable),
		Entry("existing record", "operator", domain.TaskPhaseTodo, true, approvalInstant,
			rules.CodeAlreadyApproved),
	)
})

var _ = Describe("RefuseApprovalRecordWrite", func() {
	It("names the approve endpoint as the only writer of the record", func() {
		var refusal *rules.RefusalError
		Expect(errors.As(rules.RefuseApprovalRecordWrite(), &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(rules.CodeApprovalIsAVerb))
		Expect(refusal.Allowed).To(Equal([]string{rules.ApproveEndpoint}))
	})
})

var _ = Describe("Unknown values", func() {
	It("names an unknown phase", func() {
		var refusal *rules.RefusalError
		Expect(errors.As(rules.UnknownPhase("shipped"), &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(rules.CodeUnknownPhase))
	})

	It("names an unknown status", func() {
		var refusal *rules.RefusalError
		Expect(errors.As(rules.UnknownStatus("paused"), &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(rules.CodeUnknownStatus))
	})
})

// phaseTable turns the literal phase table into named Ginkgo entries, so a
// failing case prints the pair it exercised.
func phaseTable() []any {
	args := make([]any, 0, len(phaseCases)+1)
	args = append(args, func(from, to domain.TaskPhase, legal bool, code string) {
		err := rules.ValidatePhaseTransition(from, to)
		if legal {
			Expect(err).To(BeNil())
			return
		}
		Expect(err).NotTo(BeNil())
		var refusal *rules.RefusalError
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(code))
		Expect(refusal.From).To(Equal(string(from)))
		Expect(refusal.To).To(Equal(string(to)))
	})
	for _, c := range phaseCases {
		args = append(args, Entry(
			fmt.Sprintf("%s -> %s", c.from, c.to),
			c.from, c.to, c.legal, c.code,
		))
	}
	return args
}

// statusTable turns the literal status table into named Ginkgo entries.
func statusTable() []any {
	args := make([]any, 0, len(statusCases)+1)
	args = append(args, func(from, to domain.TaskStatus, legal bool, code string) {
		err := rules.ValidateStatusTransition(domain.TaskPhaseExecution, from, to)
		if legal {
			Expect(err).To(BeNil())
			return
		}
		Expect(err).NotTo(BeNil())
		var refusal *rules.RefusalError
		Expect(errors.As(err, &refusal)).To(BeTrue())
		Expect(refusal.Code).To(Equal(code))
	})
	for _, c := range statusCases {
		args = append(args, Entry(
			fmt.Sprintf("%s -> %s", c.from, c.to),
			c.from, c.to, c.legal, c.code,
		))
	}
	return args
}

func legalPairsOfPhases() []string {
	out := make([]string, 0, len(legalPhasePairs))
	for _, c := range phaseCases {
		if c.legal {
			out = append(out, fmt.Sprintf("%s->%s", c.from, c.to))
		}
	}
	return out
}

func legalPairsOfStatuses() []string {
	out := make([]string, 0, len(legalStatusPairs))
	for _, c := range statusCases {
		if c.legal {
			out = append(out, fmt.Sprintf("%s->%s", c.from, c.to))
		}
	}
	return out
}
