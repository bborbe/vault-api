// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

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

// Differ reports the files that changed between two commits, and reads a file as
// it existed at a commit. The second half is what makes deletions publishable:
// the bytes of a deleted task are gone from the working tree, so the only place
// its identifier still exists is the old commit.
type Differ interface {
	// Diff returns the paths that differ between oldHead and newHead.
	Diff(ctx context.Context, oldHead string, newHead string) ([]Change, error)
	// ReadAt returns the content of path as it existed at head.
	ReadAt(ctx context.Context, head string, path string) ([]byte, error)
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
func (d *gitDiffer) Diff(ctx context.Context, oldHead string, newHead string) ([]Change, error) {
	out, err := d.git(ctx, "diff-tree", "-r", "--no-commit-id", "--name-status", oldHead, newHead)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "diff %s..%s failed", oldHead, newHead)
	}
	return parseNameStatus(ctx, out)
}

// ReadAt returns a path's content at the given commit.
func (d *gitDiffer) ReadAt(ctx context.Context, head string, path string) ([]byte, error) {
	out, err := d.git(ctx, "show", head+":"+path)
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "read %s at %s failed", path, head)
	}
	return []byte(out), nil
}

func (d *gitDiffer) git(ctx context.Context, args ...string) (string, error) {
	// The vault path is passed explicitly rather than inherited from the process
	// working directory: the pod's cwd is not the vault, and a bare `git` would
	// walk up from it and fail or, worse, read another repository.
	baseArgs := make([]string, 0, 4+len(args))
	baseArgs = append(
		baseArgs,
		"--git-dir",
		filepath.Join(d.vaultPath, ".git"),
		"--work-tree",
		d.vaultPath,
	)
	// #nosec G204 -- the binary is the literal "git" and every argument is built
	// here from the configured vault path or from a commit SHA the caller read out
	// of that same repository. No request value reaches this call.
	cmd := exec.CommandContext(ctx, "git", append(baseArgs, args...)...)
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

// parseNameStatus turns `git diff-tree --name-status` output into changes.
//
// A rename or copy entry carries two paths; the vault is never diffed with -M or
// -C so this is a guard rather than a live case, and the last field wins (the new
// path) because that is the one a later commit will still have.
func parseNameStatus(ctx context.Context, out string) ([]Change, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	changes := make([]Change, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			return nil, errors.Errorf(ctx, "unexpected diff-tree line %q", line)
		}
		path := fields[len(fields)-1]
		if path == "" {
			return nil, errors.Errorf(ctx, "diff-tree line %q has no path", line)
		}
		changes = append(changes, Change{
			Path:   path,
			Status: statusOf(fields[0]),
		})
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
