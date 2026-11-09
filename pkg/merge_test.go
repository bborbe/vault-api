// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var (
	earlier = time.Date(2026, 10, 21, 19, 0, 0, 0, time.UTC)
	later   = time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)
)

// mergeFixture is the reconstructed `PR Review … claude#138` conflict: the
// remote side changed `current_job`, the local side added `cluster: sun` from a
// base that had neither edit.
const (
	fixtureBase = `---
current_job: pr-reviewer-agent-dbfb48c8-20261021185316
trigger_count: 1
---
Body stays identical.
`
	fixtureOurs = `---
current_job: pr-reviewer-agent-dbfb48c8-20261021185316
trigger_count: 1
cluster: sun
---
Body stays identical.
`
	fixtureTheirs = `---
current_job: pr-reviewer-agent-dbfb48c8-20261021190000
trigger_count: 1
---
Body stays identical.
`
	// fixtureExpected names the merged output: both keys present, `cluster: sun`
	// preserved, `current_job` holding the newer side's value.
	fixtureExpected = `---
current_job: pr-reviewer-agent-dbfb48c8-20261021190000
trigger_count: 1
cluster: sun
---
Body stays identical.
`
)

var _ = Describe("MergeFile", func() {
	// frontmatter parses a merged result back into a key map.
	frontmatter := func(content []byte) map[string]string {
		parsed, ok := parseFile(content)
		Expect(ok).To(BeTrue())
		values := map[string]string{}
		for key, node := range parsed.values {
			values[key] = node.Value
		}
		return values
	}

	It("keeps a key only one side added", func() {
		result := MergeFile(
			Side{Content: []byte("---\ncurrent_job: a\n---\nbody\n"), CommitterTime: earlier},
			Side{
				Content:       []byte("---\ncurrent_job: a\ncluster: sun\n---\nbody\n"),
				CommitterTime: earlier,
			},
			Side{Content: []byte("---\ncurrent_job: b\n---\nbody\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeFalse())
		Expect(frontmatter(result.Content)).To(Equal(map[string]string{
			"current_job": "b",
			"cluster":     "sun",
		}))
	})

	It("takes the later committer side for an assignment field", func() {
		result := MergeFile(
			Side{Content: []byte("---\ncluster: base\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncluster: dev\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncluster: sun\n---\nb\n"), CommitterTime: later},
		)

		Expect(frontmatter(result.Content)["cluster"]).To(Equal("sun"))
	})

	It("resolves a committer-time tie to ours", func() {
		result := MergeFile(
			Side{Content: []byte("---\nassignee: base\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\nassignee: ours\n---\nb\n"), CommitterTime: later},
			Side{Content: []byte("---\nassignee: theirs\n---\nb\n"), CommitterTime: later},
		)

		Expect(frontmatter(result.Content)["assignee"]).To(Equal("ours"))
	})

	// listField reads a merged list back as its items. A sequence node carries
	// no scalar value, so the frontmatter map's node.Value is empty for it.
	listField := func(content []byte, key string) []string {
		parsed, ok := parseFile(content)
		Expect(ok).To(BeTrue())
		items, ok := listValue(parsed.values[key])
		Expect(ok).To(BeTrue())
		return items
	}

	It("keeps the larger counter", func() {
		result := MergeFile(
			Side{Content: []byte("---\ntrigger_count: 1\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ntrigger_count: 2\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ntrigger_count: 4\n---\nb\n"), CommitterTime: later},
		)

		Expect(frontmatter(result.Content)["trigger_count"]).To(Equal("4"))
	})

	It("unions an append-only list, ours first, duplicates removed", func() {
		result := MergeFile(
			Side{Content: []byte("---\ngoals: [a]\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ngoals: [a, b]\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ngoals: [a, c]\n---\nb\n"), CommitterTime: later},
		)

		Expect(listField(result.Content, "goals")).To(Equal([]string{"a", "b", "c"}))
	})

	It("concatenates metrics_sessions, keeping duplicates", func() {
		result := MergeFile(
			Side{Content: []byte("---\nmetrics_sessions: [a]\n---\nb\n"), CommitterTime: earlier},
			Side{
				Content:       []byte("---\nmetrics_sessions: [a, b]\n---\nb\n"),
				CommitterTime: earlier,
			},
			Side{Content: []byte("---\nmetrics_sessions: [a, c]\n---\nb\n"), CommitterTime: later},
		)

		Expect(
			listField(result.Content, "metrics_sessions"),
		).To(Equal([]string{"a", "b", "a", "c"}))
	})

	It("accepts the one legal lifecycle transition", func() {
		result := MergeFile(
			Side{Content: []byte("---\nstatus: next\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\nstatus: next\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\nstatus: in_progress\n---\nb\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeFalse())
		Expect(frontmatter(result.Content)["status"]).To(Equal("in_progress"))
	})

	It("quarantines two different legal lifecycle transitions", func() {
		result := MergeFile(
			Side{Content: []byte("---\nstatus: in_progress\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\nstatus: completed\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\nstatus: hold\n---\nb\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeTrue())
		Expect(result.Reason).To(Equal(ReasonLifecycle))
	})

	It("takes a one-sided body change", func() {
		result := MergeFile(
			Side{Content: []byte("---\ncluster: sun\n---\nbase\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncluster: sun\n---\nbase\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncluster: sun\n---\ntheirs\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeFalse())
		Expect(string(result.Content)).To(ContainSubstring("theirs"))
	})

	It("quarantines a body both sides changed differently", func() {
		result := MergeFile(
			Side{Content: []byte("---\ncluster: sun\n---\nbase\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncluster: sun\n---\nours\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncluster: sun\n---\ntheirs\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeTrue())
		Expect(result.Reason).To(Equal(ReasonBodyConflict))
	})

	// bodyOf returns the merged body, so a spec asserts on the body without
	// depending on how render formats the frontmatter it rebuilds.
	bodyOf := func(content []byte) string {
		parsed, ok := parseFile(content)
		Expect(ok).To(BeTrue())
		return parsed.body
	}

	const (
		bodyBase  = "## Notes\n\nseeded\n"
		bodyPlan  = "## Plan\n\n{\"pr_url\": \"x\"}\n"
		bodyResol = "## Resolution\n\n- **Verdict:** merged\n"
		fmPrefix  = "---\ncluster: sun\n---\n"
	)

	Context("a body both sides appended to after a common base", func() {
		It("keeps both appends, ordered by committer time rather than byte order", func() {
			// CommitterTime order is deliberately the REVERSE of byte order:
			// "## Plan" sorts before "## Resolution", so a lexicographic sort
			// yields Plan-then-Resolution and fails here.
			result := MergeFile(
				Side{Content: []byte(fmPrefix + bodyBase), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyResol), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyPlan), CommitterTime: later},
			)

			Expect(result.Quarantine).To(BeFalse())
			Expect(bodyOf(result.Content)).To(Equal(bodyBase + bodyResol + bodyPlan))
		})

		It("is orientation-independent", func() {
			// Two nodes merging one divergence see opposite ours/theirs. They
			// must write identical bytes or they re-quarantine on the next sync.
			forward := MergeFile(
				Side{Content: []byte(fmPrefix + bodyBase), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyResol), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyPlan), CommitterTime: later},
			)
			swapped := MergeFile(
				Side{Content: []byte(fmPrefix + bodyBase), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyPlan), CommitterTime: later},
				Side{Content: []byte(fmPrefix + bodyBase + bodyResol), CommitterTime: earlier},
			)

			Expect(forward.Quarantine).To(BeFalse())
			Expect(swapped.Quarantine).To(BeFalse())
			Expect(bodyOf(swapped.Content)).To(Equal(bodyOf(forward.Content)))
		})

		It("breaks a committer-time tie by suffix bytes, ascending", func() {
			forward := MergeFile(
				Side{Content: []byte(fmPrefix + bodyBase), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyResol), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyPlan), CommitterTime: earlier},
			)

			Expect(forward.Quarantine).To(BeFalse())
			Expect(bodyOf(forward.Content)).To(Equal(bodyBase + bodyPlan + bodyResol))

			// The tie-break is the branch most at risk of being
			// orientation-dependent, so it is checked swapped as well.
			swapped := MergeFile(
				Side{Content: []byte(fmPrefix + bodyBase), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyPlan), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyResol), CommitterTime: earlier},
			)

			Expect(swapped.Quarantine).To(BeFalse())
			Expect(bodyOf(swapped.Content)).To(Equal(bodyOf(forward.Content)))
		})

		It("does not double a section both sides appended", func() {
			// The longer suffix is deliberately the EARLIER commit, so a
			// "later side wins" rule would drop bodyResol and fail: only
			// "longer side wins outright" satisfies this.
			result := MergeFile(
				Side{Content: []byte(fmPrefix + bodyBase), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + bodyBase + bodyPlan), CommitterTime: later},
				Side{
					Content:       []byte(fmPrefix + bodyBase + bodyPlan + bodyResol),
					CommitterTime: earlier,
				},
			)

			Expect(result.Quarantine).To(BeFalse())
			Expect(bodyOf(result.Content)).To(Equal(bodyBase + bodyPlan + bodyResol))
		})

		It("separates two appends when the first has no trailing newline", func() {
			// A body need not end in a newline, and the suffixes are joined
			// raw, so without a separator the two sections fuse into one line.
			result := MergeFile(
				Side{Content: []byte(fmPrefix + "line\n"), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + "line\ntail A"), CommitterTime: earlier},
				Side{Content: []byte(fmPrefix + "line\ntail B"), CommitterTime: later},
			)

			Expect(result.Quarantine).To(BeFalse())
			Expect(bodyOf(result.Content)).To(Equal("line\ntail A\ntail B"))
		})

		It("quarantines a delete against an edit", func() {
			// ours deletes back to a prefix of base; theirs edits. One side is
			// a prefix of the other, but base is not a common prefix of both,
			// so the longer-side rule must not fire and resolve it silently.
			result := MergeFile(
				Side{
					Content:       []byte(fmPrefix + "## Notes\n\nseeded\nmore\n"),
					CommitterTime: earlier,
				},
				Side{Content: []byte(fmPrefix + "## Notes\n\nseeded\n"), CommitterTime: earlier},
				Side{
					Content:       []byte(fmPrefix + "## Notes\n\nseeded\nedited\n"),
					CommitterTime: later,
				},
			)

			Expect(result.Quarantine).To(BeTrue())
			Expect(result.Reason).To(Equal(ReasonBodyConflict))
		})
	})

	It("lets a lifecycle contradiction win over a mergeable body", func() {
		// The bodies are disjoint appends and would merge; the status values
		// are contradictory, so mergeKeys must quarantine before mergeBody runs.
		result := MergeFile(
			Side{
				Content:       []byte("---\nstatus: in_progress\n---\n" + bodyBase),
				CommitterTime: earlier,
			},
			Side{
				Content:       []byte("---\nstatus: completed\n---\n" + bodyBase + bodyResol),
				CommitterTime: earlier,
			},
			Side{
				Content:       []byte("---\nstatus: aborted\n---\n" + bodyBase + bodyPlan),
				CommitterTime: later,
			},
		)

		Expect(result.Quarantine).To(BeTrue())
		Expect(result.Reason).To(Equal(ReasonLifecycle))
	})

	It("quarantines a side whose frontmatter does not parse", func() {
		result := MergeFile(
			Side{Content: []byte("---\ncurrent_job: a\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncurrent_job: [unclosed\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ncurrent_job: b\n---\nb\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeTrue())
		Expect(result.Reason).To(Equal(ReasonUnparsable))
	})

	// This drives the entrypoint a pull path calls: the diverged claude#138
	// fixture in, the named expected file out. A merge function the pull path
	// never calls cannot satisfy this spec.
	Describe("the pull entrypoint over the claude#138 fixture", func() {
		It("returns exactly the expected merged file", func() {
			result := MergeFile(
				Side{Content: []byte(fixtureBase), CommitterTime: earlier},
				Side{Content: []byte(fixtureOurs), CommitterTime: earlier},
				Side{Content: []byte(fixtureTheirs), CommitterTime: later},
			)

			Expect(result.Quarantine).To(BeFalse())
			// Exact bytes, not just the parsed map: a key-order or formatting
			// regression in render passes a map comparison.
			Expect(string(result.Content)).To(Equal(fixtureExpected))
			Expect(frontmatter(result.Content)).To(Equal(frontmatter([]byte(fixtureExpected))))

			var merged map[string]any
			Expect(yaml.Unmarshal(result.Content, &merged)).To(Succeed())
			Expect(merged["cluster"]).To(Equal("sun"))
			Expect(merged["current_job"]).To(Equal("pr-reviewer-agent-dbfb48c8-20261021190000"))
		})
	})

	It("keeps the base key order and appends a key a side added", func() {
		result := MergeFile(
			Side{Content: []byte("---\nzebra: 1\nalpha: 2\n---\nb\n"), CommitterTime: earlier},
			Side{
				Content:       []byte("---\nzebra: 1\nalpha: 2\nadded: 3\n---\nb\n"),
				CommitterTime: earlier,
			},
			Side{Content: []byte("---\nzebra: 1\nalpha: 2\n---\nb\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeFalse())
		Expect(string(result.Content)).To(Equal("---\nzebra: 1\nalpha: 2\nadded: 3\n---\nb\n"))
	})

	It("treats a key the winning side deleted as a deletion, not a nil node", func() {
		result := MergeFile(
			Side{Content: []byte("---\ncluster: dev\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\n---\nb\n"), CommitterTime: later},
			Side{Content: []byte("---\ncluster: sun\n---\nb\n"), CommitterTime: earlier},
		)

		Expect(result.Quarantine).To(BeFalse())
		// Every key is gone, so the file renders as its body alone; an empty
		// frontmatter block is not preserved.
		Expect(string(result.Content)).To(Equal("b\n"))
	})

	It("quarantines a list one side deleted and the other changed", func() {
		result := MergeFile(
			Side{Content: []byte("---\ngoals: [a]\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ngoals: [a, c]\n---\nb\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeTrue())
		Expect(result.Reason).To(Equal(ReasonListConflict))
	})

	It("names a counter failure as a counter, not a lifecycle problem", func() {
		result := MergeFile(
			Side{Content: []byte("---\ntrigger_count: 1\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ntrigger_count: two\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\ntrigger_count: 4\n---\nb\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeTrue())
		Expect(result.Reason).To(Equal(ReasonCounter))
	})

	It("quarantines an unparsable base or theirs side", func() {
		broken := []byte("---\n[unclosed\n---\nb\n")

		Expect(MergeFile(
			Side{Content: broken, CommitterTime: earlier},
			Side{Content: []byte("---\na: 1\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\na: 2\n---\nb\n"), CommitterTime: later},
		).Reason).To(Equal(ReasonUnparsable))

		Expect(MergeFile(
			Side{Content: []byte("---\na: 1\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\na: 1\n---\nb\n"), CommitterTime: earlier},
			Side{Content: broken, CommitterTime: later},
		).Reason).To(Equal(ReasonUnparsable))
	})

	It("merges a file with no frontmatter as a pure body comparison", func() {
		result := MergeFile(
			Side{Content: []byte("base\n"), CommitterTime: earlier},
			Side{Content: []byte("base\n"), CommitterTime: earlier},
			Side{Content: []byte("theirs\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeFalse())
		Expect(string(result.Content)).To(Equal("theirs\n"))
	})

	It("parses a CRLF file instead of treating its frontmatter as body", func() {
		result := MergeFile(
			Side{Content: []byte("---\r\ncluster: base\r\n---\r\nb\r\n"), CommitterTime: earlier},
			Side{Content: []byte("---\r\ncluster: base\r\n---\r\nb\r\n"), CommitterTime: earlier},
			Side{Content: []byte("---\r\ncluster: sun\r\n---\r\nb\r\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeFalse())
		Expect(frontmatter(result.Content)["cluster"]).To(Equal("sun"))
	})

	It("accepts an empty frontmatter block", func() {
		result := MergeFile(
			Side{Content: []byte("---\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\n---\nb\n"), CommitterTime: earlier},
			Side{Content: []byte("---\n---\nb\n"), CommitterTime: later},
		)

		Expect(result.Quarantine).To(BeFalse())
		Expect(string(result.Content)).To(Equal("b\n"))
	})
})
