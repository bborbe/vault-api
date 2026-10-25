// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/bborbe/vault-cli/pkg/storage"
)

// Config locates the vault working tree and the directories the index reads.
type Config struct {
	VaultPath string
	VaultName string
	TasksDir  string
	GoalsDir  string
	// StaleAfter is how long either freshness signal may go unrefreshed before
	// Readiness stops reporting ready: the index's own verification, and the
	// working tree's last fetch from its remote. Zero disables the rule, which is
	// what a caller serving a tree nothing fetches (a plain clone) wants.
	StaleAfter time.Duration
}

// TaskFilter narrows a task list. Empty fields are ignored; a query with no
// fields returns every indexed task.
type TaskFilter struct {
	Assignee string
	Status   string
	Phase    string
	Goal     string
	Cluster  string
}

// Index is the in-memory view of the vault. It is rebuilt at startup and again
// whenever the working tree's HEAD changes, so list queries are map lookups
// rather than per-file reads.
type Index struct {
	cfg     Config
	metrics Metrics

	mu           sync.RWMutex
	tasks        []TaskItem
	goals        []GoalItem
	taskByID     map[string]int
	goalByID     map[string]int
	invalidTasks map[string]string
	invalidGoals map[string]string
	taskSets     taskSets
	taskWarnings int
	goalWarnings int
	// verifiedAt is when the index was last confirmed to match the working tree:
	// set by a successful Build, and advanced by Verify on every refresh tick that
	// finds the HEAD unmoved. It is the index's staleness clock, so a loop that
	// stopped running shows up as a growing age rather than a frozen one.
	verifiedAt time.Time
	// built records whether the last Build succeeded. A failed rebuild leaves the
	// previous data in place and clears this, so the read path keeps serving while
	// Readiness reports the index unusable.
	built bool
	head  string
}

type indexSet map[string]map[int]struct{}

func (s indexSet) add(key string, idx int) {
	if key == "" {
		return
	}
	m, ok := s[key]
	if !ok {
		m = make(map[int]struct{})
		s[key] = m
	}
	m[idx] = struct{}{}
}

type taskSets struct {
	assignee indexSet
	status   indexSet
	phase    indexSet
	cluster  indexSet
	goal     indexSet
}

func newTaskSets() taskSets {
	return taskSets{
		assignee: indexSet{},
		status:   indexSet{},
		phase:    indexSet{},
		cluster:  indexSet{},
		goal:     indexSet{},
	}
}

// NewIndex creates an empty index. Build populates it and refreshes the
// open-task gauge.
func NewIndex(cfg Config, metrics Metrics) *Index {
	return &Index{
		cfg:          cfg,
		metrics:      metrics,
		tasks:        make([]TaskItem, 0),
		goals:        make([]GoalItem, 0),
		taskByID:     map[string]int{},
		goalByID:     map[string]int{},
		invalidTasks: map[string]string{},
		invalidGoals: map[string]string{},
		taskSets:     newTaskSets(),
	}
}

// Build reads every task and goal page and replaces the index atomically. A
// page whose frontmatter does not parse, or whose status/phase is present but
// not a known value, is excluded and counted in Warnings — never coerced.
//
// A failed build leaves the previous index in place but marks it unbuilt, so the
// read path keeps serving the last good data while Readiness reports the failure.
// Before that mark existed a pod that had built once answered ready forever, and
// handleReadiness's 503 branch was unreachable.
func (i *Index) Build(ctx context.Context, store storage.PageStorage) error {
	tasks, taskInvalid, taskWarnings, taskByID, sets, err := i.buildTasks(ctx, store)
	if err != nil {
		i.markUnbuilt()
		return err
	}
	goals, goalInvalid, goalWarnings, goalByID, err := i.buildGoals(ctx, store)
	if err != nil {
		i.markUnbuilt()
		return err
	}

	i.mu.Lock()
	i.tasks = tasks
	i.taskByID = taskByID
	i.invalidTasks = taskInvalid
	i.taskSets = sets
	i.goals = goals
	i.goalByID = goalByID
	i.invalidGoals = goalInvalid
	i.taskWarnings = taskWarnings
	i.goalWarnings = goalWarnings
	i.verifiedAt = time.Now()
	i.head = ResolveHead(i.cfg.VaultPath)
	i.built = true
	i.mu.Unlock()

	// Published after the swap so a scrape never sees the new counts against
	// the old index.
	i.metrics.SetOpenTasks(openTasksByStageTypeAndPhase(tasks))
	return nil
}

