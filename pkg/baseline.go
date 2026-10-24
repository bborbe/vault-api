// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/errors"
)

// BaselineStore persists the last commit whose changes were published, so a
// restart resumes from it instead of adopting the current HEAD.
//
// Without a store the feed's baseline is process-local: a commit pushed while the
// process was down is adopted silently as the new baseline and never published.
// The failure is invisible from outside — the instance keeps serving reads and
// looks healthy; it simply stops publishing, and a task's last record stays at
// whatever it was when the process was last up.
type BaselineStore interface {
	// Load returns the persisted baseline, or "" when none is stored yet.
	Load(ctx context.Context) (string, error)
	// Save persists the baseline a restart should resume from.
	Save(ctx context.Context, baseline string) error
}

// NoopBaselineStore persists nothing. An instance with no configured path keeps
// exactly the process-local baseline it had before this seam existed, so
// enabling persistence is opt-in rather than a behaviour change for every
// deployment.
func NoopBaselineStore() BaselineStore {
	return noopBaselineStore{}
}

type noopBaselineStore struct{}

func (noopBaselineStore) Load(context.Context) (string, error) { return "", nil }

func (noopBaselineStore) Save(context.Context, string) error { return nil }

// NewFileBaselineStore persists the baseline in a file at path.
//
// A missing file is not an error: it is the first run, and Load reports "". A
// directory that cannot be created is reported by Save, not here, so a
// misconfigured path degrades to the process-local baseline with a warning
// rather than failing startup.
func NewFileBaselineStore(path string) BaselineStore {
	return &fileBaselineStore{path: path}
}

type fileBaselineStore struct {
	path string
}

func (s *fileBaselineStore) Load(ctx context.Context) (string, error) {
	content, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", errors.Wrapf(ctx, err, "read baseline %s failed", s.path)
	}
	return strings.TrimSpace(string(content)), nil
}

func (s *fileBaselineStore) Save(ctx context.Context, baseline string) error {
	if baseline == "" {
		// An empty baseline is the feed's first-run state, not something to
		// persist: writing it would overwrite a good baseline with nothing and
		// turn a transient empty read into a permanent gap.
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return errors.Wrapf(ctx, err, "create baseline dir %s failed", dir)
	}
	// Write then rename: a crash mid-write must leave the previous baseline
	// intact rather than a truncated one, which would resume from a commit that
	// does not parse and abandon the range that follows.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(baseline+"\n"), 0o600); err != nil {
		return errors.Wrapf(ctx, err, "write baseline %s failed", tmp)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return errors.Wrapf(ctx, err, "rename baseline %s failed", s.path)
	}
	return nil
}
