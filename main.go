// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"time"

	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	libhttp "github.com/bborbe/http"
	"github.com/bborbe/run"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"

	vaultapi "github.com/bborbe/vault-api/pkg"
	"github.com/bborbe/vault-api/pkg/handler"
	"github.com/bborbe/vault-api/pkg/publisher"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"Sentry DSN"   display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy"`

	Listen          string           `required:"false" arg:"listen"           env:"LISTEN"           usage:"HTTP listen address"                                                                                                                default:":9090"`
	VaultPath       string           `required:"true"  arg:"vault-path"       env:"VAULT_PATH"       usage:"path to the vault git working tree"`
	VaultName       string           `required:"true"  arg:"vault-name"       env:"VAULT_NAME"       usage:"vault name reported in responses"`
	InstanceStage   string           `required:"false" arg:"instance-stage"   env:"INSTANCE_STAGE"   usage:"stage of this vault-api instance, reported as the instance_stage metric label (dev, prod). Distinct from the per-task stage label." default:""`
	TasksDir        string           `required:"false" arg:"tasks-dir"        env:"TASKS_DIR"        usage:"task directory inside the vault"                                                                                                    default:"25 Tasks"`
	GoalsDir        string           `required:"false" arg:"goals-dir"        env:"GOALS_DIR"        usage:"goal directory inside the vault"                                                                                                    default:"24 Goals"`
	GatewaySecret   string           `required:"false" arg:"gateway-secret"   env:"GATEWAY_SECRET"   usage:"Shared secret required in X-Gateway-Secret for /api/v1/* requests. Empty = no auth."                                                                   display:"length"`
	RefreshInterval libtime.Duration `required:"false" arg:"refresh-interval" env:"REFRESH_INTERVAL" usage:"how often to check the working tree for a new HEAD"                                                                                 default:"30s"`
	// KafkaBrokers enables the change-event producer. Empty disables it, which is
	// what a local run wants: without a producer the service still serves reads.
	KafkaBrokers string `required:"false" arg:"kafka-brokers"    env:"KAFKA_BROKERS"    usage:"Comma separated Kafka brokers for change events. Empty = publishing disabled."`
	// TopicPrefix selects the per-cluster topic: the event topic is
	// {prefix}-agent-task-v1-event, derived from lib.TaskV1SchemaID.
	TopicPrefix string `required:"false" arg:"topic-prefix"     env:"TOPIC_PREFIX"     usage:"Kafka topic prefix for change events (dev: develop, prod: master)"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	metrics := vaultapi.NewMetrics(a.InstanceStage, a.VaultName)

	storageConfig := storage.DefaultConfig()
	storageConfig.TasksDir = a.TasksDir
	storageConfig.GoalsDir = a.GoalsDir
	store := vaultapi.NewCountingPageStorage(storage.NewPageStorage(storageConfig), metrics)

	index := vaultapi.NewIndex(vaultapi.Config{
		VaultPath: a.VaultPath,
		VaultName: a.VaultName,
		TasksDir:  a.TasksDir,
		GoalsDir:  a.GoalsDir,
	}, metrics)
	if err := index.Build(ctx, store); err != nil {
		glog.Warningf("initial index build failed: %v", err)
	}

	factory, err := a.emitterFactory(ctx)
	if err != nil {
		return err
	}

	return run.CancelOnFirstFinish(ctx,
		a.refresh(index, store, factory),
		libhttp.NewServer(a.Listen, handler.NewHandler(ctx, index, a.GatewaySecret)),
	)
}

// emitterFactory validates the change-event config and returns a constructor, or
// nil when no broker is configured.
//
// The constructor is deliberately not called here: NewSyncProducer blocks on an
// unreachable broker, so building the producer before the HTTP server starts would
// make the read API's availability depend on Kafka. The refresh loop builds it
// lazily instead, and a broker outage degrades to "no events yet" rather than
// "no reads".
func (a *application) emitterFactory(
	ctx context.Context,
) (func(ctx context.Context) (vaultapi.Emitter, func() error, error), error) {
	if a.KafkaBrokers == "" {
		glog.Infof("no kafka brokers configured — change events disabled")
		return nil, nil
	}
	// With brokers set but no prefix the topic would be "-agent-task-v1-event",
	// which is a config mistake rather than a transient failure, so it fails fast
	// and loudly instead of publishing somewhere unintended.
	if a.TopicPrefix == "" {
		return nil, errors.Errorf(ctx, "topic-prefix is required when kafka-brokers is set")
	}
	return func(ctx context.Context) (vaultapi.Emitter, func() error, error) {
		taskPublisher, closePublisher, err := publisher.NewKafkaTaskPublisher(
			ctx,
			a.KafkaBrokers,
			base.TopicPrefix(a.TopicPrefix),
			libtime.NewCurrentDateTime(),
		)
		if err != nil {
			return nil, nil, errors.Wrapf(ctx, err, "create change-event publisher failed")
		}
		glog.Infof("change events enabled on topic prefix %q", a.TopicPrefix)
		return vaultapi.NewEmitter(
			vaultapi.Config{
				VaultPath: a.VaultPath,
				VaultName: a.VaultName,
				TasksDir:  a.TasksDir,
				GoalsDir:  a.GoalsDir,
			},
			vaultapi.NewGitDiffer(a.VaultPath),
			taskPublisher,
		), closePublisher, nil
	}, nil
}

// changePublisher holds the lazily built producer. A failed construction is
// retried on the next tick that has something to publish, so a broker that comes
// up late still gets the backlog.
type changePublisher struct {
	factory func(ctx context.Context) (vaultapi.Emitter, func() error, error)
	emitter vaultapi.Emitter
	close   func() error

	// A single event that always fails — a payload over the broker's
	// message.max.bytes is the concrete case — would otherwise hold the baseline
	// forever and silently stop every later change from publishing. Only errors the
	// broker will never accept count toward the cap; a connectivity failure is
	// retried indefinitely, because dropping changes during an outage is exactly
	// what this feature exists to prevent.
	attempts    int
	rangeKey    string
	isPermanent func(err error) bool
}

const maxPublishRetries = 3

func (p *changePublisher) get(ctx context.Context) vaultapi.Emitter {
	if p == nil || p.factory == nil {
		return nil
	}
	if p.emitter != nil {
		return p.emitter
	}
	emitter, closePublisher, err := p.factory(ctx)
	if err != nil {
		glog.Warningf("change events unavailable, retrying on the next change: %v", err)
		return nil
	}
	p.emitter = emitter
	p.close = closePublisher
	return p.emitter
}

// fail records a failed attempt on rangeKey and reports whether the range should
// be abandoned rather than retried again. A transient failure resets the counter:
// an outage of any length must not consume the budget meant for a poison message.
func (p *changePublisher) fail(rangeKey string, err error) bool {
	if p.isPermanent != nil && !p.isPermanent(err) {
		p.rangeKey = rangeKey
		p.attempts = 0
		return false
	}
	if p.rangeKey != rangeKey {
		p.rangeKey = rangeKey
		p.attempts = 0
	}
	p.attempts++
	return p.attempts >= maxPublishRetries
}

func (p *changePublisher) succeeded() {
	p.attempts = 0
	p.rangeKey = ""
}

func (p *changePublisher) closeIfOpen() {
	if p == nil || p.close == nil {
		return
	}
	if err := p.close(); err != nil {
		glog.Warningf("close change-event publisher failed: %v", err)
	}
}

// refresh rebuilds the index when the working tree's HEAD changes, or while the
// first successful build has not happened yet. A steady HEAD means no file is
// read, so the file-read counter stays flat between pulls.
//
// Before rebuilding it publishes one change event per task file that differs
// between the previous HEAD and the current one. That is what makes externally
// pushed changes visible without the controller's git scan. The baseline advances
// only after a successful publish, so a Kafka failure retries the same diff on the
// next tick instead of losing it.
func (a *application) refresh(
	index *vaultapi.Index,
	store storage.PageStorage,
	factory func(ctx context.Context) (vaultapi.Emitter, func() error, error),
) run.Func {
	return func(ctx context.Context) error {
		changes := &changePublisher{factory: factory, isPermanent: publisher.IsPermanent}
		defer changes.closeIfOpen()
		emittedHead := vaultapi.ResolveHead(a.VaultPath)
		ticker := time.NewTicker(a.RefreshInterval.Duration())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				current := vaultapi.ResolveHead(a.VaultPath)
				ready := index.Readiness().Ready
				if !ready || index.Head() != current {
					if err := index.Build(ctx, store); err != nil {
						glog.Warningf("index rebuild failed: %v", err)
					}
				}
				// Publish after the rebuild, never before it. Building the producer
				// blocks on an unreachable broker (sarama dials and retries), so
				// publishing first would stall the index the read path serves for the
				// whole dial timeout — the coupling this change exists to remove.
				emittedHead = publishChanges(ctx, changes, emittedHead, current)
			}
		}
	}
}

// publishChanges emits the events for emittedHead..current and returns the
// baseline to carry forward: current once the batch published, unchanged when it
// did not, so the same diff is retried on the next tick rather than dropped.
func publishChanges(
	ctx context.Context,
	changes *changePublisher,
	emittedHead string,
	current string,
) string {
	if current == "" {
		return emittedHead
	}
	// Adopt the first head seen rather than diffing from nothing: with no earlier
	// commit there is nothing to publish, and starting from "" would replay the
	// vault's entire history.
	//
	// Known limitation, accepted: the baseline is process-local, so changes pushed
	// while vault-api was down are adopted silently and never published. Recovering
	// them needs either a persisted baseline or a full re-publish at startup; until
	// one exists, a restarted instance's consumers converge on a task's next change
	// rather than on the restart itself.
	if emittedHead == "" {
		return current
	}
	if current == emittedHead {
		return emittedHead
	}
	emitter := changes.get(ctx)
	if emitter == nil {
		return emittedHead
	}
	if err := emitter.Emit(ctx, emittedHead, current); err != nil {
		rangeKey := emittedHead + ".." + current
		if changes.fail(rangeKey, err) {
			glog.Errorf(
				"abandoning change batch %s after %d failed attempts; later changes resume from %s: %v",
				rangeKey,
				maxPublishRetries,
				current,
				err,
			)
			return current
		}
		glog.Warningf(
			"publish changes %s failed (attempt %d/%d): %v",
			rangeKey,
			changes.attempts,
			maxPublishRetries,
			err,
		)
		return emittedHead
	}
	changes.succeeded()
	return current
}
