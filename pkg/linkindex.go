// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	stderrors "errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// LinkIndex answers whole-vault page reads and backlinks without walking the
// vault on the request path.
//
// It holds three things: every page's name→relative-path so a wikilink target
// resolves, the link targets each page carries so a target's inbound set is a
// map lookup, and each file's size and modification time so a refresh re-reads
// only what changed. It deliberately holds no page body: a page read is one file
// read, and a vault of 22k pages stays a few tens of megabytes of strings rather
// than the 260 MB its bodies would cost.
//
// The index is per vault. A multi-vault instance holds one per vault in a
// LinkRegistry, which is what lets a caller name the vault it wants.
type LinkIndex struct {
	cfg Config

	// buildMu serialises builds so a burst of cold requests runs one walk, not
	// one per request.
	buildMu sync.Mutex

	mu      sync.RWMutex
	names   map[string]string
	files   map[string]fileLinks
	inbound map[string][]string
	built   bool
}

// linkSnapshot is the index's published state. The maps are replaced on each
// build, never mutated in place, so a reader may use one after the lock is
// released.
type linkSnapshot struct {
	names   map[string]string
	files   map[string]fileLinks
	inbound map[string][]string
	built   bool
}

// fileLinks is one indexed file: the size and modification time it was read at,
// and the page names its body links to.
type fileLinks struct {
	size    int64
	modTime int64
	targets []string
}

// PageContent is one page as a renderer needs it: the body, the vault-relative
// path of every wikilink target the body resolves, and the body of every page
// the body embeds. Links and embeds are resolved against the index, so a caller
// needs no second request and no local vault walk.
type PageContent struct {
	Vault   string            `json:"vault"`
	Path    string            `json:"path"`
	Name    string            `json:"name"`
	Content string            `json:"content"`
	Links   map[string]string `json:"links"`
	Embeds  map[string]string `json:"embeds"`
}

// BacklinkRef is one page that links to the requested page.
type BacklinkRef struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// NewLinkIndex returns an empty index for the vault cfg locates. It is built by
// Refresh, which the caller runs at startup and again on its refresh tick.
func NewLinkIndex(cfg Config) *LinkIndex {
	return &LinkIndex{cfg: cfg}
}

// Refresh brings the index up to date with the working tree. It walks the vault
// (stat only), re-reads the files whose size or modification time moved, and
// drops the files that vanished. It is the only expensive operation here and it
// runs on the refresh tick, never on the request path.
//
// A walk that fails part-way leaves the previous index serving: a page view over
// a slightly stale index beats a page view that errors because one directory was
// momentarily unreadable.
func (i *LinkIndex) Refresh(ctx context.Context) error {
	i.buildMu.Lock()
	defer i.buildMu.Unlock()
	return i.refreshLocked(ctx)
}

// refreshLocked is one build. The caller holds buildMu, which is what makes
// Ensure's second Built() check authoritative.
func (i *LinkIndex) refreshLocked(ctx context.Context) error {
	files, err := i.readTree(ctx, i.snapshot().files)
	if err != nil {
		return err
	}
	names, inbound := linkMaps(files)

	i.mu.Lock()
	i.names = names
	i.files = files
	i.inbound = inbound
	i.built = true
	i.mu.Unlock()
	return nil
}

// readTree returns one entry per markdown file in the vault, reusing the
// previous entry for every file whose size and modification time are unchanged.
// It reads through an os.Root so a path cannot be redirected outside the vault
// between the walk and the read.
func (i *LinkIndex) readTree(
	ctx context.Context,
	previous map[string]fileLinks,
) (map[string]fileLinks, error) {
	root, err := os.OpenRoot(i.cfg.VaultPath)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "open vault root for the link index")
	}
	defer func() { _ = root.Close() }()

	files := make(map[string]fileLinks, len(previous))
	err = filepath.WalkDir(i.cfg.VaultPath, func(p string, d fs.DirEntry, walkErr error) error {
		// An unreadable entry is skipped rather than failing the walk: one
		// permission problem must not blank the whole index.
		if walkErr != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != i.cfg.VaultPath {
				return filepath.SkipDir
			}
			return nil
		}
		rel, ok := pageRelPath(i.cfg.VaultPath, p, d)
		if !ok {
			return nil
		}
		if entry, kept := reuseEntry(rel, d, previous); kept {
			files[rel] = entry
			return nil
		}
		if entry, read := readEntry(root, rel, d); read {
			files[rel] = entry
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(ctx, err, "walk vault for the link index")
	}
	return files, nil
}

