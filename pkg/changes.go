// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	stderrors "errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// ChangeStatus is git's single-letter status for one path in a HEAD-to-HEAD diff.
type ChangeStatus string

const (
	// ChangeAdded marks a path that exists at the new commit and not at the old one.
	ChangeAdded ChangeStatus = "A"
	// ChangeModified marks a path whose blob differs between the two commits.
	ChangeModified ChangeStatus = "M"
	// ChangeDeleted marks a path that existed at the old commit and not at the new one.
	ChangeDeleted ChangeStatus = "D"
)

// Change is one path git reported as different between two commits.
type Change struct {
	Path   string
	Status ChangeStatus
}

// ErrNotAtRef reports that a ref resolves but does not carry the requested path.
//
// It is deliberately distinct from a failure about the ref itself, because the
// two mean opposite things to a caller waiting for a write to land: "not
// committed yet" is the expected state before the next git-rest flush, while an
// unresolvable ref is a configuration fault. Collapsing them would make a typo in
// a ref name indistinguishable from every write being dropped.
var ErrNotAtRef = stderrors.New("path is not present at the ref")

//counterfeiter:generate -o ../mocks/differ.go --fake-name Differ . Differ

// Differ reports the files that changed between two commits, and reads a file as
// it existed at a commit. The second half is what makes deletions publishable:
// the bytes of a deleted task are gone from the working tree, so the only place
// its identifier still exists is the old commit.
type Differ interface {
	// Diff returns the paths that differ between oldHead and newHead.
	Diff(ctx context.Context, oldHead string, newHead string) ([]Change, error)
	// ReadAt returns the content of path as it existed at head.
	ReadAt(ctx context.Context, head string, path string) ([]byte, error)
	// Exists reports whether commit is present in the repository. A persisted
	// baseline can name a commit the tree no longer holds — a re-cloned volume, a
	// force-push, a gc — and Diff then fails in a way that is not a broker error,
	// so the feed treats it as transient and retries forever. Callers use this to
	// drop such a baseline before it can wedge publishing.
	Exists(ctx context.Context, commit string) (bool, error)
}

// NewGitDiffer returns a Differ that runs git inside vaultPath.
func NewGitDiffer(vaultPath string) Differ {
	return &gitDiffer{vaultPath: vaultPath}
}

type gitDiffer struct {
	vaultPath string
}

// Diff uses diff-tree rather than diff. The vault volume is mounted read-only
// and `git diff` refreshes the index, which needs a write; diff-tree compares two
// committed trees and touches no working-tree state.
//
// -z is what makes the paths trustworthy: without it git C-quotes any path holding
// a quote, a backslash or a control character, and a quoted path matches neither
// the tasks-dir prefix nor the .md suffix, so that task would be skipped silently.
func (d *gitDiffer) Diff(ctx context.Context, oldHead string, newHead string) ([]Change, error) {
	started := time.Now()
	out, err := d.git(
		ctx,
		"diff-tree",
		"-r",
		"--no-commit-id",
		"--name-status",
		"-z",
		oldHead,
		newHead,
	)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "diff %s..%s failed", oldHead, newHead)
	}
	changes, err := parseNameStatus(ctx, out)
	if err != nil {
		return nil, err
	}
	// One line per batch, not per path: this runs on every pull and a per-file line
	// would drown the log on a large diff.
	glog.V(2).Infof(
		"git diff-tree %s..%s: %d path(s), %d bytes, %s",
		oldHead,
		newHead,
		len(changes),
		len(out),
		time.Since(started),
	)
	return changes, nil
}

// ReadAt returns a path's content at the given commit.
//
// A ref that resolves but does not carry the path returns ErrNotAtRef, so a
// caller can tell "this write has not been committed yet" from "this ref is
// wrong". Only the first is an expected state: git-rest commits on a cadence, so
// every write spends up to one window absent from the committed history.
func (d *gitDiffer) ReadAt(ctx context.Context, head string, path string) ([]byte, error) {
	out, err := d.git(ctx, "show", head+":"+path)
	if err != nil {
		if exists, existsErr := d.Exists(ctx, head); existsErr == nil && exists {
			return nil, errors.Wrapf(ctx, ErrNotAtRef, "read %s at %s", path, head)
		}
		return nil, errors.Wrapf(ctx, err, "read %s at %s failed", path, head)
	}
	return []byte(out), nil
}

