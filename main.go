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
	"github.com/bborbe/vault-cli/pkg/config"
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
	TopicsDir       string           `required:"false" arg:"topics-dir"          env:"TOPICS_DIR"          usage:"topic directory inside the vault"                                                                                                                                        default:"23 Topics"`
	GatewaySecret   string           `required:"false" arg:"gateway-secret"      env:"GATEWAY_SECRET"      usage:"Shared secret required in X-Gateway-Secret for /api/v1/* requests. Empty = no auth."                                                                                                                                   display:"length"`
	GitRestURL      string           `required:"false" arg:"git-rest-url"        env:"GIT_REST_URL"        usage:"base URL of the vault's git-rest service; the write endpoints post whole files here"                                                                                     default:"http://vault-obsidian-personal:9090"`
	RefreshInterval libtime.Duration `required:"false" arg:"refresh-interval"    env:"REFRESH_INTERVAL"    usage:"how often to check the working tree for a new HEAD"                                                                                                                      default:"30s"`
	// Watch drives the index from a filesystem watch instead of the git-HEAD
	// check. It is the local-mode switch: on a laptop nothing commits on every
	// edit, so a HEAD-gated refresh would lag edits by git-ai-sync's interval. In
	// this mode a file write is indexed within a second, with no commit.
	Watch bool `required:"false" arg:"watch"               env:"WATCH"               usage:"drive the index from a filesystem watch instead of a git-HEAD check (local mode)"                                                                                        default:"false"`
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
	// {prefix}-agent-task-v1-event, derived from lib.TaskV1SchemaID, and an empty
	// prefix yields exactly "agent-task-v1-event" with no leading dash.
	TopicPrefix string `required:"false" arg:"topic-prefix"        env:"TOPIC_PREFIX"        usage:"Kafka topic prefix for change events; empty = no prefix segment (one cluster per stage)"`
	// EventBaselinePath persists the last commit whose changes were published.
	// Empty keeps the baseline process-local, which means a restart adopts the
	// current HEAD and every commit pushed while the process was down is never
	// published — silently, since the instance still serves reads and looks
	// healthy. Set this to a path that survives a pod restart.
	EventBaselinePath string `required:"false" arg:"event-baseline-path" env:"EVENT_BASELINE_PATH" usage:"file persisting the last published commit so a restart resumes from it. Empty = process-local baseline, which silently skips changes pushed while the process was down."`
	// MultiVault adds every other vault in vault-cli's config to the list
	// queries, the way vault-ui serves every configured vault. It is read-only for
	// those vaults: lookups by id, writes and change events stay with
	// VaultPath/VaultName, the primary. Off by default, so every cluster
	// deployment keeps serving exactly its one vault.
	MultiVault bool `required:"false" arg:"multi-vault"         env:"MULTI_VAULT"         usage:"also serve every other vault in vault-cli's config on the list endpoints (read-only); off = single-vault, the cluster mode"                                              default:"false"`
	// VaultCLIConfig points at a vault-cli config.yaml other than the default.
	// It exists so a test or a probe can name a broken vault without touching the
	// live config, which vault-cli, vault-ui and both wrappers also read.
	VaultCLIConfig string `required:"false" arg:"vault-cli-config"    env:"VAULT_CLI_CONFIG"    usage:"path to vault-cli's config.yaml for -multi-vault; empty = vault-cli's default (~/.config/vault-cli/config.yaml)"`
}

// attachedVault is one secondary vault served in multi-vault mode: its own index
// and its own store, because each vault keeps its pages in its own directories.
type attachedVault struct {
	index *vaultapi.Index
	store storage.PageStorage
	cfg   vaultapi.Config
}