// pageRelPath returns a markdown file's vault-relative slash path. A directory,
// a non-markdown file and a symlinked file are all rejected: the walk must not
// follow a symlink that could point outside the vault.
func pageRelPath(vaultPath, p string, d fs.DirEntry) (string, bool) {
	if !strings.HasSuffix(d.Name(), ".md") || d.Type()&fs.ModeSymlink != 0 {
		return "", false
	}
	rel, err := filepath.Rel(vaultPath, p)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// reuseEntry returns the previous entry for a file whose size and modification
// time are unchanged, so an idle tick allocates nothing and reads nothing.
func reuseEntry(rel string, d fs.DirEntry, previous map[string]fileLinks) (fileLinks, bool) {
	known, ok := previous[rel]
	if !ok {
		return fileLinks{}, false
	}
	info, err := d.Info()
	if err != nil {
		return fileLinks{}, false
	}
	if known.size != info.Size() || known.modTime != info.ModTime().UnixNano() {
		return fileLinks{}, false
	}
	return known, true
}

// readEntry reads one page and extracts its link targets. A read that fails is
// skipped rather than failing the walk, for the same reason an unreadable entry
// is.
func readEntry(root *os.Root, rel string, d fs.DirEntry) (fileLinks, bool) {
	info, err := d.Info()
	if err != nil {
		return fileLinks{}, false
	}
	raw, readErr := root.ReadFile(rel)
	if readErr != nil {
		glog.V(2).Infof("link index: skip page %s: %v", rel, readErr)
		return fileLinks{}, false
	}
	return fileLinks{
		size:    info.Size(),
		modTime: info.ModTime().UnixNano(),
		targets: LinkTargets(string(raw)),
	}, true
}

// linkMaps builds the name→relative-path map and the target→inbound-set map
// from the per-file link targets.
//
// The name map prefers the lexically-first relative path for a duplicated
// basename, so the same target always resolves to the same page. The daily notes
// are the only real collision, and a walk-order-dependent answer would differ
// between restarts.
func linkMaps(files map[string]fileLinks) (map[string]string, map[string][]string) {
	names := make(map[string]string, len(files))
	for rel := range files {
		name := strings.TrimSuffix(path.Base(rel), ".md")
		if existing, ok := names[name]; !ok || rel < existing {
			names[name] = rel
		}
	}
	inbound := make(map[string][]string, len(names))
	for rel, entry := range files {
		for _, target := range entry.targets {
			// A root-level page linking to itself is not a backlink: this is
			// vault-markdown's own rule (it compares the page key without its .md
			// suffix to the target), and the rendered page applies a basename
			// filter on top of it.
			if strings.TrimSuffix(rel, ".md") == target {
				continue
			}
			inbound[target] = append(inbound[target], rel)
		}
	}
	for target := range inbound {
		sort.Strings(inbound[target])
	}
	return names, inbound
}

// snapshot returns the current state under the read lock.
func (i *LinkIndex) snapshot() linkSnapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return linkSnapshot{names: i.names, files: i.files, inbound: i.inbound, built: i.built}
}

// VaultName is the vault this index answers for.
func (i *LinkIndex) VaultName() string { return i.cfg.VaultName }

