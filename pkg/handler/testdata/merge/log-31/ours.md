---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/log.git
current_job: ""
job_started_at: "2026-11-08T17:48:00Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:44:13Z"
ref: 4832a31d99f6d98bfda5ada4add5bda7c8bbc070
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 2ea42960-8b27-503d-a7ec-0cb3970d08d6
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 1
trigger_scope: planning:4832a31d
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/log/pull/31

**Repo:** [bborbe/log](https://github.com/bborbe/log)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/log/pull/31",
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
    "security"
  ],
  "concerns": [
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "New error text hardcodes the bypass set (Admin repository role and Integration 3907226); if the branch-protection ruleset bypass list changes, this message silently goes stale and misleads reviewers."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The workflow only exits 1 on the do-not-merge label; who may merge despite that is governed by branch protection, not this job. Verify the wording does not imply the gate itself exempts those actors."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "A specific internal integration identifier (3907226) is now emitted in a public repo's CI error output; confirm this ID is safe to disclose publicly and does not aid bypass attempts."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Reviewed the two-file diff (.github/workflows/check-do-not-merge.yml, CHANGELOG.md) against master at 4832a31. Mechanical funnel: 79 YAMLs, 0 findings. Judgment tier: selector mode ran — 10 candidate rules, 7 applicable, 0 violations; commits are imperative-mood, under 50 chars, on a feature branch with no tag, and the new `## Unreleased` bullet uses an allowed `chore:` prefix. One non-blocking nit: the new error text hardcodes the branch-protection bypass set, which this file does not own and which will drift. The integration identifier 3907226 is a non-secret GitHub identifier and does not aid bypass. No blocking defects found; approve.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "The error text now enumerates the branch-protection bypass set by name (Admin repository role, Integration 3907226). This workflow does not own that list — it lives in the repo's master-protection ruleset, and nothing in this repo records it (the sibling check-unfinished-pipeline.yml comment confirms a ruleset exists but names no bypass actors). The job itself exits 1 for every actor, so the exemption is a ruleset property, not a property of this gate; phrasing it as part of the label block reads as though the job carves those actors out. When the ruleset's bypass list changes, this line silently becomes wrong with no test or check to catch it. Consider referring to the ruleset rather than enumerating its contents — e.g. 'Merge blocked by the do-not-merge label — blocks all actors except those on the master-protection ruleset's bypass list.' (Nice to Have / Optional)"
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: New error text hardcodes the bypass set (Admin repository role and Integration 3907226); if the branch-protection ruleset bypass list changes, this message silently goes stale and misleads reviewers.",
      "disposition": "addressed",
      "detail": "Confirmed against .github/workflows/check-do-not-merge.yml:19 — the bypass set is hardcoded in the message and no in-repo source of truth exists (grep for '3907226'/'bypass' finds only this line). Raised as a non-blocking nit comment suggesting the message reference the ruleset instead of enumerating it."
    },
    {
      "concern": "correctness: The workflow only exits 1 on the do-not-merge label; who may merge despite that is governed by branch protection, not this job. Verify the wording does not imply the gate itself exempts those actors.",
      "disposition": "addressed",
      "detail": "The job's run block (lines 18-21) exits 1 unconditionally when the label is present — it applies no actor check. The new wording 'blocks everyone except actors holding the Admin repository role and the Integration 3907226' attaches a ruleset-governed exemption to the gate's own message, which is the wording ambiguity flagged. Folded into the same nit comment."
    },
    {
      "concern": "security: A specific internal integration identifier (3907226) is now emitted in a public repo's CI error output; confirm this ID is safe to disclose publicly and does not aid bypass attempts.",
      "disposition": "not-an-issue",
      "detail": "GitHub integration (App) IDs are non-secret identifiers that appear in public API responses and web URLs; they are not credentials and cannot be used to obtain bypass. The bypass capability is granted by the master-protection ruleset to the installed integration, not by knowledge of its numeric ID, so publishing it does not widen the attack surface."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:10:15Z outcome: success review_id: 5819025884
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All three plan concerns are addressed (two via the non-blocking nit comment, one dispositioned not-an-issue), the sole comment cites .github/workflows/check-do-not-merge.yml which is in the diff and pins line 19 — the added echo line inside the changed hunk — and approve is consistent with a comment set containing no blocking: true.",
  "concerns_addressed": [
    {"concern": "correctness: New error text hardcodes the bypass set (Admin repository role and Integration 3907226); if the branch-protection ruleset bypass list changes, this message silently goes stale and misleads reviewers.", "status": "addressed"},
    {"concern": "correctness: The workflow only exits 1 on the do-not-merge label; who may merge despite that is governed by branch protection, not this job. Verify the wording does not imply the gate itself exempts those actors.", "status": "addressed"},
    {"concern": "security: A specific internal integration identifier (3907226) is now emitted in a public repo's CI error output; confirm this ID is safe to disclose publicly and does not aid bypass attempts.", "status": "addressed"}
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
  "merged_at": "2026-11-08T18:11:50Z",
  "head_ref_oid": "4832a31d99f6d98bfda5ada4add5bda7c8bbc070"
}
```