// markUnbuilt records that the last build failed. It deliberately leaves
// verifiedAt alone: the index is no longer confirmed against the tree, and
// letting the age keep growing is what makes the failure visible in Readiness
// rather than only in the log.
func (i *Index) markUnbuilt() {
	i.mu.Lock()
	i.built = false
	i.mu.Unlock()
}

// Verify records that the tree still points at the head this index was built
// from. The refresh loop calls it on every tick where the HEAD did not move, so
// a loop that is running keeps the index's age bounded and a loop that stopped
// lets it grow. It is not a rebuild: an unmoved HEAD means no file changed, so
// re-reading every page would spend the reads the HEAD check exists to save.
func (i *Index) Verify() {
	i.mu.Lock()
	i.verifiedAt = time.Now()
	i.mu.Unlock()
}

// Built reports whether the last build succeeded. It is the rebuild trigger for
// the refresh loop, deliberately narrower than Readiness().Ready: a tree that is
// stale but unmoved must not make the loop rebuild on every tick.
func (i *Index) Built() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.built
}

// openTasksByStageTypeAndPhase counts the indexed tasks whose status is neither
// completed nor aborted, per frontmatter stage, task_type and phase. A task with
// no stage, no task_type or no phase counts under the matching unknown bucket,
// and one with no status counts as open — an unparseable status is what excludes
// a page, not a missing one (see buildTasks).
func openTasksByStageTypeAndPhase(tasks []TaskItem) map[OpenTaskKey]int {
	counts := make(map[OpenTaskKey]int, len(tasks))
	for _, task := range tasks {
		if task.Status == "completed" || task.Status == "aborted" {
			continue
		}
		key := OpenTaskKey{Stage: task.Stage, TaskType: task.TaskType, Phase: task.Phase}
		if key.Stage == "" {
			key.Stage = StageUnknown
		}
		if key.TaskType == "" {
			key.TaskType = TaskTypeUnknown
		}
		if key.Phase == "" {
			key.Phase = PhaseUnknown
		}
		counts[key]++
	}
	return counts
}

func (i *Index) buildTasks(
	ctx context.Context,
	store storage.PageStorage,
) ([]TaskItem, map[string]string, int, map[string]int, taskSets, error) {
	dir := filepath.Join(i.cfg.VaultPath, i.cfg.TasksDir)
	files, err := listMarkdownFiles(dir)
	if err != nil {
		return nil, nil, 0, nil, taskSets{}, err
	}
	pages, err := store.ListPages(ctx, i.cfg.VaultPath, i.cfg.TasksDir)
	if err != nil {
		return nil, nil, 0, nil, taskSets{}, err
	}

	tasks := make([]TaskItem, 0, len(pages))
	invalid := make(map[string]string)
	byID := make(map[string]int, len(pages))
	sets := newTaskSets()

	readable := make(map[string]struct{}, len(pages))
	for _, page := range pages {
		readable[page.Name] = struct{}{}
	}
	warnings := 0
	for _, name := range files {
		if _, ok := readable[name]; ok {
			continue
		}
		warnings++
		invalid[slugify(name)] = "unparseable frontmatter"
		invalid[name] = "unparseable frontmatter"
	}

	for _, page := range pages {
		item, reason, ok := i.taskItem(page)
		if !ok {
			warnings++
			invalid[slugify(page.Name)] = reason
			invalid[page.Name] = reason
			continue
		}

		idx := len(tasks)
		tasks = append(tasks, item)
		byID[item.ID] = idx
		byID[page.Name] = idx
		byID[slugify(page.Name)] = idx
		sets.assignee.add(item.Assignee, idx)
		sets.status.add(item.Status, idx)
		sets.phase.add(item.Phase, idx)
		if item.Cluster != nil {
			sets.cluster.add(*item.Cluster, idx)
		}
		for _, goal := range item.Goals {
			sets.goal.add(goalKey(goal), idx)
		}
	}

	return tasks, invalid, warnings, byID, sets, nil
}

