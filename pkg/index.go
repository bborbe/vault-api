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

	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	pageindex "github.com/bborbe/vault-cli/pkg/index"
	"github.com/bborbe/vault-cli/pkg/storage"
	"github.com/golang/glog"
)

// Config locates the vault working tree and the directories the index reads.
type Config struct {
	VaultPath string
	VaultName string
	TasksDir  string
	GoalsDir  string
	// TopicsDir is the directory the topic pages live in. Empty disables topic
	// indexing, which is what a caller that only serves tasks and goals wants.
	TopicsDir string
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
	// TaskTypes is a set: a task matches when its type is any one of them, so a
	// caller whose population spans several types asks once instead of issuing a
	// query per type. A single-value filter is the one-element case.
	//
	// Two consequences a caller should know. An empty slice means "unset" like
	// every other field here, but a slice holding one empty string means "match
	// nothing" — build it from parsed input rather than from an optional field.
	// And a value that resolves to no task is silently absorbed when another value
	// matches: {a, typo} returns the a-tasks rather than nothing. That tolerance is
	// deliberate, because a type with no pages yet is legitimate, but a dedup
	// caller must not rely on the result being complete — it is the caller's own
	// set that a dropped type would silently shrink.
	TaskTypes []string
	ShortID   string
	// UpdatedSince keeps only tasks whose page was modified at or after it. It is
	// a pointer so "no bound" and "modified at the zero instant" stay distinct.
	UpdatedSince *time.Time
	// Flag, when non-nil, keeps only tasks whose frontmatter flag equals it. A
	// pointer because "unset" and "flag: false" are different queries.
	Flag *bool
	// DueNow keeps only tasks due at Now: defer_date absent (nothing postpones it)
	// or defer_date at or before Now. Now must be set when DueNow is true.
	DueNow bool
	// Now is the instant DueNow measures against. It is injected rather than read
	// from the wall clock so a test can pin the boundary.
	Now time.Time
}

// Index is the in-memory view of the vault. It is rebuilt at startup and again
// whenever the working tree's HEAD changes, so list queries are map lookups
// rather than per-file reads.
type Index struct {
	cfg     Config
	metrics Metrics

	mu            sync.RWMutex
	tasks         []TaskItem
	goals         []GoalItem
	topics        []TopicItem
	taskByID      map[string]int
	goalByID      map[string]int
	invalidTasks  map[string]string
	invalidGoals  map[string]string
	taskSets      taskSets
	taskWarnings  int
	goalWarnings  int
	topicWarnings int
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
	// generation counts every commit to the index — a Build swap and an UpsertTask
	// alike. A Build reads the tree outside the lock, so it captures this before
	// reading and refuses its own swap if a write committed in between; without it
	// a slow rebuild would silently undo a create that landed while it was reading.
	generation uint64
}

// currentGeneration reads the commit counter under the lock.
func (i *Index) currentGeneration() uint64 {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.generation
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
	taskType indexSet
	shortID  indexSet
}

