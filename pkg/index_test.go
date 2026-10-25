// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

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

	It("filters by task_type, short_id and updated_since", func() {
		writeFile(tasksDir, "Alpha", taskFrontmatter(
			"status: in_progress",
			"task_type: sentry-deep-analyzer",
			"short_id: 7811216784",
		))
		writeFile(tasksDir, "Bravo", taskFrontmatter(
			"status: in_progress",
			"task_type: build-fix",
		))
		Expect(index.Build(ctx, store)).To(Succeed())

		byType, _, _ := index.QueryTasks(vaultapi.TaskFilter{TaskType: "sentry-deep-analyzer"})
		Expect(byType).To(HaveLen(1))
		Expect(byType[0].Title).To(Equal("Alpha"))
		Expect(byType[0].ShortID).To(Equal("7811216784"))

		byShortID, _, _ := index.QueryTasks(vaultapi.TaskFilter{ShortID: "7811216784"})
		Expect(byShortID).To(HaveLen(1))

		past := time.Now().Add(-time.Hour)
		recent, _, _ := index.QueryTasks(vaultapi.TaskFilter{UpdatedSince: &past})
		Expect(recent).To(HaveLen(2))

		future := time.Now().Add(time.Hour)
		none, _, _ := index.QueryTasks(vaultapi.TaskFilter{UpdatedSince: &future})
		Expect(none).To(BeEmpty())

		// A typed filter matching nothing must return nothing, never fall back to
		// the whole vault — that fallback is the failure this filter set prevents.
		missing, _, _ := index.QueryTasks(vaultapi.TaskFilter{TaskType: "no-such-type"})
		Expect(missing).To(BeEmpty())
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

	It("counts open tasks per frontmatter stage and task_type, refreshing on every build", func() {
		writeFile(
			tasksDir,
			"Alpha",
			taskFrontmatter("status: in_progress", "task_type: pr-review", "stage: prod"),
		)
		writeFile(
			tasksDir,
			"Beta",
			taskFrontmatter("status: next", "task_type: pr-review", "stage: prod"),
		)
		writeFile(
			tasksDir,
			"Gamma",
			taskFrontmatter("status: completed", "task_type: pr-review", "stage: prod"),
		)
		writeFile(
			tasksDir,
			"Delta",
			taskFrontmatter("status: aborted", "task_type: pr-review", "stage: dev"),
		)
		writeFile(
			tasksDir,
			"Epsilon",
			taskFrontmatter("status: in_progress", "task_type: github-release", "stage: dev"),
		)
		writeFile(tasksDir, "Zeta", taskFrontmatter("status: in_progress", "stage: prod"))
		writeFile(
			tasksDir,
			"Eta",
			taskFrontmatter("phase: execution", "task_type: pr-review", "stage: dev"),
		)
		writeFile(
			tasksDir,
			"Theta",
			taskFrontmatter("status: in_progress", "task_type: pr-review", `stage: ""`),
		)
		writeFile(tasksDir, "Iota", taskFrontmatter("status: in_progress", "task_type: pr-review"))
		Expect(index.Build(ctx, store)).To(Succeed())

		// completed and aborted are excluded; a task with no stage, no task_type,
		// no phase or no status line still counts as open, under the matching
		// unknown bucket. Eta carries phase: execution and no status line.
		Expect(metrics.openTasks).To(Equal(map[vaultapi.OpenTaskKey]int{
			{Stage: "prod", TaskType: "pr-review", Phase: "unknown"}:     2, // Alpha, Beta
			{Stage: "prod", TaskType: "unknown", Phase: "unknown"}:       1, // Zeta — no task_type
			{Stage: "dev", TaskType: "github-release", Phase: "unknown"}: 1, // Epsilon
			{Stage: "dev", TaskType: "pr-review", Phase: "execution"}:    1, // Eta — no status line
			{Stage: "unknown", TaskType: "pr-review", Phase: "unknown"}:  2, // Theta empty stage, Iota missing
		}))
		Expect(metrics.setCalls).To(Equal(1))
	})
})

