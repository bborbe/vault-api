// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ResolveHead reads the commit the vault working tree currently points at,
// shortened to 8 characters. It reads .git/HEAD and the ref file directly so
// the service needs no git binary in its image. An empty string means the
// directory is not a git working tree (or the ref could not be read).
func ResolveHead(vaultPath string) string {
	gitDir := filepath.Join(vaultPath, ".git")
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "ref: ") {
		return shortSHA(line)
	}
	ref := strings.TrimSpace(strings.TrimPrefix(line, "ref: "))
	if value, ok := readRef(gitDir, ref); ok {
		return shortSHA(value)
	}
	if value, ok := packedRef(gitDir, ref); ok {
		return shortSHA(value)
	}
	return ""
}

// readRef reads the ref file git HEAD points at. The ref string comes from
// .git/HEAD, so it is untrusted input as far as this function is concerned: it
// is opened through an os.Root confined to gitDir, so no ref value can read
// outside it.
func readRef(gitDir, ref string) (string, bool) {
	if ref == "" || filepath.IsAbs(ref) || strings.HasPrefix(ref, "..") {
		return "", false
	}
	root, err := os.OpenRoot(gitDir)
	if err != nil {
		return "", false
	}
	defer func() {
		_ = root.Close()
	}()
	file, err := root.Open(filepath.FromSlash(ref))
	if err != nil {
		return "", false
	}
	defer func() {
		_ = file.Close()
	}()
	data, err := io.ReadAll(file)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(data)), true
}

func packedRef(gitDir, ref string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			return fields[0], true
		}
	}
	return "", false
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// FetchAge reports how long ago the working tree was last fetched from its
// remote, read from the modification time of .git/FETCH_HEAD. Every git fetch
// rewrites that file, including one that brings nothing new, so its age is the
// puller's heartbeat rather than a record of the last change: a tree that
// nothing fetches into keeps an age that only grows.
//
// That is the one signal a pod has for a tree that has stopped advancing. The
// local HEAD cannot show it — a frozen tree and a quiet one both leave the HEAD
// unmoved — so a pod comparing HEAD to HEAD sees nothing wrong while the tree
// falls arbitrarily far behind the remote.
//
// known is false when the file is absent: a tree created by git clone and never
// fetched has no fetch to measure, and no claim about its freshness can be made
// either way.
func FetchAge(vaultPath string) (ageSeconds int64, known bool) {
	info, err := os.Stat(filepath.Join(vaultPath, ".git", "FETCH_HEAD"))
	if err != nil {
		return 0, false
	}
	return int64(time.Since(info.ModTime()).Seconds()), true
}