// taskItem converts one page into a TaskItem. ok is false when the page must be
// excluded from the index, and reason then names why for the warnings map.
func (i *Index) taskItem(page *domain.Page) (TaskItem, string, bool) {
	task := domain.NewTask(page.RawMap(), page.FileMetadata, page.Content)

	rawStatus := task.GetString("status")
	status := task.Status()
	if rawStatus != "" && status == "" {
		return TaskItem{}, "unknown status " + rawStatus, false
	}

	phase := ""
	if rawPhase := task.GetString("phase"); rawPhase != "" {
		normalized, ok := domain.NormalizeTaskPhase(rawPhase)
		if !ok {
			return TaskItem{}, "unknown phase " + rawPhase, false
		}
		phase = string(normalized)
	}

	id := task.TaskIdentifier()
	if id == "" {
		id = slugify(page.Name)
	}
	goals := task.Goals()
	if goals == nil {
		goals = []string{}
	}
	return TaskItem{
		ID:       id,
		Title:    page.Name,
		Path:     relPath(i.cfg.VaultPath, page.FilePath),
		Status:   string(status),
		Phase:    phase,
		TaskType: task.GetString("task_type"),
		Stage:    task.GetString("stage"),
		Assignee: task.Assignee(),
		Goals:    goals,
		Cluster:  optionalString(task.GetString("cluster")),
		Flag:     task.Flag(),
		Vault:    i.cfg.VaultName,
	}, "", true
}

func (i *Index) buildGoals(
	ctx context.Context,
	store storage.PageStorage,
) ([]GoalItem, map[string]string, int, map[string]int, error) {
	dir := filepath.Join(i.cfg.VaultPath, i.cfg.GoalsDir)
	files, err := listMarkdownFiles(dir)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	pages, err := store.ListPages(ctx, i.cfg.VaultPath, i.cfg.GoalsDir)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	goals := make([]GoalItem, 0, len(pages))
	invalid := make(map[string]string)
	byID := make(map[string]int, len(pages))

	readable := make(map[string]struct{}, len(pages))
	for _, page := range pages {
		readable[page.Name] = struct{}{}
	}
	warnings := 0
	for _, name := range files {
		if _, ok := readable[name]; ok {
			continue
		}
		warnings++
		invalid[slugify(name)] = "unparseable frontmatter"
		invalid[name] = "unparseable frontmatter"
	}

	for _, page := range pages {
		slug := slugify(page.Name)
		rawStatus := page.GetString("status")
		if rawStatus != "" {
			if err := domain.GoalStatus(rawStatus).Validate(ctx); err != nil {
				warnings++
				invalid[slug] = "unknown status " + rawStatus
				invalid[page.Name] = "unknown status " + rawStatus
				continue
			}
		}
		themes := page.GetStringSlice("themes")
		if themes == nil {
			themes = []string{}
		}
		id := page.GetString("goal_identifier")
		if id == "" {
			id = slug
		}
		item := GoalItem{
			ID:       id,
			Title:    page.Name,
			Path:     relPath(i.cfg.VaultPath, page.FilePath),
			Status:   rawStatus,
			Themes:   themes,
			Assignee: page.Assignee(),
			Vault:    i.cfg.VaultName,
		}
		idx := len(goals)
		goals = append(goals, item)
		byID[id] = idx
		byID[page.Name] = idx
		byID[slug] = idx
	}

	return goals, invalid, warnings, byID, nil
}

// QueryTasks returns the tasks matching filter, the current warning count and
// the index age in seconds.
func (i *Index) QueryTasks(filter TaskFilter) ([]TaskItem, int, int64) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.filterTasks(filter), i.taskWarnings, ageSeconds(i.verifiedAt)
}

