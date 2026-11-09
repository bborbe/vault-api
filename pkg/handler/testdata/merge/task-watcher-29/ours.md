---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/task-watcher.git
current_job: ""
job_started_at: "2026-11-08T17:49:53Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:57:14Z"
ref: 0fa943f5ec0b4e7d9d4ecd98a09e34c836e2e621
stage: prod
status: completed
target_vault: openclaw
task_identifier: 90874dfa-ec7b-55dd-97c3-4c2dd796f710
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 0
trigger_scope: planning:0fa943f5
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/task-watcher/pull/29

**Repo:** [bborbe/task-watcher](https://github.com/bborbe/task-watcher)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/task-watcher/pull/29",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "master",
  "head_branch": "chore/restate-do-not-merge-contract",
  "files_changed": [
    ".github/workflows/check-do-not-merge.yml",
    "CHANGELOG.md"
  ],
  "scope": "docs",
  "focus_areas": [
    "correctness",
    "security",
    "tests"
  ],
  "concerns": [
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The rewritten ::error:: message now asserts a precise bypass set (Admin repository role plus Integration 3907226). Verify this matches the actual branch-protection bypass configuration; the workflow cannot enforce or introspect it, so an inaccurate claim would mislead reviewers more than the previous generic wording."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "This is an access-control statement embedded in the merge gate. Confirm the described bypass actors are intentional and current, and that no additional bypass identities are omitted from the message."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Confirm the new string stays inside the existing double-quoted echo without breaking YAML quoting (em dash and parentheses are fine, but check no stray quotes or shell metacharacters were introduced) and that the run: logic and check name are truly unchanged."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The gate has no automated test coverage; the prior always-green label bug shows the failure path is untested. For this text-only change, at minimum confirm the failure branch still exits non-zero and the success branch is unaffected."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Text-only change to the do-not-merge gate workflow plus a matching CHANGELOG entry. The `run:` logic is untouched: the jq label parse, `exit 1` failure branch, and success branch are byte-identical apart from the `::error::` message string, which stays safely inside the existing double-quoted echo (no stray quotes, `$`, backticks, or shell metacharacters; the em dash is valid UTF-8 in both YAML and bash). Mechanical funnel was clean (0 findings across 79 rule YAMLs), 0 files excluded by .reviewignore. The only reviewer-worthy item is non-blocking: the new message asserts a precise branch-protection bypass set that this workflow cannot enforce or introspect, so it is an unverifiable in-repo claim that can silently drift. Precommit skipped (selector mode) — CI covers lint+test.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have (Optional) — the rewritten `::error::` text asserts a precise bypass contract (\"blocks everyone except actors holding the Admin repository role and the Integration 3907226\") that this workflow cannot enforce or introspect. If the master-protection ruleset's bypass actors ever change, the message silently becomes wrong and readers will trust a stale claim — there is no in-repo source of truth for it (grep for `3907226`/`bypass` finds nothing outside this line). The wording does mirror GitHub's ruleset API shape (`actor_type: Integration`, `actor_id`), so it looks read from the real config, but consider naming the source (or phrasing it as \"repository admins and the dark-factory GitHub App (Integration 3907226)\") so the claim is traceable and its drift is obvious. No functional impact: `exit 1`, the jq parse, and the success branch are unchanged, so the failure path still exits non-zero."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: bypass-set claim matches actual branch-protection configuration",
      "disposition": "addressed",
      "detail": "Cannot be verified from the repo (external ruleset config); no in-repo contradiction found, and the wording matches GitHub's ruleset bypass-actor representation. Raised as a non-blocking comment on check-do-not-merge.yml:19 asking the author to cite the source of truth so drift is detectable."
    },
    {
      "concern": "security: access-control statement is intentional, current, and complete",
      "disposition": "addressed",
      "detail": "The statement is documentary only — the workflow enforces the label gate and cannot read or widen bypass actors, so no access-control behavior changed. Folded into the same non-blocking comment requesting the bypass set be traceable; the Admin role plus Integration 3907226 set is plausible and not contradicted anywhere in the tree."
    },
    {
      "concern": "correctness: new string stays inside the double-quoted echo without breaking YAML/shell quoting; run logic and check name unchanged",
      "disposition": "not-an-issue",
      "detail": "Verified check-do-not-merge.yml:19 — no stray double quotes, `$`, backticks, `!`, or backslashes; em dash is valid UTF-8 inside a double-quoted bash string and inside the `run: |` block scalar. Job name `do-not-merge`, the `on:` triggers, the jq parse at line 18, `exit 1` at line 20, and the success echo at line 22 are all unchanged."
    },
    {
      "concern": "tests: gate has no automated coverage; confirm failure branch still exits non-zero and success branch unaffected",
      "disposition": "not-an-issue",
      "detail": "Verified by reading the file: the failure branch still `exit 1` and the success branch still falls through to `echo \"No do-not-merge label — merge allowed\"`. Only the message string changed, so neither branch's control flow is affected. The absence of workflow tests is pre-existing (this gate never had any) and is not introduced by this PR."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:18:53Z outcome: success review_id: 5819053139
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns are addressed or explicitly dispositioned in the review, the single comment cites .github/workflows/check-do-not-merge.yml (present in the diff) at line 19, which is exactly the added ::error:: echo line in the hunk, and the approve verdict is consistent with a sole non-blocking nit comment.",
  "concerns_addressed": [
    {"concern": "correctness: bypass-set claim matches actual branch-protection configuration", "status": "addressed"},
    {"concern": "security: access-control statement is intentional, current, and complete", "status": "addressed"},
    {"concern": "correctness: new string stays inside the double-quoted echo without breaking YAML/shell quoting; run logic and check name unchanged", "status": "addressed"},
    {"concern": "tests: gate has no automated coverage; confirm failure branch still exits non-zero and success branch unaffected", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