// attachedVaults builds one read-only index per vault in vault-cli's config,
// skipping the primary vault, which the main index already serves.
//
// A vault whose index fails to build is still attached: it is logged with the
// fixed unavailable token now, reported by /readiness, and retried by its refresh
// loop. Dropping it instead would make its rows vanish with nothing saying so.
func (a *application) attachedVaults(ctx context.Context) ([]attachedVault, error) {
	if !a.MultiVault {
		return nil, nil
	}
	vaults, err := config.NewLoader(a.VaultCLIConfig).GetAllVaults(ctx)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "load vault-cli config %q", a.VaultCLIConfig)
	}
	attached := make([]attachedVault, 0, len(vaults))
	for _, vault := range vaults {
		if vault.Name == a.VaultName || vault.Path == "" {
			continue
		}
		// Only task-bearing vaults: one that declares no tasks_dir is a knowledge
		// vault, and GetTasksDir's "Tasks" default names a directory it does not
		// have, so attaching it would report a permanent unavailable that means
		// nothing. This is the same population `vault-cli config list` consumers
		// select on (tasks_dir present).
		if vault.TasksDir == "" {
			continue
		}
		cfg := vaultapi.Config{
			VaultPath: vault.Path,
			VaultName: vault.Name,
			TasksDir:  vault.GetTasksDir(),
			GoalsDir:  vault.GetGoalsDir(),
			TopicsDir: vault.GetTopicsDir(),
		}
		storageConfig := storage.DefaultConfig()
		storageConfig.TasksDir = cfg.TasksDir
		storageConfig.GoalsDir = cfg.GoalsDir
		storageConfig.TopicsDir = cfg.TopicsDir
		extra := attachedVault{
			index: vaultapi.NewIndex(cfg, vaultapi.NewNoopMetrics()),
			store: storage.NewPageStorage(storageConfig),
			cfg:   cfg,
		}
		if err := extra.index.Build(ctx, extra.store); err != nil {
			glog.Warningf("%s: %v", vaultapi.UnavailableLine(vault.Name), err)
		}
		attached = append(attached, extra)
	}
	glog.V(1).Infof("multi-vault: attached %d vault(s) beside %s", len(attached), a.VaultName)
	return attached, nil
}

