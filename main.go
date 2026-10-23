// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net/http"
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
	"github.com/bborbe/vault-api/pkg/gitrest"
	"github.com/bborbe/vault-api/pkg/handler"
	"github.com/bborbe/vault-api/pkg/publisher"
)

// publisherRetryInterval spaces attempts to build the change-event producer while
// a broker is unreachable. It is a startup concern only: once a publisher is
// installed the handle never needs rebuilding.
const publisherRetryInterval = 30 * time.Second

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
	GatewaySecret   string           `required:"false" arg:"gateway-secret"   env:"GATEWAY_SECRET"   usage:"Shared secret required in X-Gateway-Secret for /api/v1/* requests. Empty = no auth."                                                                                              display:"length"`
	GitRestURL      string           `required:"false" arg:"git-rest-url"     env:"GIT_REST_URL"     usage:"base URL of the vault's git-rest service; the write endpoints post whole files here"                                                default:"http://vault-obsidian-personal:9090"`
	RefreshInterval libtime.Duration `required:"false" arg:"refresh-interval" env:"REFRESH_INTERVAL" usage:"how often to check the working tree for a new HEAD"                                                                                 default:"30s"`
	// KafkaBrokers enables the change-event producer. Empty disables it, which is
	// what a local run wants: without a producer the service still serves reads and
	// writes, it just publishes nothing.
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

	writer := gitrest.NewClient(a.GitRestURL, a.GatewaySecret, http.DefaultClient)
	// A Deferred rather than a built publisher: NewSyncProducer blocks while a
	// broker is unreachable, so constructing it here would make the read API's
	// availability depend on Kafka. The handlers publish through this handle and
	// report ErrNotReady until the background builder installs the real one.
	taskPublisher := publisher.NewDeferred()

	return run.CancelOnFirstFinish(ctx,
		a.refresh(index, store),
		a.publishTaskEvents(taskPublisher),
		libhttp.NewServer(a.Listen, handler.NewHandler(
			ctx, index, a.GatewaySecret, a.VaultPath, a.VaultName, writer,
			libtime.NewCurrentDateTime(), taskPublisher,
		)),
	)
}

// publishTaskEvents builds the change-event producer off the startup path and
// installs it on the deferred handle. It never fails the process: a broker that is
// unreachable at startup degrades to "no events yet" and retries, rather than
// taking the read API down with it.
func (a *application) publishTaskEvents(deferred *publisher.Deferred) run.Func {
	return func(ctx context.Context) error {
		if a.KafkaBrokers == "" {
			// Install a discard rather than leaving the handle unset: disabled is not the
			// same state as not-yet-built, and a write against an unset handle would log
			// an error on every success — noise that reads as a fault.
			deferred.Set(publisher.NewDiscard())
			// A warning, not Info: publishing nothing is correct for a local run and a
			// misconfiguration in a deployed instance, and the two are indistinguishable
			// from here. It is stated once, at startup.
			glog.Warningf("no kafka brokers configured — task change events disabled")
			<-ctx.Done()
			return nil
		}
		// Brokers without a prefix would publish to "-agent-task-v1-event", which is
		// a config mistake rather than a transient failure: fail loudly instead of
		// publishing somewhere unintended.
		if a.TopicPrefix == "" {
			return errors.Errorf(ctx, "topic-prefix is required when kafka-brokers is set")
		}
		for {
			taskPublisher, closer, err := publisher.NewKafkaTaskPublisher(
				ctx,
				a.KafkaBrokers,
				base.TopicPrefix(a.TopicPrefix),
				libtime.NewCurrentDateTime(),
			)
			if err == nil {
				defer func() { _ = closer() }()
				deferred.Set(taskPublisher)
				glog.V(1).Infof("task change events enabled on prefix %s", a.TopicPrefix)
				break
			}
			glog.Errorf(
				"create task publisher failed, retrying in %s: %v",
				publisherRetryInterval,
				err,
			)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(publisherRetryInterval):
			}
		}
		<-ctx.Done()
		return nil
	}
}

// refresh rebuilds the index when the working tree's HEAD changes, or while the
// first successful build has not happened yet. A steady HEAD means no file is
// read, so the file-read counter stays flat between pulls.
func (a *application) refresh(index *vaultapi.Index, store storage.PageStorage) run.Func {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(a.RefreshInterval.Duration())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				ready := index.Readiness().Ready
				if ready && index.Head() == vaultapi.ResolveHead(a.VaultPath) {
					continue
				}
				if err := index.Build(ctx, store); err != nil {
					glog.Warningf("index rebuild failed: %v", err)
				}
			}
		}
	}
}
