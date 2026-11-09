---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/claude-supervisor.git
current_job: ""
job_started_at: "2026-11-08T18:31:22Z"
phase: done
queued_at: "2026-11-08T17:45:05Z"
ref: 1a95520145013b9adbc92c63b1af0f7c36cab8a1
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: e2449008-d24e-58a3-ac4c-61084b9a8a87
task_type: pr-review
title: 'docs: give the keep-alive contract one home in session-tiers.md § Keep-alive'
trigger_count: 1
trigger_scope: planning:1a955201
---
# PR Review: docs: give the keep-alive contract one home in session-tiers.md § Keep-alive


https://github.com/bborbe/claude-supervisor/pull/699

**Repo:** [bborbe/claude-supervisor](https://github.com/bborbe/claude-supervisor)
## Plan


```json
{
  "pr_url": "https://github.com/bborbe/claude-supervisor/pull/699",
  "pr_title": "docs: give the keep-alive contract one home in session-tiers.md § Keep-alive",
  "base_branch": "master",
  "head_branch": "docs/keepalive-contract",
  "files_changed": [
    "CHANGELOG.md",
    "agents/topic-drive.md",
    "commands/open.md",
    "commands/steps/keepalive.md",
    "commands/topic-drive.md",
    "docs/fleet-surface.md",
    "docs/session-tiers.md",
    "scripts/budget-baseline.json",
    "scripts/fleet-keepalive.py",
    "scripts/topic-keepalive.py",
    "scripts/worker-keepalive.py"
  ],
  "scope": "docs",
  "focus_areas": ["correctness", "tests"],
  "concerns": [
    {
      "area": "correctness",
      "file": "docs/session-tiers.md",
      "note": "New Keep-alive intro lists agents/goal-drive.md, commands/topic-loop.md and scripts/topic-spawn.py as files that point at the section, but none of the three is in the diff and the PR body says topic-loop.md and topic-spawn.py are deliberately left untouched — verify the intro does not overclaim."
    },
    {
      "area": "correctness",
      "file": "agents/topic-drive.md",
      "note": "Rewritten warning adds `restart` alongside `resume` as a keep-alive arm; confirm `restart` is a verdict/arm actually emitted by scripts/topic-keepalive.py, otherwise the doc names a nonexistent arm."
    },
    {
      "area": "tests",
      "file": "scripts/budget-baseline.json",
      "note": "commands/open.md budget is lowered from 79230 to 79227 (claimed 3-byte shrink); verify the head file's exact byte size matches, or make check / the budget gate fails."
    },
    {
      "area": "correctness",
      "file": "docs/session-tiers.md",
      "note": "Owner definition (session LIVE plus loop OK/STALE) is not what worker-keepalive.py goal_manager_live() implements; the section admits this, but the table and owner text must not be read as current behavior by downstream agents."
    },
    {
      "area": "correctness",
      "file": "docs/session-tiers.md",
      "note": "The chain table cites scripts/fleet-keeper.py, scripts/close-worker.py, scripts/manager-liveness.py and scripts/session-liveness.py, and rules 1-4 of § The four tiers; verify all exist and the rule numbering still matches the unchanged rule text."
    },
    {
      "area": "correctness",
      "file": "commands/steps/keepalive.md",
      "note": "The replacement sentence drops the explicit rule 1 citation that justified automatic worker resume; confirm the justification is still reachable and the sentence 'the fleet round is the keeper for the workers § Keep-alive assigns it' reads correctly."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "New Unreleased entry is long and nests multi-line bullets; verify it renders correctly and that no behavior change is implied beyond the docs-only claim."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Docs-only PR that centralizes the keep-alive contract in docs/session-tiers.md § Keep-alive and replaces duplicated prose in six files plus three script docstrings with pointers. The mechanics check out (commands/open.md is exactly 79227 bytes, `restart` is a real topic-keepalive verdict, every cited script and rule number exists) and the two known gaps are honestly stated. One accuracy defect: the new intro lists three files as pointing at the section when none of them does. Selector mode: 14 candidates, 10 applicable, 4 skipped; mechanical funnel clean (0 findings); precommit skipped (selector mode) — CI covers lint+test.",
  "comments": [
    {
      "file": "docs/session-tiers.md",
      "line": 144,
      "severity": "major",
      "blocking": false,
      "blocking_reason": "",
      "message": "The intro's pointer list names `agents/goal-drive.md`, `commands/topic-loop.md` and `scripts/topic-spawn.py` as files that \"point here\", but none references § Keep-alive: `agents/goal-drive.md` has no keep-alive/session-tiers mention at all, `commands/topic-loop.md` points only at § The four tiers, and `scripts/topic-spawn.py` has a generic \"the keep-alive\" code comment with no pointer. The CHANGELOG entry correctly omits these three, so the section's central \"every other file that touches it points here\" claim is false for 3 of its 9 listed files — including `agents/goal-drive.md`, which the chain table at line 162 itself names as the mechanism for the worker-with-a-goal row. Either add the § Keep-alive pointer to those files or move them out of the list. *(rule: `agent-cmd/single-source-of-truth`)*"
    },
    {
      "file": "docs/session-tiers.md",
      "line": 197,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "The second invariant's parenthetical cites rule 2 for a resume claim. Rule 2 (\"A flagged page is opened automatically by the tier above it\") establishes that the flag gates an *open*; the claim that an unflagged goal's dead manager is still resumed comes from rule 1 (\"a crashed or lapsed manager or worker is restarted automatically\"). Cite rule 1 (or rules 1 and 2) here so the reference resolves to the rule that licenses the resume."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: docs/session-tiers.md intro lists agents/goal-drive.md, commands/topic-loop.md and scripts/topic-spawn.py as pointing at § Keep-alive, but none is in the diff",
      "disposition": "addressed",
      "detail": "Confirmed overclaim: none of the three files references § Keep-alive. goal-drive.md has no keep-alive/session-tiers text, topic-loop.md points only at § The four tiers, topic-spawn.py has only a generic code comment. Reported as a major finding on docs/session-tiers.md:144."
    },
    {
      "concern": "correctness: agents/topic-drive.md adds `restart` as a keep-alive arm — confirm it is a real topic-keepalive.py verdict",
      "disposition": "not-an-issue",
      "detail": "scripts/topic-keepalive.py emits `restart` (docstring line 31; main() line 332) for a live session with a lapsed loop, so naming `restart` alongside `resume` as a keep-alive arm is correct."
    },
    {
      "concern": "tests: scripts/budget-baseline.json lowers commands/open.md to 79227 — verify exact byte size",
      "disposition": "not-an-issue",
      "detail": "`wc -c commands/open.md` returns exactly 79227, matching the new budget baseline."
    },
    {
      "concern": "correctness: Owner definition (session LIVE + loop OK/STALE) does not match worker-keepalive.py goal_manager_live()",
      "disposition": "not-an-issue",
      "detail": "goal_manager_live() (scripts/worker-keepalive.py:130) checks only the session verdict LIVE, not the loop; the Owner section's ⚠️ admits exactly this divergence, so the table/owner text is honest about the gap rather than misrepresenting current behavior."
    },
    {
      "concern": "correctness: chain table cites fleet-keeper.py, close-worker.py, manager-liveness.py, session-liveness.py and § The four tiers rules 1-4 — verify existence and numbering",
      "disposition": "addressed",
      "detail": "All four scripts exist. Rule 1 (restart automatically) and rule 4 (never open a manager fresh) citations match § The four tiers. One imprecision found: the second invariant cites rule 2 for the resume claim, which is rule 1's ground — reported as a nit on docs/session-tiers.md:197."
    },
    {
      "concern": "correctness: commands/steps/keepalive.md drops the explicit rule 1 citation for automatic worker resume",
      "disposition": "not-an-issue",
      "detail": "The justification remains reachable through § Keep-alive (\"Restart is not open\", rules 1 and 4), and the replacement sentence reads correctly."
    },
    {
      "concern": "correctness: CHANGELOG entry nests multi-line bullets; verify it renders and implies no behavior change",
      "disposition": "not-an-issue",
      "detail": "The nested bullets render as a nested list; the top-level `- docs:` line still matches the changelog prefix regex, and \"No behaviour changes\" is accurate — the scripts changed only docstrings and budget-baseline.json tracks file size."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:00:56Z outcome: success review_id: 5818996165
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All seven plan concerns are dispositioned in the review — one reported as a major comment, six confirmed non-issue with evidence — and both comments cite docs/session-tiers.md, a file present in the diff. The approve verdict carries no blocking comments, so all three checks pass.",
  "concerns_addressed": [
    {"concern": "correctness: docs/session-tiers.md intro lists agents/goal-drive.md, commands/topic-loop.md and scripts/topic-spawn.py as pointing at § Keep-alive, but none is in the diff", "status": "addressed"},
    {"concern": "correctness: agents/topic-drive.md adds `restart` as a keep-alive arm — confirm it is a real topic-keepalive.py verdict", "status": "addressed"},
    {"concern": "tests: scripts/budget-baseline.json lowers commands/open.md to 79227 — verify exact byte size", "status": "addressed"},
    {"concern": "correctness: Owner definition (session LIVE + loop OK/STALE) does not match worker-keepalive.py goal_manager_live()", "status": "addressed"},
    {"concern": "correctness: chain table cites fleet-keeper.py, close-worker.py, manager-liveness.py, session-liveness.py and § The four tiers rules 1-4 — verify existence and numbering", "status": "addressed"},
    {"concern": "correctness: commands/steps/keepalive.md drops the explicit rule 1 citation for automatic worker resume", "status": "addressed"},
    {"concern": "correctness: CHANGELOG entry nests multi-line bullets; verify it renders and implies no behavior change", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
## Resolution

```json
{
  "verdict": "merged",
  "pr_state": "MERGED",
  "merged_at": "2026-11-08T18:01:37Z",
  "head_ref_oid": "1a95520145013b9adbc92c63b1af0f7c36cab8a1"
}
```
