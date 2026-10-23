// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

var _ = Describe("Index", func() {
	var (
		ctx       context.Context
		vaultPath string
		tasksDir  string
		goalsDir  string
		metrics   *testMetrics
		store     storage.PageStorage
		index     *vaultapi.Index
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-test")
		Expect(err).To(BeNil())
		tasksDir = filepath.Join(vaultPath, "25 Tasks")
		goalsDir = filepath.Join(vaultPath, "24 Goals")
		Expect(os.MkdirAll(tasksDir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(goalsDir, 0o700)).To(Succeed())

		metrics = &testMetrics{reads: map[string]int64{}}
		storageConfig := storage.DefaultConfig()
		storageConfig.TasksDir = "25 Tasks"
		storageConfig.GoalsDir = "24 Goals"
		store = vaultapi.NewCountingPageStorage(
			storage.NewPageStorage(storageConfig),
			metrics,
		)
		index = vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, metrics)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	It("indexes a valid task and filters it from the index", func() {
		writeFile(tasksDir, "Alpha", taskFrontmatter(
			"status: in_progress",
			"phase: execution",
			"assignee: bborbe",
			"task_identifier: 11111111-1111-1111-1111-111111111111",
			"goals:\n    - '[[Some Goal]]'",
		))
		Expect(index.Build(ctx, store)).To(Succeed())

		items, warnings, _ := index.QueryTasks(vaultapi.TaskFilter{
			Assignee: "bborbe",
			Phase:    "execution",
		})
		Expect(warnings).To(Equal(0))
		Expect(items).To(HaveLen(1))
		Expect(items[0].ID).To(Equal("11111111-1111-1111-1111-111111111111"))
		Expect(items[0].Title).To(Equal("Alpha"))
		Expect(items[0].Path).To(Equal("25 Tasks/Alpha.md"))
		Expect(items[0].Status).To(Equal("in_progress"))
		Expect(items[0].Phase).To(Equal("execution"))
		Expect(items[0].Assignee).To(Equal("bborbe"))
		Expect(items[0].Goals).To(Equal([]string{"[[Some Goal]]"}))
		Expect(items[0].Vault).To(Equal("test"))

		byGoal, _, _ := index.QueryTasks(vaultapi.TaskFilter{Goal: "Some Goal"})
		Expect(byGoal).To(HaveLen(1))
	})

	It("excludes a task with an unknown phase and counts it as a warning", func() {
		writeFile(tasksDir, "Good", taskFrontmatter("status: next", "phase: planning"))
		writeFile(tasksDir, "Bad", taskFrontmatter("status: next", "phase: completed"))
		Expect(index.Build(ctx, store)).To(Succeed())

		items, warnings, _ := index.QueryTasks(vaultapi.TaskFilter{})
		Expect(warnings).To(Equal(1))
		Expect(items).To(HaveLen(1))
		Expect(items[0].Title).To(Equal("Good"))

		_, found, invalid := index.GetTask("Bad")
		Expect(found).To(BeFalse())
		Expect(invalid).To(Equal("unknown phase completed"))
	})

	It("counts an unparseable task file as a warning", func() {
		writeFile(tasksDir, "Broken", "---\nfoo: [\n---\n\nBody\n")
		Expect(index.Build(ctx, store)).To(Succeed())

		items, warnings, _ := index.QueryTasks(vaultapi.TaskFilter{})
		Expect(warnings).To(Equal(1))
		Expect(items).To(BeEmpty())

		_, found, invalid := index.GetTask("broken")
		Expect(found).To(BeFalse())
		Expect(invalid).To(Equal("unparseable frontmatter"))
	})

	It("serves repeated queries without reading files again", func() {
		writeFile(tasksDir, "Alpha", taskFrontmatter("status: next"))
		Expect(index.Build(ctx, store)).To(Succeed())

		afterBuild := metrics.reads[vaultapi.FileReadKindList]
		Expect(afterBuild).To(BeNumerically(">", 0))

		index.QueryTasks(vaultapi.TaskFilter{Assignee: "bborbe"})
		index.QueryTasks(vaultapi.TaskFilter{Phase: "execution"})
		index.QueryGoals()
		Expect(metrics.reads[vaultapi.FileReadKindList]).To(Equal(afterBuild))
	})

	It("counts one direct page read", func() {
		writeFile(tasksDir, "Alpha", taskFrontmatter("status: next"))
		_, err := store.ReadPage(ctx, vaultPath, "25 Tasks", "Alpha")
		Expect(err).To(BeNil())
		Expect(metrics.reads[vaultapi.FileReadKindRead]).To(Equal(int64(1)))
	})

	It("indexes goals with their themes", func() {
		writeFile(
			goalsDir,
			"A Goal",
			"---\npage_type: goal\nstatus: next\nthemes:\n    - '[[Leverage Autonomous Agents]]'\n---\n\nBody\n",
		)
		Expect(index.Build(ctx, store)).To(Succeed())

		goals, warnings, _ := index.QueryGoals()
		Expect(warnings).To(Equal(0))
		Expect(goals).To(HaveLen(1))
		Expect(goals[0].Title).To(Equal("A Goal"))
		Expect(goals[0].Status).To(Equal("next"))
		Expect(goals[0].Themes).To(Equal([]string{"[[Leverage Autonomous Agents]]"}))
		Expect(goals[0].Vault).To(Equal("test"))

		goal, found, _ := index.GetGoal("A Goal")
		Expect(found).To(BeTrue())
		Expect(goal.ID).To(Equal("a-goal"))
	})

	It("counts open tasks per task_type and refreshes the gauge on every build", func() {
		writeFile(tasksDir, "Alpha", taskFrontmatter("status: in_progress", "task_type: pr-review"))
		writeFile(tasksDir, "Beta", taskFrontmatter("status: next", "task_type: pr-review"))
		writeFile(tasksDir, "Gamma", taskFrontmatter("status: completed", "task_type: pr-review"))
		writeFile(tasksDir, "Delta", taskFrontmatter("status: aborted", "task_type: pr-review"))
		writeFile(
			tasksDir,
			"Epsilon",
			taskFrontmatter("status: in_progress", "task_type: github-release"),
		)
		writeFile(tasksDir, "Zeta", taskFrontmatter("status: in_progress"))
		writeFile(tasksDir, "Eta", taskFrontmatter("phase: execution"))
		Expect(index.Build(ctx, store)).To(Succeed())

		// completed and aborted are excluded; a task with no task_type and one
		// with no status line both count as open, under the unknown bucket.
		Expect(metrics.openTasks).To(Equal(map[string]int{
			"pr-review":      2,
			"github-release": 1,
			"unknown":        2,
		}))
		Expect(metrics.setCalls).To(Equal(1))
	})
})

type testMetrics struct {
	reads     map[string]int64
	openTasks map[string]int
	setCalls  int
}

func (m *testMetrics) AddFileReads(kind string, count int64) {
	m.reads[kind] += count
}

func (m *testMetrics) SetOpenTasks(counts map[string]int) {
	m.setCalls++
	m.openTasks = counts
}

func taskFrontmatter(lines ...string) string {
	body := "---\n"
	for _, line := range lines {
		body += line + "\n"
	}
	return body + "---\n\nBody\n"
}

func writeFile(dir, name, content string) {
	Expect(os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o600)).To(Succeed())
}