// IsNotFound reports whether err means the page cannot be served: it is absent,
// its path is not a markdown path inside the vault, or it is reached through a
// symlink that leaves the vault. The HTTP layer maps exactly this to a 404.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if stderrors.Is(err, fs.ErrNotExist) {
		return true
	}
	message := err.Error()
	// os.Root refuses an escaping path with its own sentinel rather than
	// fs.ErrNotExist, and the sentinel is not exported, so the message is the
	// only handle. Both spellings are checked: the root's escape refusal, and
	// the one this package used before the read moved onto os.Root.
	return strings.Contains(message, "escapes from parent") ||
		strings.Contains(message, "symlink outside vault")
}

// Built reports whether a build has completed.
func (i *LinkIndex) Built() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.built
}

// Ensure builds the index on first use, so the first page view after a restart
// is correct without the caller having run Refresh first. Concurrent cold
// callers share one build: the second check runs under buildMu, which a
// concurrent build holds.
func (i *LinkIndex) Ensure(ctx context.Context) error {
	if i.Built() {
		return nil
	}
	i.buildMu.Lock()
	defer i.buildMu.Unlock()
	if i.Built() {
		return nil
	}
	return i.refreshLocked(ctx)
}

// Page returns one page's body plus the links and embeds a renderer needs. The
// body is read from disk so a just-edited page renders fresh; the resolution of
// its targets comes from the index.
func (i *LinkIndex) Page(ctx context.Context, pagePath string) (*PageContent, error) {
	if err := i.Ensure(ctx); err != nil {
		return nil, err
	}
	rel, err := i.pageRelPath(pagePath)
	if err != nil {
		return nil, err
	}
	root, err := i.openRoot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()

	raw, err := readPageFile(ctx, root, rel)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(path.Base(pagePath), ".md")
	embeds := i.expandEmbeds(ctx, root, raw)
	links := i.resolveLinks(raw, embeds, name, pagePath)
	return &PageContent{
		Vault:   i.cfg.VaultName,
		Path:    pagePath,
		Name:    name,
		Content: raw,
		Links:   links,
		Embeds:  embeds,
	}, nil
}

// Backlinks returns every page whose body links to the page at pagePath, sorted
// by relative path. The set matches github.com/bborbe/vault-markdown's
// Backlinks exactly, including the case where a root-level page links to itself;
// the caller applies its own self filter.
func (i *LinkIndex) Backlinks(ctx context.Context, pagePath string) ([]BacklinkRef, error) {
	if err := i.Ensure(ctx); err != nil {
		return nil, err
	}
	rel, err := i.pageRelPath(pagePath)
	if err != nil {
		return nil, err
	}
	root, err := i.openRoot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// The page must exist for its backlinks to be a meaningful answer: a typo in
	// the path would otherwise read as "nobody links to this", which is a wrong
	// answer where a 404 is the right one.
	if err := statPage(ctx, root, rel); err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(path.Base(pagePath), ".md")
	i.mu.RLock()
	rels := i.inbound[name]
	i.mu.RUnlock()
	refs := make([]BacklinkRef, 0, len(rels))
	for _, rel := range rels {
		refs = append(refs, BacklinkRef{
			Path: rel,
			Name: strings.TrimSuffix(path.Base(rel), ".md"),
		})
	}
	return refs, nil
}

// pageRelPath validates a vault-relative page path and returns it in the slash
// form os.Root takes. A path that is empty, absolute, escapes the vault, or does
// not end in .md is rejected, which is the same guard the HTTP route needs.
//
// Validation is not the security boundary on its own: the read goes through
// os.Root, which refuses to follow any symlink that leaves the vault — including
// one on an intermediate directory, which a path check cannot see.
func (i *LinkIndex) pageRelPath(pagePath string) (string, error) {
	if pagePath == "" || strings.HasPrefix(pagePath, "/") || !strings.HasSuffix(pagePath, ".md") {
		return "", os.ErrNotExist
	}
	for _, segment := range strings.Split(pagePath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", os.ErrNotExist
		}
	}
	return pagePath, nil
}

