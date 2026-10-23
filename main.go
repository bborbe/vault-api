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

	Listen          string           `required:"false" arg:"listen"           env:"LISTEN"           usage:"HTTP listen address"                                                                 default:":9090"`
	VaultPath       string           `required:"true"  arg:"vault-path"       env:"VAULT_PATH"       usage:"path to the vault git working tree"`
	VaultName       string           `required:"true"  arg:"vault-name"       env:"VAULT_NAME"       usage:"vault name reported in responses"`
	Stage           string           `required:"false" arg:"stage"            env:"STAGE"            usage:"deployment stage reported on every metric (dev, prod)"                               default:""`
	TasksDir        string           `required:"false" arg:"tasks-dir"        env:"TASKS_DIR"        usage:"task directory inside the vault"                                                     default:"25 Tasks"`
	GoalsDir        string           `required:"false" arg:"goals-dir"        env:"GOALS_DIR"        usage:"goal directory inside the vault"                                                     default:"24 Goals"`
	GatewaySecret   string           `required:"false" arg:"gateway-secret"   env:"GATEWAY_SECRET"   usage:"Shared secret required in X-Gateway-Secret for /api/v1/* requests. Empty = no auth."                    display:"length"`
	RefreshInterval libtime.Duration `required:"false" arg:"refresh-interval" env:"REFRESH_INTERVAL" usage:"how often to check the working tree for a new HEAD"                                  default:"30s"`
	// KafkaBrokers enables the change-event producer. Empty disables it, which is
	// what a local run wants: without a producer the service still serves reads.
	KafkaBrokers string `required:"false" arg:"kafka-brokers"    env:"KAFKA_BROKERS"    usage:"Comma separated Kafka brokers for change events. Empty = publishing disabled."`
	// TopicPrefix selects the per-cluster topic: the event topic is
	// {prefix}-agent-task-v1-event, derived from lib.TaskV1SchemaID.
	TopicPrefix string `required:"false" arg:"topic-prefix"     env:"TOPIC_PREFIX"     usage:"Kafka topic prefix for change events (dev: develop, prod: master)"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	metrics := vaultapi.NewMetrics(a.Stage, a.VaultName)

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

	emitter, closePublisher, err := a.emitter(ctx)
	if err != nil {
		return err
	}
	if closePublisher != nil {
		defer func() {
			if closeErr := closePublisher(); closeErr != nil {
				glog.Warningf("close change-event publisher failed: %v", closeErr)
			}
		}()
	}

	return run.CancelOnFirstFinish(ctx,
		a.refresh(index, store, emitter),
		libhttp.NewServer(a.Listen, handler.NewHandler(ctx, index, a.GatewaySecret)),
	)
}

// emitter builds the change-event producer, or returns a nil emitter when no
// broker is configured. A nil emitter leaves the service a pure reader, which is
// the pre-change behaviour and what a local run wants.
func (a *application) emitter(ctx context.Context) (vaultapi.Emitter, func() error, error) {
	if a.KafkaBrokers == "" {
		glog.Infof("no kafka brokers configured — change events disabled")
		return nil, nil, nil
	}
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
	emitter vaultapi.Emitter,
) run.Func {
	return func(ctx context.Context) error {
		emittedHead := vaultapi.ResolveHead(a.VaultPath)
		ticker := time.NewTicker(a.RefreshInterval.Duration())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				current := vaultapi.ResolveHead(a.VaultPath)
				emittedHead = publishChanges(ctx, emitter, emittedHead, current)
				ready := index.Readiness().Ready
				if ready && index.Head() == current {
					continue
				}
				if err := index.Build(ctx, store); err != nil {
					glog.Warningf("index rebuild failed: %v", err)
				}
			}
		}
	}
}

// publishChanges emits the events for emittedHead..current and returns the
// baseline to carry forward: current once the batch published, unchanged when it
// did not, so the same diff is retried on the next tick rather than dropped.
func publishChanges(
	ctx context.Context,
	emitter vaultapi.Emitter,
	emittedHead string,
	current string,
) string {
	if emitter == nil || current == "" || emittedHead == "" || current == emittedHead {
		return emittedHead
	}
	if err := emitter.Emit(ctx, emittedHead, current); err != nil {
		glog.Warningf("publish changes %s..%s failed: %v", emittedHead, current, err)
		return emittedHead
	}
	return current
}
