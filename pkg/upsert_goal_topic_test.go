// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

// A goal or topic edit is applied in place, so a flag/unflag is visible without
// the full rebuild a watch event used to trigger.
var _ = Describe("Index.UpsertGoal and UpsertTopic", func() {
	var (
		ctx       context.Context
		vaultPath string
		index     *vaultapi.Index
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		vaultPath, err = os.MkdirTemp("", "vault-api-upsert-gt")
		Expect(err).To(BeNil())
		index = vaultapi.NewIndex(vaultapi.Config{
			VaultPath: vaultPath,
			VaultName: "test",
			TasksDir:  "25 Tasks",
			GoalsDir:  "24 Goals",
			TopicsDir: "23 Topics",
		}, &testMetrics{})
	})

	AfterEach(func() {
		Expect(os.RemoveAll(vaultPath)).To(Succeed())
	})

	pageAt := func(dir string, name string, fm map[string]any) *domain.Page {
		return domain.NewPage(
			fm,
			domain.FileMetadata{Name: name, FilePath: filepath.Join(vaultPath, dir, name+".md")},
			domain.Content("body\n"),
		)
	}

	It("a goal edit that removes flag yields flag false, without a second entry", func() {
		index.UpsertGoal(ctx, pageAt("24 Goals", "Alpha", map[string]any{
			"status": "in_progress", "flag": true,
		}))
		goal, found, _ := index.GetGoal("alpha")
		Expect(found).To(BeTrue())
		Expect(goal.Flag).To(BeTrue())

		index.UpsertGoal(ctx, pageAt("24 Goals", "Alpha", map[string]any{
			"status": "in_progress",
		}))
		goal, found, _ = index.GetGoal("alpha")
		Expect(found).To(BeTrue())
		Expect(goal.Flag).To(BeFalse())
		goals, _, _ := index.QueryGoals()
		Expect(goals).To(HaveLen(1))
	})

	It("a goal with an unknown status is dropped and counted as a warning", func() {
		index.UpsertGoal(ctx, pageAt("24 Goals", "Alpha", map[string]any{"status": "in_progress"}))
		index.UpsertGoal(ctx, pageAt("24 Goals", "Alpha", map[string]any{"status": "nonsense"}))
		goals, warnings, _ := index.QueryGoals()
		Expect(goals).To(BeEmpty())
		Expect(warnings).To(Equal(1))

		index.UpsertGoal(ctx, pageAt("24 Goals", "Alpha", map[string]any{"status": "in_progress"}))
		goals, warnings, _ = index.QueryGoals()
		Expect(goals).To(HaveLen(1))
		Expect(warnings).To(Equal(0))
	})

	It("a topic edit that removes flag yields flag false, without a second entry", func() {
		index.UpsertTopic(ctx, pageAt("23 Topics", "Beta", map[string]any{
			"status": "in_progress", "flag": true,
		}))
		topic, found := index.GetTopic("beta")
		Expect(found).To(BeTrue())
		Expect(topic.Flag).To(BeTrue())

		index.UpsertTopic(ctx, pageAt("23 Topics", "Beta", map[string]any{
			"status": "in_progress",
		}))
		topic, found = index.GetTopic("beta")
		Expect(found).To(BeTrue())
		Expect(topic.Flag).To(BeFalse())
		topics, _, _ := index.QueryTopics()
		Expect(topics).To(HaveLen(1))
	})
})