func newTaskSets() taskSets {
	return taskSets{
		assignee: indexSet{},
		status:   indexSet{},
		phase:    indexSet{},
		cluster:  indexSet{},
		goal:     indexSet{},
		taskType: indexSet{},
		shortID:  indexSet{},
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
	// Snapshot the generation BEFORE the reads. They run outside the lock and take
	// seconds over a full vault, so a create can land in between; committing this
	// build's snapshot would then overwrite that create's upsert with a view of the
	// tree that predates it, re-opening the 404 the upsert exists to close.
	generation := i.currentGeneration()
	pages := newBuildPageIndex(store)
	tasks, taskInvalid, taskWarnings, taskByID, sets, err := i.buildTasks(ctx, pages)
	if err != nil {
		i.markUnbuilt()
		return err
	}
	goals, goalInvalid, goalWarnings, goalByID, err := i.buildGoals(ctx, pages)
	if err != nil {
		i.markUnbuilt()
		return err
	}
	topics, topicWarnings, err := i.buildTopics(ctx, pages)
	if err != nil {
		i.markUnbuilt()
		return err
	}

	i.mu.Lock()
	if i.generation != generation {
		// A write committed while this build was reading. Committing the snapshot
		// would drop it, so the build is abandoned rather than applied: the index
		// keeps serving what it had (which includes the write), `head` stays behind
		// the tree, and the refresh loop rebuilds on its next tick.
		i.mu.Unlock()
		glog.Warningf(
			"index build abandoned: a write committed while it was reading; "+
				"keeping the current index (generation %d -> %d)",
			generation,
			i.currentGeneration(),
		)
		return nil
	}
	i.generation++
	i.tasks = tasks
	i.taskByID = taskByID
	i.invalidTasks = taskInvalid
	i.taskSets = sets
	i.goals = goals
	i.goalByID = goalByID
	i.invalidGoals = goalInvalid
	i.taskWarnings = taskWarnings
	i.goalWarnings = goalWarnings
	i.topics = topics
	i.topicWarnings = topicWarnings
	i.verifiedAt = time.Now()
	i.head = ResolveHead(i.cfg.VaultPath)
	i.built = true
	i.mu.Unlock()

	// Published after the swap so a scrape never sees the new counts against
	// the old index.
	i.metrics.SetOpenTasks(openTasksByState(tasks))
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

// openTaskJobStateOf reports whether a task carries a claimed job. The test is
// frontmatter presence alone: vault-api never polls the executor, so a job that
// died leaves its id behind and reads running until the next dispatch overwrites
// it. That is the same reading the board's "is anyone working this" question uses.
func openTaskJobStateOf(task TaskItem) string {
	if task.CurrentJob == "" {
		return JobNone
	}
	return JobRunning
}

// openTasksByState counts the indexed tasks whose status is neither completed nor
// aborted, per frontmatter stage, task_type, phase, job presence, PR state and
// assignee. A task with no stage, no task_type or no phase counts under the
// matching unknown bucket, and one with no status counts as open — an unparseable
// status is what excludes a page, not a missing one (see buildTasks). An empty
// assignee is left empty here and coerced to AssigneeUnassigned at the gauge, the
// same division of labour job_state uses.
func openTasksByState(tasks []TaskItem) map[OpenTaskKey]int {
	counts := make(map[OpenTaskKey]int, len(tasks))
	for _, task := range tasks {
		if IsTerminalStatus(task.Status) {
			continue
		}
		key := OpenTaskKey{
			Stage:    task.Stage,
			TaskType: task.TaskType,
			Phase:    task.Phase,
			JobState: openTaskJobStateOf(task),
			PRState:  task.PRState,
			Assignee: task.Assignee,
		}
		if key.Stage == "" {
			key.Stage = StageUnknown
		}
		if key.TaskType == "" {
			key.TaskType = TaskTypeUnknown
		}
		if key.Phase == "" {
			key.Phase = PhaseUnknown
		}
		if key.PRState == "" {
			key.PRState = PRStateUnknown
		}
		counts[key]++
	}
	return counts
}

func (i *Index) buildTasks(
	ctx context.Context,
	pages pageindex.PageIndex,
) ([]TaskItem, map[string]string, int, map[string]int, taskSets, error) {
	dir := filepath.Join(i.cfg.VaultPath, i.cfg.TasksDir)
	files, err := listMarkdownFiles(dir)
	if err != nil {
		return nil, nil, 0, nil, taskSets{}, err
	}
	listed, err := pages.ListPages(ctx, i.cfg.VaultPath, i.cfg.TasksDir)
	if err != nil {
		return nil, nil, 0, nil, taskSets{}, err
	}

	tasks := make([]TaskItem, 0, len(listed))
	invalid := make(map[string]string)
	byID := make(map[string]int, len(listed))
	sets := newTaskSets()

	readable := make(map[string]struct{}, len(listed))
	for _, page := range listed {
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

	for _, page := range listed {
		item, reason, ok := i.taskItem(page)
		if !ok {
			warnings++
			invalid[slugify(page.Name)] = reason
			invalid[page.Name] = reason
			continue
		}

		idx := len(tasks)
		tasks = append(tasks, item)
		registerTask(byID, sets, item, idx)
	}

	return tasks, invalid, warnings, byID, sets, nil
}

// registerTask records one task's lookup keys and filter sets at position idx.
// It is shared by the full build and the incremental upsert so the two cannot
// disagree about which keys address a task or which set it belongs to.
func registerTask(byID map[string]int, sets taskSets, item TaskItem, idx int) {
	byID[item.ID] = idx
	byID[item.Title] = idx
	byID[slugify(item.Title)] = idx
	sets.assignee.add(item.Assignee, idx)
	sets.status.add(item.Status, idx)
	sets.phase.add(item.Phase, idx)
	if item.Cluster != nil {
		sets.cluster.add(*item.Cluster, idx)
	}
	if item.TaskType != "" {
		sets.taskType.add(item.TaskType, idx)
	}
	if item.ShortID != "" {
		sets.shortID.add(item.ShortID, idx)
	}
	for _, goal := range item.Goals {
		sets.goal.add(goalKey(goal), idx)
	}
}

// UpsertTask adds or replaces the indexed task for page. The write path calls it
// after a create or a reopen, so the task it just wrote is readable by
// identifier immediately.
//
// Without it, a create and the read that follows it disagree for up to one
// refresh interval. The create writes the file and answers 201; the read path
// resolves identifiers through this index; and the index is rebuilt only when
// the working tree's HEAD moves, on the refresh ticker. A caller that creates a
// task and then addresses it — the executor's probe publishes create-task and
// update-frontmatter back to back — therefore gets a 404 for a task that exists,
// and the follow-up write is dropped. Measured on dev 2026-11-09: the update ran
// 4 ms after the create and reported "task not found".
//
// It does NOT make a genuine miss resolvable: nothing is recorded for an
// identifier no file carries, so an unknown identifier still answers 404. That
// distinction is the point — the read path must stay able to say "no such task",
// which is why the create answers with the task it stored rather than the read
// path being made lenient.
//
// The byID map and the filter sets are rebuilt from the slice rather than
// patched in place: a replace has to drop the old item's keys and set
// memberships, and rebuilding is O(tasks) over memory already held, with no file
// read — the cost this index exists to avoid is I/O, not map writes.
func (i *Index) UpsertTask(page *domain.Page) {
	item, _, ok := i.taskItem(page)
	if !ok {
		// A page the full build would exclude must not enter through the write
		// path either: the two have to agree about what is indexed.
		return
	}
	i.mu.Lock()
	// A FRESH slice, never an in-place write. Build hands its slice to
	// openTasksByState after releasing the lock, so mutating an element of the
	// published array would race with a reader that is still counting it. Replacing
	// the slice leaves every array already handed out immutable.
	// Matched by PATH, and by nothing else. The path is what the write actually
	// addressed, so replacing on it cannot touch a different page. An identifier
	// fallback looks helpful for a rename and is not: prepareCreateFrontmatter
	// already refuses a create whose identifier is carried at another path, and that
	// guard is not atomic with this upsert — so two racing creates sharing one
	// identifier could reach the fallback and evict each other's entry, leaving both
	// files sharing an address until the next Build.
	replaceAt := -1
	for idx, existing := range i.tasks {
		if existing.Path == item.Path {
			replaceAt = idx
			break
		}
	}
	tasks := make([]TaskItem, 0, len(i.tasks)+1)
	for idx, existing := range i.tasks {
		if idx == replaceAt {
			tasks = append(tasks, item)
			continue
		}
		tasks = append(tasks, existing)
	}
	if replaceAt < 0 {
		tasks = append(tasks, item)
	}
	byID := make(map[string]int, len(tasks))
	sets := newTaskSets()
	for idx, task := range tasks {
		registerTask(byID, sets, task, idx)
	}
	i.generation++
	i.tasks = tasks
	i.taskByID = byID
	i.taskSets = sets
	// A page that previously failed to parse is indexed now, so its warning entry
	// has to go: leaving it would keep a healthy task reported as invalid, and
	// leaving the count alone would keep /readiness reporting a warning that no
	// longer describes the tree.
	//
	// ⚠️ The count decrements ONCE per page, never once per key. buildTasks records
	// each invalid page under its name AND its slug but increments taskWarnings
	// once, so decrementing per key would take two off for one page and make
	// /readiness under-report the remaining warnings.
	warned := false
	for _, key := range []string{item.Title, slugify(item.Title)} {
		if _, present := i.invalidTasks[key]; present {
			delete(i.invalidTasks, key)
			warned = true
		}
	}
	if warned && i.taskWarnings > 0 {
		i.taskWarnings--
	}
	// Counted under the lock for the same reason the slice is fresh: the map must
	// describe the index as it stood when this write committed, not a state a
	// concurrent upsert has since replaced.
	counts := openTasksByState(tasks)
	i.mu.Unlock()

	// Published after the swap, like Build, so a scrape never sees the new counts
	// against the old index.
	i.metrics.SetOpenTasks(counts)
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
		ID:        id,
		Title:     page.Name,
		Path:      relPath(i.cfg.VaultPath, page.FilePath),
		Status:    string(status),
		Phase:     phase,
		TaskType:  task.GetString("task_type"),
		ShortID:   task.GetString("short_id"),
		Stage:     task.GetString("stage"),
		Assignee:  task.Assignee(),
		Goals:     goals,
		Cluster:   optionalString(task.GetString("cluster")),
		Flag:      task.Flag(),
		Vault:     i.cfg.VaultName,
		UpdatedAt: page.ModifiedDate,
		// Raw frontmatter, uncoerced: the gauge maps an empty value onto its
		// sentinel (JobNone / PRStateUnknown), so the two readings stay distinct
		// here and one mapping site owns the coercion.
		CurrentJob: task.GetString("current_job"),
		PRState:    task.GetString("pr_state"),
		DeferDate:  deferDate(task),
	}, "", true
}

func (i *Index) buildGoals(
	ctx context.Context,
	pages pageindex.PageIndex,
) ([]GoalItem, map[string]string, int, map[string]int, error) {
	dir := filepath.Join(i.cfg.VaultPath, i.cfg.GoalsDir)
	files, err := listMarkdownFiles(dir)
	if err != nil {
		return nil, nil, 0, nil, err
	}
	listed, err := pages.ListPages(ctx, i.cfg.VaultPath, i.cfg.GoalsDir)
	if err != nil {
		return nil, nil, 0, nil, err
	}

	goals := make([]GoalItem, 0, len(listed))
	invalid := make(map[string]string)
	byID := make(map[string]int, len(listed))

	readable := make(map[string]struct{}, len(listed))
	for _, page := range listed {
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

	for _, page := range listed {
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
	sets := make([]map[int]struct{}, 0, 7)
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
	if len(filter.TaskTypes) > 0 {
		sets = append(sets, i.unionTaskTypes(filter.TaskTypes))
	}
	if filter.ShortID != "" {
		sets = append(sets, i.taskSets.shortID[filter.ShortID])
	}
	indexes := i.matchingIndexes(sets)
	items := make([]TaskItem, 0, len(indexes))
	for _, idx := range indexes {
		if !updatedSince(i.tasks[idx], filter.UpdatedSince) {
			continue
		}
		if filter.Flag != nil && i.tasks[idx].Flag != *filter.Flag {
			continue
		}
		if filter.DueNow && !dueAt(i.tasks[idx], filter.Now) {
			continue
		}
		items = append(items, i.tasks[idx])
	}
	return items
}

// dueAt reports whether a task is due at now: its defer_date is absent, or it has
// arrived. An absent defer_date is due by definition — nothing postpones the row.
func dueAt(item TaskItem, now time.Time) bool {
	if item.DeferDate == nil {
		return true
	}
	return !item.DeferDate.After(now)
}

// deferDate projects a task's defer_date to a *time.Time. An absent field is nil,
// which the due filter reads as "not postponed" rather than "unknown".
func deferDate(task *domain.Task) *time.Time {
	d := task.DeferDate()
	if d == nil {
		return nil
	}
	t := time.Time(*d)
	return &t
}

// unionTaskTypes returns every task whose type is any one of types. It is a union
// rather than an intersection because the values are alternatives, not
// constraints: a caller asking for two types wants both populations, while the
// other filters still narrow by intersection.
func (i *Index) unionTaskTypes(types []string) map[int]struct{} {
	combined := make(map[int]struct{})
	for _, taskType := range types {
		for idx := range i.taskSets.taskType[taskType] {
			combined[idx] = struct{}{}
		}
	}
	return combined
}

// matchingIndexes returns the task indexes present in every set, or all of them
// when no set was named. It walks the smallest set, so a narrow query costs the
// narrow set rather than the whole vault.
func (i *Index) matchingIndexes(sets []map[int]struct{}) []int {
	if len(sets) == 0 {
		indexes := make([]int, 0, len(i.tasks))
		for idx := range i.tasks {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)
		return indexes
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
	return indexes
}

// updatedSince reports whether an item falls inside the time bound. A task whose
// page reported no modification time cannot be shown to be inside it, so it is
// excluded rather than guessed at — a silently included stale task is the
// failure a time filter exists to prevent.
func updatedSince(item TaskItem, since *time.Time) bool {
	if since == nil {
		return true
	}
	return item.UpdatedAt != nil && !item.UpdatedAt.Before(*since)
}

// QueryGoals returns every indexed goal, the warning count and the index age.
func (i *Index) QueryGoals() ([]GoalItem, int, int64) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]GoalItem(nil), i.goals...), i.goalWarnings, ageSeconds(i.verifiedAt)
}

// QueryTopics returns every indexed topic, the warning count and the index age.
func (i *Index) QueryTopics() ([]TopicItem, int, int64) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]TopicItem(nil), i.topics...), i.topicWarnings, ageSeconds(i.verifiedAt)
}

