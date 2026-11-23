// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/bborbe/vault-cli/pkg/storage"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/publisher"
)

// emitterFactory used to reject brokers-without-prefix on the belief that the
// topic would render as "-agent-task-v1-event". cqrs's base.TopicPrefix does the
// opposite — an empty prefix renders as no segment and no leading dash — so the
// octopus agent lane, which runs one cluster per stage and consumes
// "agent-task-v1-event", could not start at all: the process exited with
// "topic-prefix is required when kafka-brokers is set" on every boot.
var _ = Describe("emitterFactory", func() {
	It("builds an emitter when brokers are set and the prefix is empty", func() {
		app := &application{KafkaBrokers: "tls://broker:9093", TopicPrefix: ""}

		Expect(app.emitterFactory(publisher.NewDiscard())).NotTo(BeNil())
	})

	It("builds an emitter when brokers and a prefix are both set", func() {
		app := &application{KafkaBrokers: "tls://broker:9093", TopicPrefix: "develop"}

		Expect(app.emitterFactory(publisher.NewDiscard())).NotTo(BeNil())
	})

	It("returns no emitter when brokers are unset", func() {
		app := &application{KafkaBrokers: "", TopicPrefix: ""}

		Expect(app.emitterFactory(publisher.NewDiscard())).To(BeNil())
	})
})

var _ = Describe("eventVaultName", func() {
	It("prefers the routing slug when one is configured", func() {
		app := &application{VaultName: "private-personal", EventVaultName: "personal"}

		Expect(app.eventVaultName()).To(Equal("personal"))
	})

	It("falls back to the reported vault name when no slug is configured", func() {
		app := &application{VaultName: "private-personal"}

		Expect(app.eventVaultName()).To(Equal("private-personal"))
	})

	It("treats an explicitly empty slug as unset rather than stamping an empty vault", func() {
		app := &application{VaultName: "personal", EventVaultName: ""}

		Expect(app.eventVaultName()).To(Equal("personal"))
	})
})

// recordingStore records what was written and can be made to fail, so the save
// path and its retry can both be observed without a file.
type recordingStore struct {
	saves []string
	err   error
}

func (s *recordingStore) Load(context.Context) (string, error) { return "", nil }

func (s *recordingStore) Save(_ context.Context, baseline string) error {
	if s.err != nil {
		return s.err
	}
	s.saves = append(s.saves, baseline)
	return nil
}

var _ = Describe("persistBaseline", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("writes a baseline that moved", func() {
		store := &recordingStore{}
		saved := ""

		persistBaseline(ctx, store, "bbbbbbb2", &saved)

		Expect(store.saves).To(Equal([]string{"bbbbbbb2"}))
		Expect(saved).To(Equal("bbbbbbb2"))
	})

	It("writes nothing for an unchanged baseline", func() {
		store := &recordingStore{}
		saved := "bbbbbbb2"

		persistBaseline(ctx, store, "bbbbbbb2", &saved)

		Expect(store.saves).To(BeEmpty())
	})

	It("writes nothing for an empty baseline", func() {
		store := &recordingStore{}
		saved := ""

		persistBaseline(ctx, store, "", &saved)

		Expect(store.saves).To(BeEmpty())
	})

	It("retries a baseline whose save failed", func() {
		// Recording a failed write as saved would suppress every later attempt, and
		// on a quiescent vault the file would stay at the previous commit until the
		// baseline moved again — the failure this guards.
		store := &recordingStore{err: errors.New("volume is read-only")}
		saved := ""

		persistBaseline(ctx, store, "bbbbbbb2", &saved)
		Expect(saved).To(BeEmpty())

		store.err = nil
		persistBaseline(ctx, store, "bbbbbbb2", &saved)

		Expect(store.saves).To(Equal([]string{"bbbbbbb2"}))
		Expect(saved).To(Equal("bbbbbbb2"))
	})
})

var _ = Describe("attachedNeedsRebuild", func() {
	var (
		root  string
		extra attachedVault
		last  vaultapi.Fingerprint
	)

	BeforeEach(func() {
		var err error
		root, err = os.MkdirTemp("", "vault-api-attached")
		Expect(err).NotTo(HaveOccurred())
		for _, dir := range []string{"T", "G", "P", ".git"} {
			Expect(os.MkdirAll(filepath.Join(root, dir), 0o750)).To(Succeed())
		}
		Expect(os.WriteFile(
			filepath.Join(root, ".git", "HEAD"),
			[]byte("0123456789abcdef0123456789abcdef01234567\n"),
			0o600,
		)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(root, "T", "a.md"), []byte("---\n---\n"), 0o600)).
			To(Succeed())
		cfg := vaultapi.Config{
			VaultPath: root, VaultName: "v", TasksDir: "T", GoalsDir: "G", TopicsDir: "P",
		}
		storageConfig := storage.DefaultConfig()
		storageConfig.TasksDir, storageConfig.GoalsDir, storageConfig.TopicsDir = "T", "G", "P"
		extra = attachedVault{
			index: vaultapi.NewIndex(cfg, vaultapi.NewNoopMetrics()),
			store: storage.NewPageStorage(storageConfig),
			cfg:   cfg,
		}
		last = vaultapi.PageFingerprint(cfg)
		Expect(extra.index.Build(context.Background(), extra.store)).To(Succeed())
	})

	AfterEach(func() { _ = os.RemoveAll(root) })

	It("does not rebuild when neither HEAD nor the page files moved", func() {
		rebuild, fp := attachedNeedsRebuild(extra, last)
		Expect(rebuild).To(BeFalse())
		Expect(fp).To(Equal(last))
	})

	It("rebuilds when a page file was edited without a new HEAD", func() {
		later := time.Now().Add(time.Hour)
		Expect(os.Chtimes(filepath.Join(root, "T", "a.md"), later, later)).To(Succeed())
		rebuild, fp := attachedNeedsRebuild(extra, last)
		Expect(rebuild).To(BeTrue())
		Expect(fp).NotTo(Equal(last))
		// Once the new fingerprint is carried forward the vault settles again.
		rebuild, _ = attachedNeedsRebuild(extra, fp)
		Expect(rebuild).To(BeFalse())
	})

	It("rebuilds when a page file was created", func() {
		Expect(os.WriteFile(filepath.Join(root, "G", "g.md"), []byte("x"), 0o600)).To(Succeed())
		rebuild, _ := attachedNeedsRebuild(extra, last)
		Expect(rebuild).To(BeTrue())
	})

	It("rebuilds when HEAD moved", func() {
		Expect(os.WriteFile(
			filepath.Join(root, ".git", "HEAD"),
			[]byte("fedcba9876543210fedcba9876543210fedcba98\n"),
			0o600,
		)).To(Succeed())
		rebuild, _ := attachedNeedsRebuild(extra, last)
		Expect(rebuild).To(BeTrue())
	})
})
