// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"

	"github.com/bborbe/errors"
)

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
	return "", nil
}
