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

// newVaultIndex builds an index over a fresh temp vault named name, with one
// flagged in-progress task and one goal, and returns it with its root path.
func newVaultIndex(
	ctx context.Context,
	name string,
	metrics vaultapi.Metrics,
) (*vaultapi.Index, string) {
	root, err := os.MkdirTemp("", "vault-api-attached-"+name)
	Expect(err).To(BeNil())
	tasks := filepath.Join(root, "25 Tasks")
	goals := filepath.Join(root, "24 Goals")
	Expect(os.MkdirAll(tasks, 0o700)).To(Succeed())
	Expect(os.MkdirAll(goals, 0o700)).To(Succeed())
	writeFile(tasks, name+" Task", taskFrontmatter(
		"status: in_progress",
		"phase: execution",
		"flag: true",
	))
	writeFile(goals, name+" Goal", "---\npage_type: goal\nstatus: in_progress\n---\n\nBody\n")

	storageConfig := storage.DefaultConfig()
	storageConfig.TasksDir = "25 Tasks"
	storageConfig.GoalsDir = "24 Goals"
	index := vaultapi.NewIndex(vaultapi.Config{
		VaultPath: root,
		VaultName: name,
		TasksDir:  "25 Tasks",
		GoalsDir:  "24 Goals",
	}, metrics)
	Expect(index.Build(ctx, storage.NewPageStorage(storageConfig))).To(Succeed())
	return index, root
}

func taskVaults(items []vaultapi.TaskItem) []string {
	vaults := make([]string, 0, len(items))
	for _, item := range items {
		vaults = append(vaults, item.Vault)
	}
	return vaults
}

func goalVaults(items []vaultapi.GoalItem) []string {
	vaults := make([]string, 0, len(items))
	for _, item := range items {
		vaults = append(vaults, item.Vault)
	}
	return vaults
}

var _ = Describe("Index with attached vaults", func() {
	var (
		ctx     context.Context
		primary *vaultapi.Index
		roots   []string
	)

	BeforeEach(func() {
		ctx = context.Background()
		var root string
		primary, root = newVaultIndex(ctx, "private-personal", vaultapi.NewNoopMetrics())
		roots = []string{root}
	})

	AfterEach(func() {
		for _, root := range roots {
			Expect(os.RemoveAll(root)).To(Succeed())
		}
	})

	It("serves only its own vault when nothing is attached", func() {
		items, _, _ := primary.QueryTasks(vaultapi.TaskFilter{})
		Expect(taskVaults(items)).To(ConsistOf("private-personal"))
		Expect(primary.Readiness().UnavailableVaults).To(BeEmpty())
		Expect(primary.IndexedVaults()).To(Equal([]string{"private-personal"}))
	})

	It("merges every attached vault's rows into the list queries, each with its own vault", func() {
		extra, root := newVaultIndex(ctx, "seibert-brogrammers", vaultapi.NewNoopMetrics())
		roots = append(roots, root)
		primary.Attach(extra)

		flag := true
		tasks, _, _ := primary.QueryTasks(vaultapi.TaskFilter{Flag: &flag, Status: "in_progress"})
		Expect(taskVaults(tasks)).To(ConsistOf("private-personal", "seibert-brogrammers"))

		goals, _, _ := primary.QueryGoals()
		Expect(goalVaults(goals)).To(ConsistOf("private-personal", "seibert-brogrammers"))

		// IndexedVaults is the coverage signal, so it pins order as well as
		// membership: a consumer reads element 0 as the primary vault.
		Expect(primary.IndexedVaults()).
			To(Equal([]string{"private-personal", "seibert-brogrammers"}))
	})

	It("keeps lookups by id on the primary vault only", func() {
		extra, root := newVaultIndex(ctx, "seibert-brogrammers", vaultapi.NewNoopMetrics())
		roots = append(roots, root)
		primary.Attach(extra)

		_, found, _ := primary.GetTask("seibert-brogrammers Task")
		Expect(found).To(BeFalse())
		_, found, _ = primary.GetTask("private-personal Task")
		Expect(found).To(BeTrue())
	})

	It("names a vault that cannot build, without dropping the other vaults' rows", func() {
		good, root := newVaultIndex(ctx, "seibert-brogrammers", vaultapi.NewNoopMetrics())
		roots = append(roots, root)
		storageConfig := storage.DefaultConfig()
		broken := vaultapi.NewIndex(vaultapi.Config{
			VaultPath: filepath.Join(os.TempDir(), "vault-api-attached-does-not-exist"),
			VaultName: "seibert-personal",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
		}, vaultapi.NewNoopMetrics())
		Expect(broken.Build(ctx, storage.NewPageStorage(storageConfig))).NotTo(Succeed())
		primary.Attach(good, broken)

		items, _, _ := primary.QueryTasks(vaultapi.TaskFilter{})
		Expect(taskVaults(items)).To(ConsistOf("private-personal", "seibert-brogrammers"))
		Expect(primary.Readiness().UnavailableVaults).To(ConsistOf("seibert-personal"))
		Expect(vaultapi.UnavailableLine("seibert-personal")).
			To(Equal("vault=seibert-personal rows_source=unavailable"))
	})

	It("does not publish an attached vault's open-task counts through the noop metrics", func() {
		metrics := &testMetrics{reads: map[string]int64{}}
		withReal, root := newVaultIndex(ctx, "private-agent", metrics)
		roots = append(roots, root)
		Expect(metrics.setCalls).To(Equal(1))
		Expect(withReal).NotTo(BeNil())

		_, root = newVaultIndex(ctx, "seibert-agent", vaultapi.NewNoopMetrics())
		roots = append(roots, root)
		Expect(metrics.setCalls).To(Equal(1))
	})
})