// attachedRefresh keeps one attached vault's index current, and publishes no
// change events: events stay with the primary.
//
// ⚠️ It never uses the filesystem watch, even in -watch mode. On macOS the watch
// holds one descriptor per watched file, so watching every configured vault
// exhausted the process's descriptor limit and the HTTP server stopped
// accepting connections ("accept: too many open files") — taking the primary
// vault's API down with it. Measured 2026-11-23 on a 13-vault probe. The HEAD
// check costs one stat per tick; the vaults commit through git-ai-sync every
// minute or two, which is well inside the flagged tick's own cadence.
//
// A tree with no resolvable HEAD (not a git checkout) cannot use that gate, so
// it is rebuilt on every tick rather than never.
func (a *application) attachedRefresh(extra attachedVault) run.Func {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(a.RefreshInterval.Duration())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				current := vaultapi.ResolveHead(extra.cfg.VaultPath)
				if current != "" && extra.index.Built() && extra.index.Head() == current {
					extra.index.Verify()
					continue
				}
				if err := extra.index.Build(ctx, extra.store); err != nil {
					glog.Warningf("%s: %v", vaultapi.UnavailableLine(extra.cfg.VaultName), err)
				}
			}
		}
	}
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
	storageConfig.TopicsDir = a.TopicsDir
	store := vaultapi.NewCountingPageStorage(storage.NewPageStorage(storageConfig), metrics)

	index := vaultapi.NewIndex(vaultapi.Config{
		VaultPath:  a.VaultPath,
		VaultName:  a.VaultName,
		TasksDir:   a.TasksDir,
		GoalsDir:   a.GoalsDir,
		TopicsDir:  a.TopicsDir,
		StaleAfter: a.StaleAfter.Duration(),
	}, metrics)
	if err := index.Build(ctx, store); err != nil {
		glog.Warningf("initial index build failed: %v", err)
	}

	attached, err := a.attachedVaults(ctx)
	if err != nil {
		return errors.Wrap(ctx, err, "attach vaults")
	}
	for _, extra := range attached {
		index.Attach(extra.index)
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

	factory := a.emitterFactory(taskPublisher)
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
	glog.V(1).Infof("event baseline loaded: %s", loadedBaseline)

	// Always a Feed, even with a nil factory: the no-broker case then still owns a
	// baseline and simply publishes nothing, so the loop needs no nil checks.
	feed := vaultapi.NewFeedWithBaseline(factory, publisher.IsPermanent, initialBaseline)
	defer feed.Close()

	writer := gitrest.NewClient(a.GitRestURL, a.GatewaySecret, http.DefaultClient)
	// The ref client talks to the same git-rest instance on a different route.
	// It is separate from the writer because a claim write has a third outcome —
	// it lost the compare-and-swap — that a file write cannot report.
	refs := gitrest.NewRefClient(a.GitRestURL, a.GatewaySecret, http.DefaultClient)

	runners := []run.Func{
		a.refresh(index, store, feed, baselineStore),
		a.publishTaskEvents(taskPublisher),
		libhttp.NewServer(a.Listen, handler.NewHandler(
			ctx, index, store, a.GatewaySecret, a.VaultPath, a.TasksDir, a.eventVaultName(), writer,
			libtime.NewCurrentDateTime(), taskPublisher, refs,
		)),
	}
	for _, extra := range attached {
		runners = append(runners, a.attachedRefresh(extra))
	}
	return run.CancelOnFirstFinish(ctx, runners...)
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
		// The prefix may legitimately be empty (one cluster per stage), so this loop
		// only has to survive a broker outage.
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

// emitterFactory returns a constructor for the change-event emitter, or nil when
// no broker is configured.
//
// It does not build the producer: the deferred handle owns that, because
// NewSyncProducer blocks on an unreachable broker. The factory only assembles an
// emitter over whatever publisher the handle holds, so the pull path and the write
// path share one producer rather than each building its own.
func (a *application) emitterFactory(
	taskPublisher publisher.TaskPublisher,
) func(ctx context.Context) (vaultapi.Emitter, func() error, error) {
	if a.KafkaBrokers == "" {
		return nil
	}
	// An empty prefix is a legitimate configuration, not a mistake: cqrs's
	// base.TopicPrefix renders "" as no prefix segment and no leading dash, so the
	// topic is exactly "agent-task-v1-event". A cluster that serves a single stage
	// needs precisely that — the octopus agent lane runs one Strimzi cluster per
	// stage and its executor consumes "agent-task-v1-event" with TOPIC_PREFIX empty.
	// Requiring a prefix here would publish to "<prefix>-agent-task-v1-event", a
	// topic nothing consumes, which reads as wired and delivers nothing.
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
				TopicsDir: a.TopicsDir,
			},
			vaultapi.NewGitDiffer(a.VaultPath),
			taskPublisher,
		), nil, nil
	}
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
	if a.Watch {
		return a.watchRefresh(index, store, feed, baselineStore)
	}
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

// watchRefresh is the local-mode refresh: the filesystem watch drives rebuilds,
// with no git-HEAD gate, so an edit is visible without a commit. The change-event
// feed still advances on each rebuild, which keeps a local instance's baseline
// moving even though nothing pushes to it.
func (a *application) watchRefresh(
	index *vaultapi.Index,
	store storage.PageStorage,
	feed vaultapi.Feed,
	baselineStore vaultapi.BaselineStore,
) run.Func {
	return func(ctx context.Context) error {
		savedBaseline := ""
		onHead := func(head string) {
			next := feed.Advance(ctx, head)
			persistBaseline(ctx, baselineStore, next, &savedBaseline)
		}
		return vaultapi.WatchRefresh(
			ctx,
			index,
			store,
			vaultapi.WatchTargets(vaultapi.Config{
				VaultPath: a.VaultPath,
				VaultName: a.VaultName,
				TasksDir:  a.TasksDir,
				GoalsDir:  a.GoalsDir,
				TopicsDir: a.TopicsDir,
			}),
			a.RefreshInterval.Duration(),
			onHead,
		)
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
		// saved is left alone deliberately: recording a failed write as saved would
		// suppress the retry on every later tick, and on a quiescent vault the file
		// would stay at the previous commit until the baseline moved again.
		glog.Warningf("persist event baseline failed: %v", err)
		return
	}
	*saved = next
}