func (i *Index) filterTasks(filter TaskFilter) []TaskItem {
	sets := make([]map[int]struct{}, 0, 5)
	if filter.Assignee != "" {
		sets = append(sets, i.taskSets.assignee[filter.Assignee])
	}
	if filter.Status != "" {
		sets = append(sets, i.taskSets.status[filter.Status])
	}
	if filter.Phase != "" {
		sets = append(sets, i.taskSets.phase[filter.Phase])
	}
	if filter.Goal != "" {
		sets = append(sets, i.taskSets.goal[goalKey(filter.Goal)])
	}
	if filter.Cluster != "" {
		sets = append(sets, i.taskSets.cluster[filter.Cluster])
	}
	if len(sets) == 0 {
		return append([]TaskItem(nil), i.tasks...)
	}

	smallest := sets[0]
	for _, set := range sets[1:] {
		if len(set) < len(smallest) {
			smallest = set
		}
	}
	indexes := make([]int, 0, len(smallest))
	for idx := range smallest {
		matched := true
		for _, set := range sets {
			if _, found := set[idx]; !found {
				matched = false
				break
			}
		}
		if matched {
			indexes = append(indexes, idx)
		}
	}
	sort.Ints(indexes)
	items := make([]TaskItem, 0, len(indexes))
	for _, idx := range indexes {
		items = append(items, i.tasks[idx])
	}
	return items
}

// QueryGoals returns every indexed goal, the warning count and the index age.
func (i *Index) QueryGoals() ([]GoalItem, int, int64) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]GoalItem(nil), i.goals...), i.goalWarnings, ageSeconds(i.verifiedAt)
}

// GetTask resolves one task by identifier or filename slug. found is false when
// the id is absent; invalid carries the exclusion reason for a file that was
// refused rather than served.
func (i *Index) GetTask(id string) (TaskItem, bool, string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if idx, ok := i.taskByID[id]; ok {
		return i.tasks[idx], true, ""
	}
	return TaskItem{}, false, i.invalidTasks[id]
}

// GetGoal resolves one goal by identifier or filename slug.
func (i *Index) GetGoal(id string) (GoalItem, bool, string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if idx, ok := i.goalByID[id]; ok {
		return i.goals[idx], true, ""
	}
	return GoalItem{}, false, i.invalidGoals[id]
}

// Readiness reports whether the index is usable and how stale it is.
//
// Ready answers a narrower question than "did a build succeed once": it also
// requires both freshness signals to be inside the staleness window, so a pod
// whose index stopped being confirmed, or whose tree stopped being fetched from
// its remote, reports not-ready instead of serving stale data behind a green
// light. The two signals are separate because they fail independently: a rebuild
// can fail on a live tree, and a tree can freeze under a perfectly healthy loop.
func (i *Index) Readiness() Readiness {
	i.mu.RLock()
	defer i.mu.RUnlock()
	indexAge := ageSeconds(i.verifiedAt)
	readiness := Readiness{
		Ready:           i.built && i.fresh(indexAge),
		Vault:           i.cfg.VaultName,
		IndexAgeSeconds: indexAge,
		Head:            i.head,
		Warnings:        i.taskWarnings + i.goalWarnings,
	}
	if treeAge, known := FetchAge(i.cfg.VaultPath); known {
		readiness.TreeAgeSeconds = &treeAge
		readiness.Ready = readiness.Ready && i.fresh(treeAge)
	}
	return readiness
}

// fresh reports whether one age is inside the staleness window. A zero
// StaleAfter disables the rule, which is the local-run case: a caller serving a
// tree nothing fetches has no remote-freshness claim to make, and inventing one
// would make every local instance report not-ready.
func (i *Index) fresh(ageSeconds int64) bool {
	limit := int64(i.cfg.StaleAfter.Seconds())
	if limit <= 0 {
		return true
	}
	return ageSeconds <= limit
}

// Head returns the working-tree commit the current index was built from.
func (i *Index) Head() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.head
}

func ageSeconds(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}
	return int64(time.Since(at).Seconds())
}

func listMarkdownFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".md"))
	}
	return names, nil
}

// slugify derives the path-based identifier: lowercase, runs of spaces,
// underscores and hyphens collapse to one hyphen, and every other character is
// dropped. "Broken Frontmatter Probe" becomes "broken-frontmatter-probe".
func slugify(name string) string {
	var builder strings.Builder
	previousDash := false
	for _, char := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
			previousDash = false
		case char == ' ' || char == '-' || char == '_' || char == '/':
			if !previousDash && builder.Len() > 0 {
				builder.WriteRune('-')
				previousDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func relPath(vaultPath, filePath string) string {
	rel, err := filepath.Rel(vaultPath, filePath)
	if err != nil {
		return filepath.ToSlash(filePath)
	}
	return filepath.ToSlash(rel)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func goalKey(value string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(value), "[]"))
}
