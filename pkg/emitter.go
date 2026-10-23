// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	lib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"

	"github.com/bborbe/vault-api/pkg/publisher"
)

//counterfeiter:generate -o ../mocks/emitter.go --fake-name Emitter . Emitter

// Emitter publishes one change event per task file that differs between two
// commits.
//
// It is the pull-side producer: the write handlers cover mutations vault-api
// itself performed, and this covers everything else — Obsidian on the Mac,
// vault-ui, git-ai-sync — which is what lets the controller's git scan retire.
// Only task files are published; a goals-only pull emits nothing.
type Emitter interface {
	// Emit publishes the change events for oldHead..newHead. A read or publish
	// failure is returned so the caller can leave its baseline unadvanced and retry
	// the same diff; content that does not parse is skipped and logged, because the
	// same bytes will not become parseable on the next tick.
	Emit(ctx context.Context, oldHead string, newHead string) error
}

// NewEmitter creates an Emitter over the vault described by cfg.
func NewEmitter(cfg Config, differ Differ, taskPublisher publisher.TaskPublisher) Emitter {
	// TasksDir arrives from a CLI flag and is compared against git paths, which are
	// always slash-separated and clean. Normalizing once here means a trailing slash
	// or a "./" prefix cannot silently stop every path from matching.
	cfg.TasksDir = path.Clean(filepath.ToSlash(strings.TrimSpace(cfg.TasksDir)))
	return &emitter{cfg: cfg, differ: differ, publisher: taskPublisher}
}

type emitter struct {
	cfg       Config
	differ    Differ
	publisher publisher.TaskPublisher
}

func (e *emitter) Emit(ctx context.Context, oldHead string, newHead string) error {
	// A missing baseline means there is no previous commit to diff against — the
	// startup case, where every task already exists and nothing has changed.
	if oldHead == "" || newHead == "" || oldHead == newHead {
		return nil
	}
	changes, err := e.differ.Diff(ctx, oldHead, newHead)
	if err != nil {
		return err
	}
	for _, change := range deletionsFirst(changes) {
		// Each iteration publishes, which can block on the broker; stop promptly
		// when the process is shutting down rather than draining the whole diff.
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.isTaskPath(change.Path) {
			continue
		}
		if err := e.emitOne(ctx, change, oldHead, newHead); err != nil {
			return err
		}
	}
	return nil
}

func (e *emitter) emitOne(
	ctx context.Context,
	change Change,
	oldHead string,
	newHead string,
) error {
	content, err := e.content(ctx, change, oldHead, newHead)
	if err != nil {
		// A read failure is transient — the object store is local, but a concurrent
		// gc or an interrupted fetch can lose it for a moment. Return it so the
		// caller keeps its baseline and retries the same diff, rather than advancing
		// past an event that was never published.
		return errors.Wrapf(ctx, err, "read %s for %s failed", change.Path, change.Status)
	}
	task, err := e.taskFromContent(ctx, change.Path, content)
	if err != nil {
		// Unparseable content is permanent: the same bytes will not parse on the
		// next tick, so skip it and let the batch finish.
		glog.Warningf("skip %s %s: %v", change.Status, change.Path, err)
		return nil
	}
	if change.Status == ChangeDeleted {
		if err := e.publisher.PublishDeleted(ctx, task.TaskIdentifier); err != nil {
			return errors.Wrapf(ctx, err, "publish %s %s failed", change.Status, change.Path)
		}
		return nil
	}
	if err := e.publisher.PublishChanged(ctx, task); err != nil {
		return errors.Wrapf(ctx, err, "publish %s %s failed", change.Status, change.Path)
	}
	return nil
}

// content reads the bytes the event describes. Both sides are read from the
// commit the diff names, never from the working tree: the tree can advance again
// between the diff and this read, and an event must describe the range it claims.
func (e *emitter) content(
	ctx context.Context,
	change Change,
	oldHead string,
	newHead string,
) ([]byte, error) {
	if change.Status == ChangeDeleted {
		return e.differ.ReadAt(ctx, oldHead, change.Path)
	}
	return e.differ.ReadAt(ctx, newHead, change.Path)
}

// deletionsFirst orders tombstones ahead of upserts. A rename arrives as a delete
// of the old path plus an add of the new one, and both resolve to the same task
// identifier; publishing the upsert first and the tombstone second would remove
// the task from a compacted consumer view.
func deletionsFirst(changes []Change) []Change {
	ordered := make([]Change, 0, len(changes))
	for _, change := range changes {
		if change.Status == ChangeDeleted {
			ordered = append(ordered, change)
		}
	}
	for _, change := range changes {
		if change.Status != ChangeDeleted {
			ordered = append(ordered, change)
		}
	}
	return ordered
}

func (e *emitter) isTaskPath(path string) bool {
	if !strings.HasSuffix(path, ".md") {
		return false
	}
	return strings.HasPrefix(path, e.cfg.TasksDir+"/")
}

// taskFromContent mirrors the controller's own conversion, so a consumer sees the
// same payload shape from either producer: the frontmatter map verbatim, the body
// without its frontmatter block, and the vault slug stamped on the event.
func (e *emitter) taskFromContent(
	ctx context.Context,
	path string,
	content []byte,
) (lib.Task, error) {
	frontmatter, err := storage.ParseFrontmatterMap(ctx, content)
	if err != nil {
		return lib.Task{}, err
	}
	id := taskIdentifierFrom(frontmatter, path)
	if id == "" {
		return lib.Task{}, errors.Errorf(ctx, "no task identifier derivable from %s", path)
	}
	return lib.Task{
		TaskIdentifier: lib.TaskIdentifier(id),
		Vault:          e.cfg.VaultName,
		Frontmatter:    lib.TaskFrontmatter(frontmatter),
		Content:        lib.TaskContent(body(content)),
	}, nil
}

// taskIdentifierFrom prefers the file's own task_identifier and falls back to the
// path slug, the same derivation the index uses, so a published event and a
// served task agree on identity for a file that carries no identifier.
func taskIdentifierFrom(frontmatter map[string]any, path string) string {
	if value, ok := frontmatter["task_identifier"]; ok {
		if id, ok := value.(string); ok && strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return slugify(strings.TrimSuffix(filepath.Base(path), ".md"))
}

// body returns content with its frontmatter block removed.
func body(content []byte) string {
	s := string(content)
	const delimiter = "---"
	if !strings.HasPrefix(s, delimiter) {
		return s
	}
	rest := strings.TrimPrefix(s, delimiter)
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	index := strings.Index(rest, "\n"+delimiter)
	if index == -1 {
		return s
	}
	rest = rest[index+len("\n"+delimiter):]
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	return rest
}