// openRoot opens the vault as an os.Root, so every read below is confined to it.
func (i *LinkIndex) openRoot(ctx context.Context) (*os.Root, error) {
	root, err := os.OpenRoot(i.cfg.VaultPath)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "open vault root")
	}
	return root, nil
}

// resolveLinks returns the target→relative-path map for every wikilink target
// the body carries after embed expansion. A target that resolves to the
// requested page itself maps to pagePath, so a self-link stays on the page the
// caller asked for rather than jumping to the index's copy of the same name.
func (i *LinkIndex) resolveLinks(
	body string,
	embeds map[string]string,
	pageName string,
	pagePath string,
) map[string]string {
	expanded := expandEmbedBodies(body, embeds)
	i.mu.RLock()
	defer i.mu.RUnlock()
	links := map[string]string{}
	for _, target := range LinkTargets(expanded) {
		if target == pageName {
			links[target] = pagePath
			continue
		}
		if rel, ok := i.names[target]; ok {
			links[target] = rel
		}
	}
	return links
}

// expandEmbeds returns the body of every page the body embeds on a line of its
// own. Only the top level expands, mirroring the renderer: an embed inside an
// inlined body is dropped there, so its body is not needed here.
func (i *LinkIndex) expandEmbeds(
	ctx context.Context,
	root *os.Root,
	body string,
) map[string]string {
	embeds := map[string]string{}
	i.mu.RLock()
	names := i.names
	i.mu.RUnlock()
	for _, line := range strings.Split(normalizeNewlines(body), "\n") {
		target, ok := loneEmbedTarget(line)
		if !ok {
			continue
		}
		rel, known := names[target]
		if !known {
			continue
		}
		raw, err := readPageFile(ctx, root, rel)
		if err != nil {
			continue
		}
		inner := bodyWithoutFrontmatter(normalizeNewlines(raw))
		embeds[target] = inner
	}
	return embeds
}

// expandEmbedBodies replaces each lone-embed line with the embedded body, which
// is what the renderer does before it parses links. Links inside an embedded
// body therefore resolve too, and a link set built from the raw body alone would
// leave them unresolved.
func expandEmbedBodies(body string, embeds map[string]string) string {
	if len(embeds) == 0 {
		return body
	}
	lines := strings.Split(normalizeNewlines(body), "\n")
	for idx, line := range lines {
		target, ok := loneEmbedTarget(line)
		if !ok {
			continue
		}
		if content, found := embeds[target]; found {
			lines[idx] = content
		}
	}
	return strings.Join(lines, "\n")
}

// loneEmbedTarget reports whether a line is nothing but an ![[target]] embed,
// matching the renderer's own test.
func loneEmbedTarget(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "![[") || !strings.HasSuffix(trimmed, "]]") {
		return "", false
	}
	target := strings.TrimSpace(trimmed[3 : len(trimmed)-2])
	if target == "" || strings.ContainsAny(target, "[]") {
		return "", false
	}
	return target, true
}

// statPage reports whether a page exists and may be served. The stat goes
// through os.Root, so a page reached through a symlink that leaves the vault —
// on the file itself or on any directory above it — is refused here.
func statPage(ctx context.Context, root *os.Root, rel string) error {
	if _, err := root.Stat(rel); err != nil {
		return errors.Wrap(ctx, err, "stat page")
	}
	return nil
}

// readPageFile reads one page file through os.Root, which refuses any symlink
// that leaves the vault. The bytes are returned as they are: a page without
// frontmatter is a page, not an error.
func readPageFile(ctx context.Context, root *os.Root, rel string) (string, error) {
	raw, err := root.ReadFile(rel)
	if err != nil {
		return "", errors.Wrap(ctx, err, "read page")
	}
	return string(raw), nil
}

var (
	linkPattern       = regexp.MustCompile(`\[\[([^\]]*)\]\]`)
	fencedBlock       = regexp.MustCompile("(?s)```.*?```")
	tildeBlock        = regexp.MustCompile("(?s)~~~.*?~~~")
	inlineCode        = regexp.MustCompile("`[^`\n]*`")
	frontmatterMarker = "---"
)