var _ = Describe("Readiness", func() {
	var (
		ctx       context.Context
		vaultPath string
		store     storage.PageStorage
		metrics   *testMetrics
	)

	newIndex := func(staleAfter time.Duration) *vaultapi.Index {
		return vaultapi.NewIndex(vaultapi.Config{
			VaultPath:  vaultPath,
			VaultName:  "test",
			TasksDir:   "25 Tasks",
			GoalsDir:   "24 Goals",
			StaleAfter: staleAfter,
		}, metrics)
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-readiness-test")
		Expect(err).To(BeNil())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "25 Tasks"), 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Goals"), 0o700)).To(Succeed())

		metrics = &testMetrics{reads: map[string]int64{}}
		storageConfig := storage.DefaultConfig()
		storageConfig.TasksDir = "25 Tasks"
		storageConfig.GoalsDir = "24 Goals"
		store = vaultapi.NewCountingPageStorage(
			storage.NewPageStorage(storageConfig),
			metrics,
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	It("is not ready before the first build", func() {
		Expect(newIndex(5 * time.Minute).Readiness().Ready).To(BeFalse())
	})

	It("is ready while the tree is verified and freshly fetched", func() {
		writeFetchHead(vaultPath, time.Now())
		index := newIndex(5 * time.Minute)
		Expect(index.Build(ctx, store)).To(Succeed())
		index.Verify()

		readiness := index.Readiness()
		Expect(readiness.Ready).To(BeTrue())
		Expect(readiness.IndexAgeSeconds).To(BeNumerically("<=", 1))
		Expect(readiness.TreeAgeSeconds).NotTo(BeNil())
		Expect(*readiness.TreeAgeSeconds).To(BeNumerically("<=", 1))
	})

	It("is not ready when the tree has not been fetched inside the window", func() {
		// The index is fresh — the loop is verifying every tick — and the pod is
		// still not ready, because the tree it indexes stopped being refreshed.
		writeFetchHead(vaultPath, time.Now().Add(-2*time.Hour))
		index := newIndex(5 * time.Minute)
		Expect(index.Build(ctx, store)).To(Succeed())
		index.Verify()

		readiness := index.Readiness()
		Expect(readiness.Ready).To(BeFalse())
		Expect(readiness.IndexAgeSeconds).To(BeNumerically("<=", 1))
		Expect(*readiness.TreeAgeSeconds).To(BeNumerically(">=", 7199))
	})

	It("reports a null tree age when the tree carries no fetch to measure", func() {
		index := newIndex(5 * time.Minute)
		Expect(index.Build(ctx, store)).To(Succeed())
		index.Verify()

		readiness := index.Readiness()
		Expect(readiness.Ready).To(BeTrue())
		Expect(readiness.TreeAgeSeconds).To(BeNil())
	})

	It("is not ready after a failed rebuild, and recovers on the next good one", func() {
		writeFile(filepath.Join(vaultPath, "25 Tasks"), "Alpha", taskFrontmatter(
			"status: in_progress",
			"phase: execution",
			"task_identifier: 11111111-1111-1111-1111-111111111111",
		))
		index := newIndex(0)
		Expect(index.Build(ctx, store)).To(Succeed())
		Expect(index.Readiness().Ready).To(BeTrue())

		Expect(os.RemoveAll(filepath.Join(vaultPath, "25 Tasks"))).To(Succeed())
		Expect(index.Build(ctx, store)).NotTo(Succeed())

		Expect(index.Readiness().Ready).To(BeFalse())
		Expect(index.Built()).To(BeFalse())
		// The read path keeps serving the last good index rather than emptying.
		items, _, _ := index.QueryTasks(vaultapi.TaskFilter{})
		Expect(items).To(HaveLen(1))

		Expect(os.MkdirAll(filepath.Join(vaultPath, "25 Tasks"), 0o700)).To(Succeed())
		Expect(index.Build(ctx, store)).To(Succeed())
		Expect(index.Readiness().Ready).To(BeTrue())
	})

	It("leaves the staleness rule off when StaleAfter is zero", func() {
		writeFetchHead(vaultPath, time.Now().Add(-48*time.Hour))
		index := newIndex(0)
		Expect(index.Build(ctx, store)).To(Succeed())
		Expect(index.Readiness().Ready).To(BeTrue())
	})
})

var _ = Describe("FetchAge", func() {
	It("reports unknown when the tree carries no FETCH_HEAD", func() {
		vaultPath, err := os.MkdirTemp("", "vault-api-fetch-age-test")
		Expect(err).To(BeNil())
		defer func() {
			Expect(os.RemoveAll(vaultPath)).To(Succeed())
		}()

		_, known := vaultapi.FetchAge(vaultPath)
		Expect(known).To(BeFalse())
	})

	It("reports the age of the last fetch", func() {
		vaultPath, err := os.MkdirTemp("", "vault-api-fetch-age-test")
		Expect(err).To(BeNil())
		defer func() {
			Expect(os.RemoveAll(vaultPath)).To(Succeed())
		}()
		writeFetchHead(vaultPath, time.Now().Add(-90*time.Second))

		age, known := vaultapi.FetchAge(vaultPath)
		Expect(known).To(BeTrue())
		Expect(age).To(BeNumerically("~", 90, 5))
	})
})

type testMetrics struct {
	reads     map[string]int64
	openTasks map[vaultapi.OpenTaskKey]int
	setCalls  int
}

func (m *testMetrics) AddFileReads(kind string, count int64) {
	m.reads[kind] += count
}

func (m *testMetrics) SetOpenTasks(counts map[vaultapi.OpenTaskKey]int) {
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

// writeFetchHead gives the test vault a fetch heartbeat at the given time, which
// is what FetchAge measures. The content is irrelevant — only the mtime is read.
func writeFetchHead(vaultPath string, fetchedAt time.Time) {
	Expect(os.MkdirAll(filepath.Join(vaultPath, ".git"), 0o700)).To(Succeed())
	path := filepath.Join(vaultPath, ".git", "FETCH_HEAD")
	Expect(
		os.WriteFile(
			path,
			[]byte("0000000000000000000000000000000000000000\t\tbranch 'main'\n"),
			0o600,
		),
	).To(Succeed())
	Expect(os.Chtimes(path, fetchedAt, fetchedAt)).To(Succeed())
}
