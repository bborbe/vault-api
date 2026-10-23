// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vaultapi

import (
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Side is one side of a three-way merge: the file's content at that side plus
// the committer time of the commit that produced it. The time is what breaks a
// same-key tie under the last-writer-wins rule.
type Side struct {
	Content       []byte
	CommitterTime time.Time
}

// MergeReason names why a file could not be resolved. The empty value means the
// merge succeeded.
type MergeReason string

const (
	// ReasonUnparsable names a side whose frontmatter does not parse.
	ReasonUnparsable MergeReason = "frontmatter does not parse"
	// ReasonBodyConflict names a body both sides changed differently.
	ReasonBodyConflict MergeReason = "body conflict outside the frontmatter"
	// ReasonLifecycle names status/phase edits that are not a legal transition.
	ReasonLifecycle MergeReason = "lifecycle fields are not a legal transition"
)

// MergeResult is the outcome of resolving a diverged file. Content is the file
// to write when Quarantine is false. When Quarantine is true, Content is empty
// and Reason names why no rule could resolve the file.
type MergeResult struct {
	Content    []byte
	Quarantine bool
	Reason     MergeReason
}

// MergeFile resolves a three-way merge of one vault file.
//
// The merge is per frontmatter key, never per line: a key edited on one side
// only is kept, and a key edited on both sides follows the rule its field class
// names. A body both sides changed differently is not merged at all — it
// quarantines, because concatenating two writers' prose produces a file neither
// of them wrote.
//
// This function performs no I/O. The pull path supplies the three sides and
// writes the merged bytes through whatever write path it owns.
//
// Named for the operation rather than as NewMergeResult: it is not a
// constructor. It takes no context and returns no error for the same reason it
// does no I/O — an unparsable side, an illegal lifecycle pair and a body
// conflict are outcomes of the merge, reported as Quarantine with a Reason, not
// failures of it.
func MergeFile(base, ours, theirs Side) MergeResult {
	baseFile, ok := parseFile(base.Content)
	if !ok {
		return MergeResult{Quarantine: true, Reason: ReasonUnparsable}
	}
	oursFile, ok := parseFile(ours.Content)
	if !ok {
		return MergeResult{Quarantine: true, Reason: ReasonUnparsable}
	}
	theirsFile, ok := parseFile(theirs.Content)
	if !ok {
		return MergeResult{Quarantine: true, Reason: ReasonUnparsable}
	}

	merged, keys, ok := mergeKeys(baseFile, oursFile, theirsFile, ours, theirs)
	if !ok {
		return MergeResult{Quarantine: true, Reason: ReasonLifecycle}
	}

	body, ok := mergeBody(baseFile.body, oursFile.body, theirsFile.body)
	if !ok {
		return MergeResult{Quarantine: true, Reason: ReasonBodyConflict}
	}

	return MergeResult{Content: render(merged, keys, body)}
}

// file is one side of the merge, split into its frontmatter keys and its body.
type file struct {
	values map[string]*yaml.Node
	order  []string
	body   string
}

// parseFile splits a vault file into frontmatter and body. A file with no
// frontmatter block parses to an empty key set and a body holding the whole
// content, so a body-only file merges as a pure body comparison.
func parseFile(content []byte) (*file, bool) {
	text := string(content)
	if !strings.HasPrefix(text, "---\n") {
		return &file{values: map[string]*yaml.Node{}, body: text}, true
	}
	rest := text[len("---\n"):]
	header, body, found := strings.Cut(rest, "\n---")
	if !found {
		return nil, false
	}
	body = strings.TrimPrefix(body, "\n")

	var document yaml.Node
	if err := yaml.Unmarshal([]byte(header), &document); err != nil {
		return nil, false
	}
	result := &file{values: map[string]*yaml.Node{}, body: body}
	if len(document.Content) == 0 {
		return result, true
	}
	mapping := document.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, false
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i].Value
		if _, exists := result.values[key]; !exists {
			result.order = append(result.order, key)
		}
		result.values[key] = mapping.Content[i+1]
	}
	return result, true
}

// mergeKeys resolves every frontmatter key. ok is false when the lifecycle rule
// found no single legal result and the caller must quarantine.
func mergeKeys(
	base, ours, theirs *file,
	oursSide, theirsSide Side,
) (map[string]*yaml.Node, []string, bool) {
	keys := keyOrder(base, ours, theirs)
	merged := make(map[string]*yaml.Node, len(keys))
	kept := make([]string, 0, len(keys))

	for _, key := range keys {
		value, keep, ok := mergeKey(
			key,
			base.values[key],
			ours.values[key],
			theirs.values[key],
			oursSide,
			theirsSide,
		)
		if !ok {
			return nil, nil, false
		}
		if !keep {
			continue
		}
		merged[key] = value
		kept = append(kept, key)
	}
	return merged, kept, true
}

// mergeKey resolves one key. keep is false when both sides deleted it.
func mergeKey(
	key string,
	base, ours, theirs *yaml.Node,
	oursSide, theirsSide Side,
) (value *yaml.Node, keep bool, ok bool) {
	switch {
	case equalNodes(ours, theirs):
		return ours, ours != nil, true
	case equalNodes(theirs, base):
		return ours, ours != nil, true
	case equalNodes(ours, base):
		return theirs, theirs != nil, true
	}

	// Both sides changed the key differently. metrics_sessions is matched
	// before the counter rule: it shares the metrics_ prefix but holds a list,
	// so the integer max would quarantine it.
	switch {
	case key == "metrics_sessions":
		return concatList(ours, theirs), true, true
	case isCounterKey(key):
		return maxCounter(ours, theirs)
	case isLifecycleKey(key):
		return lifecycleValue(key, base, ours, theirs)
	case isAppendOnlyListKey(key):
		return unionList(ours, theirs), true, true
	default:
		if theirsSide.CommitterTime.After(oursSide.CommitterTime) {
			return theirs, true, true
		}
		return ours, true, true
	}
}

