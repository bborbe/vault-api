// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

// TaskItem is the typed projection of one task page. It is served by
// GET /api/v1/tasks and GET /api/v1/tasks/{id}.
type TaskItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Path     string   `json:"path"`
	Status   string   `json:"status"`
	Phase    string   `json:"phase"`
	TaskType string   `json:"task_type"`
	Stage    string   `json:"stage"`
	Assignee string   `json:"assignee"`
	Goals    []string `json:"goals"`
	Cluster  *string  `json:"cluster"`
	Flag     bool     `json:"flag"`
	Vault    string   `json:"vault"`
}

// GoalItem is the typed projection of one goal page. It is served by
// GET /api/v1/goals and GET /api/v1/goals/{id}.
type GoalItem struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Path     string   `json:"path"`
	Status   string   `json:"status"`
	Themes   []string `json:"themes"`
	Assignee string   `json:"assignee,omitempty"`
	Vault    string   `json:"vault"`
}

// ListResponse is the envelope every list endpoint returns. Total is the number
// of items in the response; Warnings counts files excluded from the index
// because they could not be parsed or carry a value outside the enum.
type ListResponse[T any] struct {
	Items           []T   `json:"items"`
	Total           int   `json:"total"`
	Warnings        int   `json:"warnings"`
	IndexAgeSeconds int64 `json:"index_age_seconds"`
}

// Readiness is the body of GET /readiness.
type Readiness struct {
	Ready           bool   `json:"ready"`
	Vault           string `json:"vault"`
	IndexAgeSeconds int64  `json:"index_age_seconds"`
	// TreeAgeSeconds is how long ago the working tree was last fetched from its
	// remote, and is null when the tree carries no FETCH_HEAD to measure. It is
	// separate from IndexAgeSeconds because the two fail independently: the index
	// can stop being confirmed on a live tree, and a tree can freeze under a
	// healthy loop. A caller gating on Ready does not need to read it, but an
	// operator diagnosing a red pod does: it says which half went stale.
	TreeAgeSeconds *int64 `json:"tree_age_seconds"`
	Head           string `json:"head"`
	Warnings       int    `json:"warnings"`
}