// Exists reports whether commit resolves to a commit object in the repository.
//
// rev-parse --verify --quiet answers exit status 1, and prints nothing, for an
// object it cannot resolve — that is the commit being absent, which is a fact the
// caller acts on rather than a failure. Any other non-zero status is a broken or
// unreadable repository and is returned as an error, so a misconfigured git-dir is
// never silently reported as "the baseline is gone".
func (d *gitDiffer) Exists(ctx context.Context, commit string) (bool, error) {
	if commit == "" {
		return false, nil
	}
	// #nosec G204 -- the binary is the literal "git" and the commit is a SHA the
	// caller read out of this same repository. No request value reaches this call.
	cmd := exec.CommandContext(
		ctx,
		"git",
		d.baseArgs(
			"rev-parse",
			"--verify",
			"--quiet",
			commit+"^{commit}",
		)...,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if stderrors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, errors.Wrapf(
			ctx,
			err,
			"git rev-parse %s: %s",
			commit,
			strings.TrimSpace(stderr.String()),
		)
	}
	return true, nil
}

// baseArgs prefixes every git call with the vault's git-dir and work-tree. The
// path is passed explicitly rather than inherited from the process working
// directory: the pod's cwd is not the vault, and a bare `git` would walk up from
// it and fail or, worse, read another repository.
func (d *gitDiffer) baseArgs(args ...string) []string {
	base := make([]string, 0, 4+len(args))
	base = append(
		base,
		"--git-dir",
		filepath.Join(d.vaultPath, ".git"),
		"--work-tree",
		d.vaultPath,
	)
	return append(base, args...)
}

func (d *gitDiffer) git(ctx context.Context, args ...string) (string, error) {
	// #nosec G204 -- the binary is the literal "git" and every argument is built
	// here from the configured vault path or from a commit SHA the caller read out
	// of that same repository. No request value reaches this call.
	cmd := exec.CommandContext(ctx, "git", d.baseArgs(args...)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.Wrapf(
			ctx,
			err,
			"git %s: %s",
			strings.Join(args, " "),
			strings.TrimSpace(stderr.String()),
		)
	}
	return string(out), nil
}

// parseNameStatus turns `git diff-tree --name-status -z` output into changes.
//
// -z separates every field with NUL, so a path containing a quote, a backslash or a
// newline arrives verbatim instead of C-quoted. A rename or copy entry carries two
// paths and becomes the delete+add pair it semantically is; the vault is never
// diffed with -M, so that is a guard rather than a live case.
func parseNameStatus(ctx context.Context, out string) ([]Change, error) {
	fields := strings.Split(out, "\x00")
	changes := make([]Change, 0, len(fields)/2)
	for i := 0; i < len(fields); {
		status := strings.TrimSpace(fields[i])
		if status == "" {
			i++
			continue
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if i+2 >= len(fields) {
				return nil, errors.Errorf(
					ctx,
					"diff-tree entry %q has fewer than two paths",
					status,
				)
			}
			changes = append(
				changes,
				Change{Path: fields[i+1], Status: ChangeDeleted},
				Change{Path: fields[i+2], Status: ChangeAdded},
			)
			i += 3
			continue
		}
		if i+1 >= len(fields) {
			return nil, errors.Errorf(ctx, "diff-tree entry %q has no path", status)
		}
		if fields[i+1] == "" {
			return nil, errors.Errorf(ctx, "diff-tree entry %q has an empty path", status)
		}
		changes = append(changes, Change{Path: fields[i+1], Status: statusOf(status)})
		i += 2
	}
	return changes, nil
}

// statusOf maps git's status letter onto the three cases the emitter acts on.
// A typechange is a content change as far as a consumer is concerned, so anything
// that is not an add or a delete is reported as a modification rather than
// dropped silently.
func statusOf(letter string) ChangeStatus {
	switch ChangeStatus(strings.TrimSpace(letter)) {
	case ChangeAdded:
		return ChangeAdded
	case ChangeDeleted:
		return ChangeDeleted
	case ChangeModified:
		return ChangeModified
	default:
		glog.V(2).Infof("treating diff status %q as a modification", letter)
		return ChangeModified
	}
}