// LinkTargets returns the page names a body links to, with the same rules
// github.com/bborbe/vault-markdown's Backlinks applies: a link inside the
// frontmatter, a fenced block or an inline code span does not count, and the
// target is the text before the first `|` or `#` with no trimming — so
// `[[Foo #anchor]]` is not a link to Foo, exactly as Backlinks decides it.
func LinkTargets(content string) []string {
	text := linkableText(content)
	seen := map[string]struct{}{}
	targets := make([]string, 0, 8)
	for _, match := range linkPattern.FindAllStringSubmatch(text, -1) {
		// Clone the target: a substring of the page body keeps the whole body
		// alive, and the index holds one of these per link for the life of the
		// process — measured at ~490 MB of retained bodies across the local
		// vault set before the clone.
		target := strings.Clone(match[1])
		if idx := strings.IndexAny(target, "|#"); idx >= 0 {
			target = target[:idx]
		}
		if target == "" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	return targets
}

// linkableText drops the frontmatter, fenced blocks and inline code, so a link
// shown as code is not counted.
func linkableText(content string) string {
	body := bodyWithoutFrontmatter(normalizeNewlines(content))
	body = fencedBlock.ReplaceAllString(body, "")
	body = tildeBlock.ReplaceAllString(body, "")
	return inlineCode.ReplaceAllString(body, "")
}

// normalizeNewlines makes CRLF and LF sources compare equal.
func normalizeNewlines(source string) string {
	return strings.ReplaceAll(source, "\r\n", "\n")
}

// bodyWithoutFrontmatter drops a leading YAML frontmatter block. A source
// without frontmatter is returned unchanged.
func bodyWithoutFrontmatter(source string) string {
	if !strings.HasPrefix(source, frontmatterMarker+"\n") {
		return source
	}
	rest := source[len(frontmatterMarker)+1:]
	if strings.HasPrefix(rest, frontmatterMarker) {
		return strings.TrimPrefix(rest[len(frontmatterMarker):], "\n")
	}
	end := strings.Index(rest, "\n"+frontmatterMarker)
	if end < 0 {
		return source
	}
	return strings.TrimPrefix(rest[end+len(frontmatterMarker)+1:], "\n")
}

// LinkRegistry maps a vault name to its link index, so one multi-vault instance
// can answer a page read for any vault it serves.
type LinkRegistry struct {
	mu      sync.RWMutex
	byVault map[string]*LinkIndex
	primary string
}

// NewLinkRegistry returns a registry with the primary vault registered. A caller
// attaches the other vaults it serves with Attach.
func NewLinkRegistry(primary *LinkIndex) *LinkRegistry {
	registry := &LinkRegistry{byVault: map[string]*LinkIndex{}}
	if primary != nil {
		registry.primary = strings.ToLower(primary.cfg.VaultName)
	}
	registry.Attach(primary)
	return registry
}

// Attach registers another vault's index. A nil index, or one whose vault has no
// name, is ignored, so a caller that could not build one does not have to
// branch.
func (r *LinkRegistry) Attach(index *LinkIndex) {
	if index == nil || index.cfg.VaultName == "" {
		return
	}
	r.mu.Lock()
	r.byVault[strings.ToLower(index.cfg.VaultName)] = index
	r.mu.Unlock()
}

// For returns the index for a vault name, case-insensitively. An empty name
// selects the primary vault, which is what a single-vault deployment expects.
func (r *LinkRegistry) For(vaultName string) (*LinkIndex, bool) {
	key := strings.ToLower(strings.TrimSpace(vaultName))
	if key == "" {
		key = r.primary
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	index, ok := r.byVault[key]
	return index, ok
}

// Indexes returns every registered index, so a caller can refresh them all.
func (r *LinkRegistry) Indexes() []*LinkIndex {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*LinkIndex, 0, len(r.byVault))
	for _, index := range r.byVault {
		out = append(out, index)
	}
	return out
}