// keyOrder lists every key across the three sides: base order first, then keys
// each side added, so a re-emitted file keeps its familiar shape.
func keyOrder(base, ours, theirs *file) []string {
	seen := map[string]bool{}
	keys := make([]string, 0, len(base.order)+len(ours.order)+len(theirs.order))
	for _, side := range []*file{base, ours, theirs} {
		for _, key := range side.order {
			if seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
}

func isCounterKey(key string) bool {
	return key == "trigger_count" || key == "retry_count" || strings.HasPrefix(key, "metrics_")
}

func isLifecycleKey(key string) bool {
	return key == "status" || key == "phase"
}

func isAppendOnlyListKey(key string) bool {
	return key == "goals" || key == "themes" || key == "blocked_by"
}

// maxCounter keeps the larger integer of the two sides.
func maxCounter(ours, theirs *yaml.Node) (*yaml.Node, bool, bool) {
	oursValue, oursOK := intValue(ours)
	theirsValue, theirsOK := intValue(theirs)
	if !oursOK || !theirsOK {
		return nil, false, false
	}
	if theirsValue > oursValue {
		return theirs, true, true
	}
	return ours, true, true
}

// lifecycleValue accepts a side only when it is a legal successor of the base
// value and the other side is not. Two different legal successors, or none,
// quarantine: the transition graph cannot pick between them.
func lifecycleValue(key string, base, ours, theirs *yaml.Node) (*yaml.Node, bool, bool) {
	baseValue := scalarValue(base)
	oursLegal := isLegalTransition(key, baseValue, scalarValue(ours))
	theirsLegal := isLegalTransition(key, baseValue, scalarValue(theirs))

	switch {
	case oursLegal && !theirsLegal:
		return ours, true, true
	case theirsLegal && !oursLegal:
		return theirs, true, true
	case oursLegal && theirsLegal && equalNodes(ours, theirs):
		return ours, true, true
	default:
		return nil, false, false
	}
}

// isLegalTransition reports whether to is a legal successor of from.
func isLegalTransition(key, from, to string) bool {
	if from == to {
		return true
	}
	successors := map[string]map[string]map[string]bool{
		"status": {
			"backlog":     {"next": true, "in_progress": true},
			"next":        {"in_progress": true, "hold": true, "aborted": true},
			"in_progress": {"completed": true, "aborted": true, "hold": true},
			"hold":        {"in_progress": true, "aborted": true},
		},
		"phase": {
			"todo":         {"planning": true},
			"planning":     {"execution": true},
			"execution":    {"ai_review": true},
			"ai_review":    {"human_review": true},
			"human_review": {"done": true},
		},
	}
	allowed, known := successors[key]
	if !known {
		return false
	}
	return allowed[from][to]
}

// unionList appends theirs after ours, dropping duplicates and keeping order.
func unionList(ours, theirs *yaml.Node) *yaml.Node {
	oursItems, oursOK := listValue(ours)
	theirsItems, theirsOK := listValue(theirs)
	if !oursOK || !theirsOK {
		return ours
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(oursItems)+len(theirsItems))
	for _, item := range append(append([]string{}, oursItems...), theirsItems...) {
		if seen[item] {
			continue
		}
		seen[item] = true
		result = append(result, item)
	}
	return stringListNode(result)
}

// concatList appends theirs after ours, keeping duplicates: metrics_sessions
// records every session that touched the task, so a union would drop history.
func concatList(ours, theirs *yaml.Node) *yaml.Node {
	oursItems, oursOK := listValue(ours)
	theirsItems, theirsOK := listValue(theirs)
	if !oursOK || !theirsOK {
		return ours
	}
	return stringListNode(append(append([]string{}, oursItems...), theirsItems...))
}

// mergeBody takes a side's body when only one side changed it. Two different
// changes are a human decision, so they quarantine.
func mergeBody(base, ours, theirs string) (string, bool) {
	switch {
	case ours == theirs:
		return ours, true
	case ours == base:
		return theirs, true
	case theirs == base:
		return ours, true
	default:
		return "", false
	}
}

// render rebuilds the file from the merged keys and body.
func render(values map[string]*yaml.Node, keys []string, body string) []byte {
	if len(keys) == 0 {
		return []byte(body)
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	for _, key := range keys {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			values[key],
		)
	}
	encoded, err := yaml.Marshal(mapping)
	if err != nil {
		return []byte(body)
	}
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.Write(encoded)
	builder.WriteString("---\n")
	builder.WriteString(body)
	return []byte(builder.String())
}

func equalNodes(left, right *yaml.Node) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return encode(left) == encode(right)
}

func encode(node *yaml.Node) string {
	encoded, err := yaml.Marshal(node)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func scalarValue(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	return node.Value
}

func intValue(node *yaml.Node) (int, bool) {
	if node == nil {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(node.Value))
	if err != nil {
		return 0, false
	}
	return value, true
}

func listValue(node *yaml.Node) ([]string, bool) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, false
	}
	items := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		items = append(items, item.Value)
	}
	return items, true
}

func stringListNode(items []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode}
	for _, item := range items {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: item})
	}
	return node
}
