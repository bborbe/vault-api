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

	Listen    string `required:"false" arg:"listen"              env:"LISTEN"              usage:"HTTP listen address"                                                                                                                                                     default:":9090"`
	VaultPath string `required:"true"  arg:"vault-path"          env:"VAULT_PATH"          usage:"path to the vault git working tree"`
	VaultName string `required:"true"  arg:"vault-name"          env:"VAULT_NAME"          usage:"vault name reported in responses"`
	// EventVaultName is the vault slug stamped on task change events as
	// lib.Task.Vault. It is a routing identity, not a display name: a consumer
	// that shares one topic across vaults owns a task only when this value equals
	// its own, and the agent-task-controller stamps its VAULT_NAME into the same
	// field. Empty falls back to VaultName, which is correct only where the
	// display name and the routing slug coincide.
	EventVaultName  string           `required:"false" arg:"event-vault-name"    env:"EVENT_VAULT_NAME"    usage:"vault slug stamped on task change events; must equal the controller's VAULT_NAME and the executor's TASK_VAULT. Empty = vault-name."`
	InstanceStage   string           `required:"false" arg:"instance-stage"      env:"INSTANCE_STAGE"      usage:"stage of this vault-api instance, reported as the instance_stage metric label (dev, prod). Distinct from the per-task stage label."                                      default:""`
	TasksDir        string           `required:"false" arg:"tasks-dir"           env:"TASKS_DIR"           usage:"task directory inside the vault"                                                                                                                                         default:"25 Tasks"`
	GoalsDir        string           `required:"false" arg:"goals-dir"           env:"GOALS_DIR"           usage:"goal directory inside the vault"                                                                                                                                         default:"24 Goals"`
	GatewaySecret   string           `required:"false" arg:"gateway-secret"      env:"GATEWAY_SECRET"      usage:"Shared secret required in X-Gateway-Secret for /api/v1/* requests. Empty = no auth."                                                                                                                                   display:"length"`
	GitRestURL      string           `required:"false" arg:"git-rest-url"        env:"GIT_REST_URL"        usage:"base URL of the vault's git-rest service; the write endpoints post whole files here"                                                                                     default:"http://vault-obsidian-personal:9090"`
	RefreshInterval libtime.Duration `required:"false" arg:"refresh-interval"    env:"REFRESH_INTERVAL"    usage:"how often to check the working tree for a new HEAD"                                                                                                                      default:"30s"`
	// StaleAfter bounds both freshness signals /readiness reports on: the index's
	// own verification and the working tree's last fetch from its remote. It is
	// ten times the 30s refresh interval, so it tolerates ten missed ticks before
	// the pod stops reporting ready. Zero disables the rule, which is what a local
	// run against a tree nothing fetches wants.
	StaleAfter libtime.Duration `required:"false" arg:"stale-after"         env:"STALE_AFTER"         usage:"how long the index or the tree may go unrefreshed before /readiness answers 503. 0 disables."                                                                            default:"5m"`
	// KafkaBrokers enables the change-event producer. Empty disables it, which is
	// what a local run wants: without a producer the service still serves reads and
	// writes, it just publishes nothing.
	KafkaBrokers string `required:"false" arg:"kafka-brokers"       env:"KAFKA_BROKERS"       usage:"Comma separated Kafka brokers for change events. Empty = publishing disabled."`
	// TopicPrefix selects the per-cluster topic: the event topic is
	// {prefix}-agent-task-v1-event, derived from lib.TaskV1SchemaID.
	TopicPrefix string `required:"false" arg:"topic-prefix"        env:"TOPIC_PREFIX"        usage:"Kafka topic prefix for change events (dev: develop, prod: master)"`
	// EventBaselinePath persists the last commit whose changes were published.
	// Empty keeps the baseline process-local, which means a restart adopts the
	// current HEAD and every commit pushed while the process was down is never
	// published — silently, since the instance still serves reads and looks
	// healthy. Set this to a path that survives a pod restart.
	EventBaselinePath string `required:"false" arg:"event-baseline-path" env:"EVENT_BASELINE_PATH" usage:"file persisting the last published commit so a restart resumes from it. Empty = process-local baseline, which silently skips changes pushed while the process was down."`
}

