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
			Expect(frontmatter(result.Content)).To(Equal(frontmatter([]byte(fixtureExpected))))

			var merged map[string]any
			Expect(yaml.Unmarshal(result.Content, &merged)).To(Succeed())
			Expect(merged["cluster"]).To(Equal("sun"))
			Expect(merged["current_job"]).To(Equal("pr-reviewer-agent-dbfb48c8-20261021190000"))
			Expect(string(result.Content)).To(ContainSubstring("Body stays identical."))
		})
	})
})
