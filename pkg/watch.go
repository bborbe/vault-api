// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
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
	go func() {
		// The watcher runs until ctx is cancelled; its error is a closed channel or
		// a cancelled context, neither of which the caller can act on. A real
		// watcher failure surfaces as a stale index, which the resync below bounds.
		_ = ops.NewWatchOperation().Execute(ctx, targets, func(ops.WatchEvent) error {
			select {
			case changed <- struct{}{}:
			default:
			}
			return nil
		})
	}()

	rebuild := func() {
		if err := index.Build(ctx, store); err != nil {
			glog.Warningf("watch index rebuild failed: %v", err)
			return
		}
		if onHead != nil {
			onHead(ResolveHead(index.cfg.VaultPath))
		}
	}

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
		case <-changed:
			quiet := time.NewTimer(watchCoalesce)
		drain:
			for {
				select {
				case <-changed:
					quiet.Reset(watchCoalesce)
				case <-quiet.C:
					break drain
				case <-ctx.Done():
					quiet.Stop()
					return nil
				}
			}
			quiet.Stop()
			rebuild()
		case <-heartbeat.C:
			index.Verify()
		case <-resync.C:
			rebuild()
		}
	}
}