// eventVaultName is the vault identity this instance stamps on published task
// change events. It is deliberately separate from VaultName: VaultName is what
// the read API reports and what an operator sees, while this is the routing slug
// every consumer compares against. Where they differ, the event must carry the
// slug or the task is dropped as foreign by the executor that owns it.
func (a *application) eventVaultName() string {
	if a.EventVaultName != "" {
		return a.EventVaultName
	}
	return a.VaultName
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	metrics := vaultapi.NewMetrics(a.InstanceStage, a.VaultName)

	storageConfig := storage.DefaultConfig()
	storageConfig.TasksDir = a.TasksDir
	storageConfig.GoalsDir = a.GoalsDir
	store := vaultapi.NewCountingPageStorage(storage.NewPageStorage(storageConfig), metrics)

	index := vaultapi.NewIndex(vaultapi.Config{
		VaultPath:  a.VaultPath,
		VaultName:  a.VaultName,
		TasksDir:   a.TasksDir,
		GoalsDir:   a.GoalsDir,
		StaleAfter: a.StaleAfter.Duration(),
	}, metrics)
	if err := index.Build(ctx, store); err != nil {
		glog.Warningf("initial index build failed: %v", err)
	}

	// One producer for both paths: the write handlers publish through this handle and
	// the pull feed builds its emitter over the same one, so a change reaches the
	// topic once, published by whichever path saw it first.
	//
	// A Deferred rather than a built publisher: NewSyncProducer blocks while a broker
	// is unreachable, so constructing it here would make the read API's availability
	// depend on Kafka. publishTaskEvents installs the real one in the background, and
	// both paths report ErrNotReady until it does.
	taskPublisher := publisher.NewDeferred()

	factory, err := a.emitterFactory(ctx, taskPublisher)
	if err != nil {
		return err
	}
	// A persisted baseline is what lets a restart resume where the previous
	// process stopped. Without one the feed adopts the current HEAD, and every
	// commit pushed while the process was down is skipped and never published.
	baselineStore := vaultapi.NoopBaselineStore()
	if a.EventBaselinePath != "" {
		baselineStore = vaultapi.NewFileBaselineStore(a.EventBaselinePath)
	}
	initialBaseline, err := baselineStore.Load(ctx)
	if err != nil {
		// A baseline that cannot be read is not fatal: falling back to the
		// process-local behaviour is what the instance did before this seam
		// existed, and failing startup would take the read API down over it.
		glog.Warningf("load event baseline failed, adopting current HEAD: %v", err)
		initialBaseline = ""
	}

	// A baseline naming a commit this repository no longer holds cannot be diffed,
	// and that failure is not a broker error, so the feed would retry it forever.
	// Dropping it here — where adopting HEAD is still the safe fallback — is what
	// keeps a stale file from wedging publishing.
	initialBaseline = vaultapi.ResolveBaseline(
		ctx,
		vaultapi.NewGitDiffer(a.VaultPath),
		initialBaseline,
	)

	// Logged on success as well as on failure: the baseline a restart resumes from
	// is the only observable that separates a persisted baseline from a
	// process-local one, and a pod's log is readable where its filesystem is not.
	// "none" is a literal sentinel rather than an empty string so a reader can
	// never mistake an absent baseline for a commit.
	loadedBaseline := "none"
	if initialBaseline != "" {
		loadedBaseline = initialBaseline
	}
	glog.V(2).Infof("event baseline loaded: %s", loadedBaseline)

	// Always a Feed, even with a nil factory: the no-broker case then still owns a
	// baseline and simply publishes nothing, so the loop needs no nil checks.
	feed := vaultapi.NewFeedWithBaseline(factory, publisher.IsPermanent, initialBaseline)
	defer feed.Close()

	writer := gitrest.NewClient(a.GitRestURL, a.GatewaySecret, http.DefaultClient)
	// The ref client talks to the same git-rest instance on a different route.
	// It is separate from the writer because a claim write has a third outcome —
	// it lost the compare-and-swap — that a file write cannot report.
	refs := gitrest.NewRefClient(a.GitRestURL, a.GatewaySecret, http.DefaultClient)

	return run.CancelOnFirstFinish(ctx,
		a.refresh(index, store, feed, baselineStore),
		a.publishTaskEvents(taskPublisher),
		libhttp.NewServer(a.Listen, handler.NewHandler(
			ctx, index, a.GatewaySecret, a.VaultPath, a.eventVaultName(), writer,
			libtime.NewCurrentDateTime(), taskPublisher, refs,
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
		// Brokers-without-prefix is rejected by emitterFactory before this runs, so a
		// prefix is guaranteed here and this loop only has to survive an outage.
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

// emitterFactory validates the change-event config and returns a constructor, or
// nil when no broker is configured.
//
// It does not build the producer: the deferred handle owns that, because
// NewSyncProducer blocks on an unreachable broker. The factory only assembles an
// emitter over whatever publisher the handle holds, so the pull path and the write
// path share one producer rather than each building its own.
func (a *application) emitterFactory(
	ctx context.Context,
	taskPublisher publisher.TaskPublisher,
) (func(ctx context.Context) (vaultapi.Emitter, func() error, error), error) {
	if a.KafkaBrokers == "" {
		return nil, nil
	}
	// With brokers set but no prefix the topic would be "-agent-task-v1-event",
	// which is a config mistake rather than a transient failure, so it fails fast
	// and loudly instead of publishing somewhere unintended.
	if a.TopicPrefix == "" {
		return nil, errors.Errorf(ctx, "topic-prefix is required when kafka-brokers is set")
	}
	return func(ctx context.Context) (vaultapi.Emitter, func() error, error) {
		return vaultapi.NewEmitter(
			vaultapi.Config{
				VaultPath: a.VaultPath,
				// The emitter stamps Vault on every task event, so it takes the
				// routing slug rather than the reported display name. The index
				// above keeps a.VaultName for its responses.
				VaultName: a.eventVaultName(),
				TasksDir:  a.TasksDir,
				GoalsDir:  a.GoalsDir,
			},
			vaultapi.NewGitDiffer(a.VaultPath),
			taskPublisher,
		), nil, nil
	}, nil
}

// refresh rebuilds the index when the working tree's HEAD changes, or while the
// first successful build has not happened yet. A steady HEAD means no file is
// read, so the file-read counter stays flat between pulls; the tick still
// records a verification, which is what lets /readiness tell a running loop from
// a stopped one.
//
// After rebuilding it hands the new HEAD to the feed, which publishes one change
// event per task file that differs from the last published commit. That is what
// makes externally pushed changes visible without the controller's git scan.
func (a *application) refresh(
	index *vaultapi.Index,
	store storage.PageStorage,
	feed vaultapi.Feed,
	baselineStore vaultapi.BaselineStore,
) run.Func {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(a.RefreshInterval.Duration())
		defer ticker.Stop()
		// Persist only when the baseline actually moves, so a steady HEAD costs no
		// write. Seeded empty on purpose: one redundant save at startup is cheaper
		// than threading the loaded value through and getting the comparison wrong.
		savedBaseline := ""
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				current := vaultapi.ResolveHead(a.VaultPath)
				// Built(), not Readiness().Ready: Ready also fails on a stale tree,
				// and rebuilding every tick for a tree that has not moved would spend
				// the file reads the HEAD check exists to avoid.
				if !index.Built() || index.Head() != current {
					if err := index.Build(ctx, store); err != nil {
						glog.Warningf("index rebuild failed: %v", err)
					}
				} else {
					index.Verify()
				}
				// Publish after the rebuild, never before it. Building the producer
				// blocks on an unreachable broker (sarama dials and retries), so
				// publishing first would stall the index the read path serves for the
				// whole dial timeout — the coupling this change exists to remove.
				//
				// The returned baseline is persisted only after a batch published, which
				// is what makes a restart resume from the last published commit instead
				// of adopting HEAD and skipping everything pushed while the process was
				// down. It deliberately lags when a range was abandoned: persisting the
				// skip would make it permanent, so the persisted value stays before the
				// abandoned range and a restart re-attempts it.
				next := feed.Advance(ctx, current)
				persistBaseline(ctx, baselineStore, next, &savedBaseline)
			}
		}
	}
}

// persistBaseline records the baseline the feed will resume from, and only when it
// moved: a steady HEAD then costs no write. A save failure is logged rather than
// returned, because losing persistence degrades to the process-local baseline
// rather than breaking publishing.
func persistBaseline(
	ctx context.Context,
	store vaultapi.BaselineStore,
	next string,
	saved *string,
) {
	if next == "" || next == *saved {
		return
	}
	if err := store.Save(ctx, next); err != nil {
		glog.Warningf("persist event baseline failed: %v", err)
	}
	*saved = next
}
