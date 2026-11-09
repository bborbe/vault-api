---
assignee: ""
base_ref: master
clone_url: https://github.com/bborbe/claude-supervisor.git
current_job: ""
job_started_at: "2026-11-08T17:49:19Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:37:57Z"
ref: a3f37ea47b1190f947a4c7497f4934afebf3fa7e
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 7c553803-bd20-524b-8118-5efb62825a03
task_type: pr-review
title: 'fix: report a worker that wrote nothing as a failure, not an idle row'
trigger_count: 3
trigger_scope: planning:a3f37ea4
---
# PR Review: fix: report a worker that wrote nothing as a failure, not an idle row



https://github.com/bborbe/claude-supervisor/pull/698

**Repo:** [bborbe/claude-supervisor](https://github.com/bborbe/claude-supervisor)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/claude-supervisor/pull/698",
  "pr_title": "fix: report a worker that wrote nothing as a failure, not an idle row",
  "base_branch": "master",
  "head_branch": "feat/wrote-nothing-check",
  "files_changed": [
    "CHANGELOG.md",
    "Makefile",
    "agents/fleet-drive.md",
    "scripts/budget-baseline.json",
    "scripts/check-wrote-nothing.py"
  ],
  "scope": "bugfix",
  "focus_areas": ["correctness", "tests"],
  "concerns": [
    {
      "area": "correctness",
      "file": "agents/fleet-drive.md",
      "note": "New step 1a classifies a task as wrote-nothing when task-file mtime is at or before metrics_sessions[].started_at. Verify the equality case is safe given filesystem mtime granularity and possible clock skew between the session-start timestamp and the writer host; a worker that wrote within the same second as spawn could be misclassified as a failure."
    },
    {
      "area": "correctness",
      "file": "agents/fleet-drive.md",
      "note": "The rule only fires for task execution with open boxes and only when a metrics_sessions entry exists. Check whether plan-phase tasks, missing/unreadable task files, or rows with no metrics_sessions entry (the fallback says the check did not run) leave the exact failure this PR targets still invisible."
    },
    {
      "area": "correctness",
      "file": "agents/fleet-drive.md",
      "note": "The rule assumes task-file mtime changes only when the worker writes. Check whether any other leg (manager, sweep reader, handoff tooling) touches the task file, which would make an idle worker look productive and defeat the positive control."
    },
    {
      "area": "tests",
      "file": "scripts/check-wrote-nothing.py",
      "note": "The guard is a case-folded substring-presence check over prose only. It would pass if the markers appeared in a negated or contradictory sentence, and it does not exercise the mtime-vs-started_at comparison at all. Consider whether any behavioral test or fixture is feasible, or at least whether marker checks are ordered/anchor-validated."
    },
    {
      "area": "tests",
      "file": "scripts/check-wrote-nothing.py",
      "note": "FORBIDDEN_SOURCE requires the literal phrase never `claude_session_started`. Any reword that keeps the rule intact (e.g. do not use claude_session_started) breaks the guard. The case-folding mitigates capitalization but not rewording, which is the stated intent of the guard."
    },
    {
      "area": "correctness",
      "file": "scripts/budget-baseline.json",
      "note": "check-budget fails both ways, so the raised agents/fleet-drive.md entry (30505 to 31686) must exactly match the post-change file size. Verify the recorded byte count against the actual file; a mismatch makes precommit fail regardless of content correctness."
    },
    {
      "area": "tests",
      "file": "agents/fleet-drive.md",
      "note": "PR description states the rule's live probe (a real fleet round producing a digest line with both timestamps) was not run. End-to-end evidence that the new step fires is absent; assess whether prose-level verification is sufficient for an agent-instruction change."
    }
  ]
}
```
## Resolution

```json
{
  "verdict": "superseded",
  "pr_state": "OPEN",
  "head_ref_oid": "04f1a352c80af6798f63a25949eb8c0af7bb5ded"
}
```
## Trigger Cap Escalation

- **Timestamp:** 2026-11-08T18:19:44Z
- **Trigger count:** 3
- **Max triggers:** 3
- **Assignee:** 
- **Last agent output:** see `## Result` above
