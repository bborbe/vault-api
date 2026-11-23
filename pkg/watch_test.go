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

var _ = Describe("Local mode", func() {
	var (
		ctx       context.Context
		vaultPath string
		tasksDir  string
		topicsDir string
		store     storage.PageStorage
		index     *vaultapi.Index
		metrics   *testMetrics
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-local-test")
		Expect(err).To(BeNil())
		tasksDir = filepath.Join(vaultPath, "25 Tasks")
		topicsDir = filepath.Join(vaultPath, "23 Topics")
		Expect(os.MkdirAll(tasksDir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(topicsDir, 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(vaultPath, "24 Goals"), 0o700)).To(Succeed())

		metrics = &testMetrics{reads: map[string]int64{}}
		storageConfig := storage.DefaultConfig()
		storageConfig.TasksDir = "25 Tasks"
		storageConfig.GoalsDir = "24 Goals"
		storageConfig.TopicsDir = "23 Topics"
		store = vaultapi.NewCountingPageStorage(
			storage.NewPageStorage(storageConfig),
			metrics,
		)
		index = vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
			TopicsDir: "23 Topics",
		}, metrics)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	It("filters by flag and keeps an unset flag distinct from false", func() {
		writeFile(tasksDir, "Flagged", taskFrontmatter(
			"status: in_progress",
			"flag: true",
		))
		writeFile(tasksDir, "Plain", taskFrontmatter("status: in_progress"))
		Expect(index.Build(ctx, store)).To(Succeed())

		yes := true
		flagged, _, _ := index.QueryTasks(vaultapi.TaskFilter{Flag: &yes})
		Expect(flagged).To(HaveLen(1))
		Expect(flagged[0].Title).To(Equal("Flagged"))
		Expect(flagged[0].Flag).To(BeTrue())

		no := false
		unflagged, _, _ := index.QueryTasks(vaultapi.TaskFilter{Flag: &no})
		Expect(unflagged).To(HaveLen(1))
		Expect(unflagged[0].Title).To(Equal("Plain"))

		all, _, _ := index.QueryTasks(vaultapi.TaskFilter{})
		Expect(all).To(HaveLen(2), "an unset Flag pointer must not filter")
	})

	It("keeps only tasks whose defer_date is absent or has arrived", func() {
		writeFile(tasksDir, "NoDefer", taskFrontmatter("status: in_progress"))
		writeFile(tasksDir, "Past", taskFrontmatter(
			"status: in_progress",
			`defer_date: "2020-01-01"`,
		))
		writeFile(tasksDir, "Future", taskFrontmatter(
			"status: in_progress",
			`defer_date: "2999-01-01"`,
		))
		Expect(index.Build(ctx, store)).To(Succeed())

		now := time.Now()
		due, _, _ := index.QueryTasks(vaultapi.TaskFilter{DueNow: true, Now: now})
		Expect(due).To(HaveLen(2))
		titles := []string{due[0].Title, due[1].Title}
		Expect(titles).To(ConsistOf("NoDefer", "Past"))

		// The boundary is inclusive: a defer_date exactly at Now is due.
		all, _, _ := index.QueryTasks(vaultapi.TaskFilter{})
		var past vaultapi.TaskItem
		for _, item := range all {
			if item.Title == "Past" {
				past = item
			}
		}
		Expect(past.DeferDate).ToNot(BeNil())
		atBoundary, _, _ := index.QueryTasks(vaultapi.TaskFilter{
			DueNow: true,
			Now:    *past.DeferDate,
		})
		Expect(atBoundary).To(ContainElement(HaveField("Title", "Past")))
	})

	It("serves topics alongside tasks and goals", func() {
		writeFile(topicsDir, "Some Topic", taskFrontmatter(
			"page_type: topic",
			"status: in_progress",
			"phase: execution",
			"goals:\n    - '[[Some Goal]]'",
			"themes:\n    - '[[Some Theme]]'",
		))
		Expect(index.Build(ctx, store)).To(Succeed())

		topics, warnings, _ := index.QueryTopics()
		Expect(warnings).To(Equal(0))
		Expect(topics).To(HaveLen(1))
		Expect(topics[0].Title).To(Equal("Some Topic"))
		Expect(topics[0].Path).To(Equal("23 Topics/Some Topic.md"))
		Expect(topics[0].Status).To(Equal("in_progress"))
		Expect(topics[0].Phase).To(Equal("execution"))
		Expect(topics[0].Goals).To(Equal([]string{"[[Some Goal]]"}))
		Expect(topics[0].Themes).To(Equal([]string{"[[Some Theme]]"}))
	})

	It("returns an empty topic list, not null, when topics are not configured", func() {
		noTopics := vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, metrics)
		Expect(noTopics.Build(ctx, store)).To(Succeed())

		topics, warnings, _ := noTopics.QueryTopics()
		Expect(topics).ToNot(BeNil(), "an absent topic set must be an empty slice, not nil")
		Expect(topics).To(BeEmpty())
		Expect(warnings).To(Equal(0))
	})

	It("indexes a written file with no commit and no HEAD move", func() {
		// No .git directory at all: ResolveHead returns "", so a HEAD-gated refresh
		// could never see the write. Visibility can only come from the watcher.
		Expect(index.Build(ctx, store)).To(Succeed())
		Expect(index.Head()).To(Equal(""))

		targets := vaultapi.WatchTargets(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
			TopicsDir: "23 Topics",
		})
		runCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			// A 1s heartbeat makes the 10s resync far outside this test's window, so
			// a hit proves the watch path rather than the safety net.
			done <- vaultapi.WatchRefresh(runCtx, index, store, targets, time.Second, nil)
		}()
		defer func() {
			// Stop the loop before the vault is removed, so the watcher does not log
			// against deleted directories and no goroutine outlives the spec.
			cancel()
			Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		}()

		// Write inside the poll rather than once after a fixed delay: the watcher
		// registers its directories asynchronously, so a single early write could
		// land before registration and be missed. Re-writing the same content is
		// harmless and guarantees an event once the watch is live — and the 10s
		// resync is outside this window, so a hit still proves the watch path.
		Eventually(func() []vaultapi.TaskItem {
			writeFile(tasksDir, "Fresh", taskFrontmatter("status: in_progress"))
			items, _, _ := index.QueryTasks(vaultapi.TaskFilter{})
			return items
		}, 5*time.Second, 200*time.Millisecond).Should(
			ContainElement(HaveField("Title", "Fresh")),
		)
	})
})
