// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"bytes"
	"context"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// MergeMarker prefixes every resolved-merge log line. It is a fixed literal
// rather than a configurable one so a probe can grep for exactly one string: a
// marker the caller chooses is a marker the caller's own probe can be written
// to match.
const MergeMarker = "pull: merged"

// MergeLog records one resolved merge. It is a variable rather than a direct
// glog call so the specs can assert the line without depending on glog's file
// sinks, which offer no capture hook. Production never reassigns it.
var MergeLog = glog.Infof

// FileWriter writes one complete file back to the vault. It is satisfied by the
// git-rest client the write endpoints use, so a resolved pull lands the same way
// an API edit does.
type FileWriter interface {
	WriteFile(ctx context.Context, path string, content []byte) error
}

// ResolveDivergedPull resolves a file that changed on both sides and writes the
// outcome. It is the pull seam: the three sides come from a diverged pull, and
// MergeFile — not this function — decides what the merged bytes are.
//
// A quarantined merge writes nothing and returns the reason, so the caller can
// record it and leave the file for a human. A resolved merge writes Content
// verbatim: it is already the complete file, and re-rendering it through
// storage.SerializeFrontmatter would rewrite bytes the merge just decided.
func ResolveDivergedPull(
	ctx context.Context,
	writer FileWriter,
	path string,
	base, ours, theirs Side,
) (MergeReason, error) {
	result := MergeFile(base, ours, theirs)
	if result.Quarantine {
		return result.Reason, nil
	}
	if err := writer.WriteFile(ctx, path, result.Content); err != nil {
		return "", errors.Wrap(ctx, err, "write merged file")
	}
	LogResolvedMerge(path, base, ours, theirs)
	return "", nil
}

// LogResolvedMerge emits the merge marker for a divergence the service resolved.
//
// It is separate from the write because the two paths that resolve a divergence
// differ only in who writes: an API caller has no working tree, so the service
// writes for it, while git-rest is mid-pull holding the repository and takes the
// bytes itself. Both reconciled a real divergence, so both must produce the same
// marker — a probe that could only see one of them would report the puller's
// automatic path as silent.
//
// Only a file both sides changed counts. When either side still equals the base,
// nothing diverged: the winning side is written, but logging it would make the
// line indistinguishable from a real reconciliation.
func LogResolvedMerge(path string, base, ours, theirs Side) {
	if bytes.Equal(ours.Content, base.Content) || bytes.Equal(theirs.Content, base.Content) {
		return
	}
	// V0 rather than V(1): a resolved merge is a state change an operator has to
	// see without turning on a verbosity flag. It is the only record that two
	// writers' edits were reconciled instead of one silently overwriting the
	// other, and it fires only on a genuine divergence, so it cannot flood the
	// log. Emitted after the write succeeds, so a failed write logs nothing and
	// "one line per resolved file" stays literally true.
	MergeLog(
		"%s path=%s ours_committer_time=%s theirs_committer_time=%s",
		MergeMarker,
		path,
		ours.CommitterTime.Format(time.RFC3339),
		theirs.CommitterTime.Format(time.RFC3339),
	)
}