// buildTopics projects the topic pages. A missing topic directory is not an
// error: a vault that has none simply serves an empty list, and failing the whole
// build over it would take the task read path down with it.
func (i *Index) buildTopics(
	ctx context.Context,
	pages pageindex.PageIndex,
) ([]TopicItem, int, error) {
	if i.cfg.TopicsDir == "" {
		return nil, 0, nil
	}
	dir := filepath.Join(i.cfg.VaultPath, i.cfg.TopicsDir)
	files, err := listMarkdownFiles(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	listed, err := pages.ListPages(ctx, i.cfg.VaultPath, i.cfg.TopicsDir)
	if err != nil {
		return nil, 0, err
	}

	readable := make(map[string]struct{}, len(listed))
	for _, page := range listed {
		readable[page.Name] = struct{}{}
	}
	warnings := 0
	for _, name := range files {
		if _, ok := readable[name]; !ok {
			warnings++
		}
	}

	topics := make([]TopicItem, 0, len(listed))
	for _, page := range listed {
		slug := slugify(page.Name)
		id := page.GetString("topic_identifier")
		if id == "" {
			id = slug
		}
		goals := page.GetStringSlice("goals")
		if goals == nil {
			goals = []string{}
		}
		themes := page.GetStringSlice("themes")
		if themes == nil {
			themes = []string{}
		}
		topics = append(topics, TopicItem{
			ID:       id,
			Title:    page.Name,
			Path:     relPath(i.cfg.VaultPath, page.FilePath),
			Status:   page.GetString("status"),
			Goals:    goals,
			Themes:   themes,
			Assignee: page.Assignee(),
			Vault:    i.cfg.VaultName,
		})
	}
	return topics, warnings, nil
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

// newBuildPageIndex returns a fresh vault-cli metadata page index over store.
// Each Build gets its own, so a rebuild always reads the tree as it is now and
// carries no snapshot over from an earlier one; per-request body reads keep
// going through store's ReadPage, never through this index.
func newBuildPageIndex(store storage.PageStorage) pageindex.PageIndex {
	idx := pageindex.NewPageIndex(
		pageindex.NewPageReader(store),
		pageindex.NewDirectoryLister(),
		libtime.NewCurrentDateTime(),
		libtime.NewWaiterDuration(),
		nil,
	)
	pageindex.SetWarnf(idx, glog.Warningf)
	return idx
}
