// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package handler

import (
	"net/http"
	"path"
	"strings"

	"github.com/gorilla/mux"

	vaultapi "github.com/bborbe/vault-api/pkg"
)

// PageDeps is what the page routes need: the per-vault link indexes.
type PageDeps struct {
	Links *vaultapi.LinkRegistry
}

// registerPageRoutes wires the generic page surface. It is registered on the
// /api/v1 subrouter, so it inherits the gateway-secret middleware.
//
// The backlinks route is registered FIRST: `{path:.+}` is greedy, so a route
// declared after it would never match `/pages/a/b.md/backlinks` — the page route
// would swallow the suffix and answer 404 for a page named "backlinks".
func registerPageRoutes(api *mux.Router, deps PageDeps) {
	api.Path("/pages/{path:.+}/backlinks").
		Methods(http.MethodGet).
		HandlerFunc(handlePageBacklinks(deps))
	api.Path("/pages/{path:.+}").
		Methods(http.MethodGet).
		HandlerFunc(handlePage(deps))
}

// pageBacklinksResponse is the GET /api/v1/pages/{path}/backlinks envelope.
type pageBacklinksResponse struct {
	Vault     string                 `json:"vault"`
	Path      string                 `json:"path"`
	Name      string                 `json:"name"`
	Backlinks []vaultapi.BacklinkRef `json:"backlinks"`
}

// handlePage serves one page's body plus the links and embeds a renderer needs.
// The vault is a query parameter defaulting to the instance's primary vault, so
// a single-vault deployment needs no parameter and a multi-vault one can name
// any vault it serves.
func handlePage(deps PageDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pagePath, ok := pagePathOf(w, r)
		if !ok {
			return
		}
		index, ok := pageIndex(w, r, deps)
		if !ok {
			return
		}
		page, err := index.Page(r.Context(), pagePath)
		if err != nil {
			writePageError(w, err, pagePath)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}
}

// handlePageBacklinks serves every page that links to the requested page.
func handlePageBacklinks(deps PageDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pagePath, ok := pagePathOf(w, r)
		if !ok {
			return
		}
		index, ok := pageIndex(w, r, deps)
		if !ok {
			return
		}
		backlinks, err := index.Backlinks(r.Context(), pagePath)
		if err != nil {
			writePageError(w, err, pagePath)
			return
		}
		writeJSON(w, http.StatusOK, pageBacklinksResponse{
			Vault:     index.VaultName(),
			Path:      pagePath,
			Name:      strings.TrimSuffix(path.Base(pagePath), ".md"),
			Backlinks: backlinks,
		})
	}
}

// pagePathOf reads and validates the page path from the route.
func pagePathOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	pagePath := mux.Vars(r)["path"]
	if pagePath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return "", false
	}
	return pagePath, true
}

// pageIndex resolves the vault query parameter to a link index.
func pageIndex(w http.ResponseWriter, r *http.Request, deps PageDeps) (*vaultapi.LinkIndex, bool) {
	if deps.Links == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_vault"})
		return nil, false
	}
	vaultName := r.URL.Query().Get("vault")
	index, ok := deps.Links.For(vaultName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "unknown_vault",
			"vault": vaultName,
		})
		return nil, false
	}
	return index, true
}

// writePageError maps a page read error onto the route's status codes. A page
// that cannot be read because it is absent, escapes the vault, or is not a
// markdown path is a 404 — the same answer a browser gets for a missing page.
// Anything else is a 500, because it means the vault itself could not be read.
func writePageError(w http.ResponseWriter, err error, pagePath string) {
	if vaultapi.IsNotFound(err) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "not_found",
			"path":  pagePath,
		})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{
		"error": "internal",
		"path":  pagePath,
	})
}
