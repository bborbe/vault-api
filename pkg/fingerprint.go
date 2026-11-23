// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fingerprint is a cheap change signal for a vault's page directories: the
// newest modification time and the number of *.md files across the tasks,
// goals and topics directories. A write moves the newest mtime, a create or
// delete moves the count, so any edit made outside vault-api (vault-cli writing
// frontmatter, an editor) moves it without a git commit or a filesystem watch.
type Fingerprint struct {
	MaxModTime time.Time
	Count      int
}

// PageFingerprint computes the Fingerprint of the directories the index reads.
// It lists each directory once and stats each entry, with no recursion and no
// file reads, mirroring what Build lists. A directory that cannot be read
// contributes nothing: Build reports that failure itself.
func PageFingerprint(cfg Config) Fingerprint {
	var fp Fingerprint
	for _, dir := range []string{cfg.TasksDir, cfg.GoalsDir, cfg.TopicsDir} {
		entries, err := os.ReadDir(filepath.Join(cfg.VaultPath, dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			fp.Count++
			if info.ModTime().After(fp.MaxModTime) {
				fp.MaxModTime = info.ModTime()
			}
		}
	}
	return fp
}
