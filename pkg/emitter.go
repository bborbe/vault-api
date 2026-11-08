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
	ordered := deletionsFirst(changes)
	// Two passes: every tombstone of the batch goes out before any upsert. A
	// tombstone published after an upsert of the same key removes that task from
	// a compacted view — a rename (delete + add sharing one identifier), two files
	// swapping identifiers, or an added file taking over a key another file just
	// left would all lose a live task if the passes were interleaved per file.
	for _, change := range ordered {
		if err := e.step(ctx, change, oldHead, newHead, e.emitTombstone); err != nil {
			return err
		}
	}
	for _, change := range ordered {
		if err := e.step(ctx, change, oldHead, newHead, e.emitUpsert); err != nil {
			return err
		}
	}
	return nil
}

// step runs one pass's action for one change, skipping non-task paths and
// stopping promptly on shutdown: each action publishes, which can block on the
// broker, and draining the whole diff after cancellation helps no one.
func (e *emitter) step(
	ctx context.Context,
	change Change,
	oldHead string,
	newHead string,
	action func(context.Context, Change, string, string) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !e.isTaskPath(change.Path) {
		return nil
	}
	return action(ctx, change, oldHead, newHead)
}

// emitTombstone publishes the tombstones a change implies: the identifier of a
// deleted file, and the previous identifier of a modified file whose identifier
// changed. An added file implies none.
func (e *emitter) emitTombstone(
	ctx context.Context,
	change Change,
	oldHead string,
	newHead string,
) error {
	if change.Status != ChangeDeleted && change.Status != ChangeModified {
		return nil
	}
	task, ok, err := e.readTask(ctx, change, oldHead, newHead)
	if err != nil || !ok {
		return err
	}
	if change.Status == ChangeModified {
		return e.tombstoneOldKey(ctx, change, oldHead, task.TaskIdentifier)
	}
	if err := e.publisher.PublishDeleted(ctx, task.TaskIdentifier); err != nil {
		return errors.Wrapf(ctx, err, "publish %s %s failed", change.Status, change.Path)
	}
	return nil
}

// emitUpsert publishes the current state of an added or modified file.
func (e *emitter) emitUpsert(
	ctx context.Context,
	change Change,
	oldHead string,
	newHead string,
) error {
	if change.Status == ChangeDeleted {
		return nil
	}
	task, ok, err := e.readTask(ctx, change, oldHead, newHead)
	if err != nil || !ok {
		return err
	}
	if err := e.publisher.PublishChanged(ctx, task); err != nil {
		return errors.Wrapf(ctx, err, "publish %s %s failed", change.Status, change.Path)
	}
	return nil
}

// readTask reads and parses the side of a change its event describes. It
// reports ok=false, with no error, for content that does not parse.
func (e *emitter) readTask(
	ctx context.Context,
	change Change,
	oldHead string,
	newHead string,
) (lib.Task, bool, error) {
	content, err := e.content(ctx, change, oldHead, newHead)
	if err != nil {
		// A read failure is transient — the object store is local, but a concurrent
		// gc or an interrupted fetch can lose it for a moment. Return it so the
		// caller keeps its baseline and retries the same diff, rather than advancing
		// past an event that was never published.
		return lib.Task{}, false, errors.Wrapf(
			ctx, err, "read %s for %s failed", change.Path, change.Status,
		)
	}
	task, err := e.taskFromContent(ctx, change.Path, content)
	if err != nil {
		// Unparseable content is permanent: the same bytes will not parse on the
		// next tick, so skip it and let the batch finish.
		glog.Warningf("skip %s %s: %v", change.Status, change.Path, err)
		return lib.Task{}, false, nil
	}
	return task, true, nil
}

// tombstoneOldKey publishes a tombstone for the identifier a modified file
// carried at oldHead when it differs from the one it carries now.
//
// The topic is compacted and keyed by task identifier, so an address change —
// a slug-addressed file gaining a task_identifier, or a readdress — would
// otherwise leave the old key's last record live forever: every later event
// lands under the new key and nothing supersedes the old one. A consumer view
// replaying the topic then holds a ghost task at whatever state it last had
// (measured 2026-11-08: one such ghost drove 650+ re-spawns of a completed task).
//
// An old side that does not parse yields no identifier and therefore no
// tombstone: the upsert still has to go out, and the same bytes will not parse
// on a retry either.
func (e *emitter) tombstoneOldKey(
	ctx context.Context,
	change Change,
	oldHead string,
	current lib.TaskIdentifier,
) error {
	content, err := e.differ.ReadAt(ctx, oldHead, change.Path)
	if err != nil {
		return errors.Wrapf(ctx, err, "read previous %s failed", change.Path)
	}
	previous, err := e.taskFromContent(ctx, change.Path, content)
	if err != nil || previous.TaskIdentifier == current {
		return nil
	}
	if err := e.publisher.PublishDeleted(ctx, previous.TaskIdentifier); err != nil {
		return errors.Wrapf(
			ctx, err, "publish tombstone for previous key %s of %s failed",
			previous.TaskIdentifier, change.Path,
		)
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
