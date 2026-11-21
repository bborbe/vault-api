// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	stderrors "errors"
	"time"

	"github.com/bborbe/vault-cli/pkg/ops"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"
)

// watchCoalesce is how long the refresh loop waits for a burst of filesystem
// events to go quiet before rebuilding. An editor save can emit several events
// for one logical change, and a rebuild reads the whole tree, so one rebuild per
// burst is what keeps a save from costing a rebuild per event.
const watchCoalesce = 200 * time.Millisecond

// WatchTargets returns the directories a local-mode instance watches: the same
// task, goal and topic directories the index reads. It is the vault-cli
// pkg/ops.WatchTarget shape, so the watcher is the shared library's rather than a
// second implementation.
func WatchTargets(cfg Config) []ops.WatchTarget {
	dirs := make([]ops.WatchDir, 0, 3)
	if cfg.TasksDir != "" {
		dirs = append(dirs, ops.WatchDir{Dir: cfg.TasksDir, Kind: "task"})
	}
	if cfg.GoalsDir != "" {
		dirs = append(dirs, ops.WatchDir{Dir: cfg.GoalsDir, Kind: "goal"})
	}
	if cfg.TopicsDir != "" {
		dirs = append(dirs, ops.WatchDir{Dir: cfg.TopicsDir, Kind: "topic"})
	}
	return []ops.WatchTarget{{
		VaultPath: cfg.VaultPath,
		VaultName: cfg.VaultName,
		WatchDirs: dirs,
	}}
}

// WatchRefresh runs the local-mode refresh loop: it rebuilds the index whenever
// the file watcher reports a change, with no git-HEAD gate, and falls back to a
// periodic full rebuild so a dropped filesystem event cannot leave the index
// stale forever. onHead is called with the working tree's HEAD after each
// rebuild, which is where the change-event feed advances its baseline.
//
// refreshInterval spaces the readiness heartbeat (index.Verify) and bounds the
// resync; a value <= 0 falls back to 30s.
func WatchRefresh(
	ctx context.Context,
	index *Index,
	store storage.PageStorage,
	targets []ops.WatchTarget,
	refreshInterval time.Duration,
	onHead func(head string),
) error {
	if refreshInterval <= 0 {
		refreshInterval = 30 * time.Second
	}

	changed := make(chan struct{}, 16)
	watchDone := startWatcher(ctx, targets, changed)
	// Joined on every exit path, so the goroutine is never left unjoined and its
	// error is never silently dropped.
	defer func() {
		if err := joinWatcher(watchDone); err != nil && !stderrors.Is(err, context.Canceled) {
			glog.Warningf("file watcher stopped: %v", err)
		}
	}()

	heartbeat := time.NewTicker(refreshInterval)
	defer heartbeat.Stop()
	// resync bounds how long a missed filesystem event can leave the index stale:
	// the watcher is the fast path, this is the guarantee.
	resync := time.NewTicker(10 * refreshInterval)
	defer resync.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-watchDone:
			// The watcher exited on its own (a closed event channel). Keep serving:
			// the resync ticker still bounds staleness, just far more slowly than a
			// live watch would. watchDone becomes nil, disabling this case.
			watchDone = nil
			if err != nil && !stderrors.Is(err, context.Canceled) {
				glog.Warningf("file watcher stopped, relying on periodic resync: %v", err)
			}
		case <-changed:
			if !waitQuiet(ctx, changed) {
				return nil
			}
			rebuildIndex(ctx, index, store, onHead)
		case <-heartbeat.C:
			index.Verify()
		case <-resync.C:
			rebuildIndex(ctx, index, store, onHead)
		}
	}
}

// startWatcher spawns the shared-library file watcher and returns a channel that
// carries its exit. A change sends one signal on changed, dropping it when the
// buffer is full so a burst cannot stall the watcher.
func startWatcher(
	ctx context.Context,
	targets []ops.WatchTarget,
	changed chan<- struct{},
) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- ops.NewWatchOperation().Execute(ctx, targets, func(ops.WatchEvent) error {
			select {
			case changed <- struct{}{}:
			default:
			}
			return nil
		})
	}()
	return done
}

// joinWatcher waits for the watcher goroutine to return. A nil channel (the
// watcher already exited and was consumed) returns immediately.
func joinWatcher(done <-chan error) error {
	if done == nil {
		return nil
	}
	return <-done
}

// rebuildIndex rebuilds the index and advances the change-event baseline. A
// failed build leaves the previous index serving and is logged, not fatal.
func rebuildIndex(
	ctx context.Context,
	index *Index,
	store storage.PageStorage,
	onHead func(string),
) {
	if err := index.Build(ctx, store); err != nil {
		glog.Warningf("watch index rebuild failed: %v", err)
		return
	}
	if onHead != nil {
		onHead(ResolveHead(index.cfg.VaultPath))
	}
}

// waitQuiet blocks until no change has arrived for watchCoalesce, collapsing a
// save's event burst into one rebuild. It returns false when ctx is cancelled
// first, so the caller can join the watcher and exit.
func waitQuiet(ctx context.Context, changed <-chan struct{}) bool {
	quiet := time.NewTimer(watchCoalesce)
	defer quiet.Stop()
	for {
		select {
		case <-changed:
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(watchCoalesce)
		case <-quiet.C:
			return true
		case <-ctx.Done():
			return false
		}
	}
}
