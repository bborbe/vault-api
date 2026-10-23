// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

// maxMergeBodyBytes caps a merge request. It is far larger than the write
// endpoints' cap because the body carries three complete file contents rather
// than a phase name, a status name and an approver: git-rest accepts 10 MiB per
// file, so 32 MiB admits a merge of three near-limit files with room for JSON
// escaping.
const maxMergeBodyBytes = 32 * 1024 * 1024

// mergeSide is one side of a three-way merge as it arrives on the wire. The
// committer time is a string rather than a time.Time so a malformed value is
// answered as a 400 with a named field instead of an opaque decode error.
type mergeSide struct {
	Content       string `json:"content"`
	CommitterTime string `json:"committer_time"`
}

// mergeRequest is the body of POST /api/v1/merge. It carries the three sides a
// diverged pull produces — base, ours and theirs — so the caller supplies the
// divergence rather than the service having to observe one it cannot see.
type mergeRequest struct {
	Path   string    `json:"path"`
	Base   mergeSide `json:"base"`
	Ours   mergeSide `json:"ours"`
	Theirs mergeSide `json:"theirs"`
}

// mergeResponse reports what the merge decided. Reason is empty when the merge
// resolved; otherwise it names why the file was quarantined.
type mergeResponse struct {
	Path   string `json:"path"`
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// handleMerge resolves a diverged file and writes the outcome through git-rest.
//
// It is the production caller of ResolveDivergedPull: without it the merge was
// reachable only from its own tests. The three sides come from the request
// because nothing in this service observes a divergence — git-rest pulls, and
// the refresh loop only compares HEAD (see Typed Vault REST API Design § 6,
// "Where the merge is actually invoked").
func handleMerge(deps writeDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var request mergeRequest
		if !decodeBodyLimited(w, r, &request, maxMergeBodyBytes) {
			return
		}
		if reason := mergePathError(request.Path); reason != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error":  "invalid_body",
				"reason": reason,
				"path":   request.Path,
			})
			return
		}
		base, ok := mergeSideOf(w, request.Path, "base", request.Base)
		if !ok {
			return
		}
		ours, ok := mergeSideOf(w, request.Path, "ours", request.Ours)
		if !ok {
			return
		}
		theirs, ok := mergeSideOf(w, request.Path, "theirs", request.Theirs)
		if !ok {
			return
		}

		reason, err := vaultapi.ResolveDivergedPull(
			ctx, deps.writer, request.Path, base, ours, theirs,
		)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":  "git_rest_failed",
				"reason": err.Error(),
				"path":   request.Path,
			})
			return
		}
		if reason != "" {
			writeJSON(w, http.StatusOK, mergeResponse{
				Path:   request.Path,
				Result: "quarantined",
				Reason: string(reason),
			})
			return
		}
		writeJSON(w, http.StatusOK, mergeResponse{
			Path:   request.Path,
			Result: "merged",
		})
	}
}

// mergePathError names why a path cannot be merged, or returns "" when it can.
//
// The path is client-supplied and becomes two things: a segment of the outbound
// git-rest URL and part of the `pull: merged` log line. Every sibling write
// endpoint resolves its target through the index and so can only reach an
// indexed task file; this one cannot, because a merge target need not be
// indexed. So it is confined here instead: `..` is refused rather than left to
// git-rest (url.PathEscape leaves dots unreserved, so a `..` segment survives
// encodePath), and control characters are refused because a newline would
// inject raw lines into the log.
func mergePathError(raw string) string {
	switch {
	case raw == "":
		return "path is required"
	case raw != strings.TrimSpace(raw):
		return "path must not carry leading or trailing whitespace"
	case strings.HasPrefix(raw, "/"):
		return "path must be vault-relative"
	case strings.ContainsAny(raw, "\n\r\t"):
		return "path must not contain control characters"
	}
	// Before the cleanliness check: path.Clean collapses "25 Tasks/../../etc"
	// to "../etc", so the general message would fire first and hide the
	// traversal, which is the failure a reader most needs named.
	if slices.Contains(strings.Split(raw, "/"), "..") {
		return "path must not contain a .. segment"
	}
	if path.Clean(raw) != raw {
		return "path must be a clean vault-relative path"
	}
	return ""
}

// mergeSideOf converts one wire side into a merge side. It answers the 400
// itself and reports whether the caller should continue, so a bad committer
// time names the field that carried it rather than failing the whole body.
func mergeSideOf(
	w http.ResponseWriter,
	vaultPath, name string,
	side mergeSide,
) (vaultapi.Side, bool) {
	// An omitted side decodes to the zero value, so it is reported as absent
	// rather than as a malformed time — the two are different mistakes and only
	// one of them is fixed by correcting the timestamp.
	if side.CommitterTime == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":  "invalid_body",
			"reason": name + " is required",
			"path":   vaultPath,
		})
		return vaultapi.Side{}, false
	}
	committerTime, err := time.Parse(time.RFC3339, side.CommitterTime)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error":  "invalid_body",
			"reason": name + ".committer_time must be RFC3339",
			"path":   vaultPath,
		})
		return vaultapi.Side{}, false
	}
	return vaultapi.Side{
		Content:       []byte(side.Content),
		CommitterTime: committerTime,
	}, true
}
