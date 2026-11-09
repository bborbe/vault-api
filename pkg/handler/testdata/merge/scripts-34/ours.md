---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/scripts.git
current_job: ""
job_started_at: "2026-11-08T18:35:21Z"
phase: done
queued_at: "2026-11-08T17:45:07Z"
ref: ca7fcaf10fe9c9d908bc0a7f0bcf0ab208ef77ca
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 47c4eec7-dd51-51f5-ac5d-8d1d12252503
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 1
trigger_scope: planning:ca7fcaf1
---
# PR Review: chore: restate the do-not-merge contract


https://github.com/bborbe/scripts/pull/34

**Repo:** [bborbe/scripts](https://github.com/bborbe/scripts)
## Plan


```json
{
  "pr_url": "https://github.com/bborbe/scripts/pull/34",
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
      "note": "New ::error:: text hardcodes the bypass set (Admin repository role and Integration 3907226). Verify it matches the actual branch protection / ruleset bypass list; the workflow logic cannot read that config, so the message will silently go stale if the bypass list changes."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Message attributes the bypass to the gate itself, but bypass is enforced by branch protection/ruleset, not this workflow. Confirm wording does not imply the workflow enforces the exception."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Naming the exact bypass principals (Admin role and app/integration id 3907226) in a public repo documents precisely who can merge past the gate; confirm this disclosure is intended."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No verification that the stated contract matches configuration. Consider a pointer to the source of truth or a check that fails when the message and ruleset drift."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "Adds a new Unreleased section above v0.16.2; confirm it matches the repo release/changelog conventions and does not break the automated release tooling."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Two-file diff: a one-line change to the do-not-merge gate's error text plus a matching `## Unreleased` CHANGELOG entry. The change is valid YAML/bash, the changelog follows repo conventions, and no candidate judgment rule is violated; the only note is that the new message hardcodes the ruleset bypass principals, which will drift silently since the workflow cannot read that config.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "The `::error::` text hardcodes the branch-protection/ruleset bypass principals (Admin repository role + Integration 3907226). This job cannot read the ruleset config, so the message will silently go stale when the bypass list changes — and nothing in the repo records that list as the source of truth. Consider pointing at the ruleset (or a checked-in note) rather than restating the principals inline; also note the bypass is enforced by branch protection, not by this workflow itself, so the wording could imply the gate applies the exception."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: new ::error:: text hardcodes the bypass set (Admin repository role and Integration 3907226); workflow cannot read the config so the message will go stale",
      "disposition": "addressed",
      "detail": "Confirmed the hardcode at .github/workflows/check-do-not-merge.yml:19 and that no ruleset/source-of-truth config is checked into the repo; raised a non-blocking nit recommending a pointer to the ruleset instead of inline principals."
    },
    {
      "concern": "correctness: message may imply the gate itself enforces the bypass, but bypass is enforced by branch protection/ruleset",
      "disposition": "addressed",
      "detail": "Wording is 'Merge blocked by the do-not-merge label — blocks everyone except actors holding ...', which reads as effective behavior, but folded the enforcement-attribution caveat into the same nit comment."
    },
    {
      "concern": "security: naming exact bypass principals (Admin role and integration id 3907226) in a public repo documents who can merge past the gate",
      "disposition": "not-an-issue",
      "detail": "The Admin repository role is a standard GitHub role and integration id 3907226 is an app/installation identifier, not a credential or token; it grants no access on its own and the bypass list is admin-visible config. No exploitable disclosure."
    },
    {
      "concern": "tests: no verification that the stated contract matches configuration; consider a source-of-truth pointer or drift check",
      "disposition": "addressed",
      "detail": "Confirmed there is no checked-in source of truth for the bypass list; the nit comment recommends pointing at the ruleset (or adding a checked-in note) so drift is visible."
    },
    {
      "concern": "correctness: new Unreleased CHANGELOG section above v0.16.2 must match repo conventions and not break release tooling",
      "disposition": "not-an-issue",
      "detail": "CHANGELOG.md:8-10 places `## Unreleased` after the frozen preamble and before `## v0.16.2`, and the bullet uses the `chore:` conventional prefix, so changelog/conventional-prefix-required and the release bump parser are satisfied."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T17:58:56Z outcome: success review_id: 5818989971
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All five plan concerns are accounted for in the review's concerns_addressed (three addressed, two confirmed non-issues), the single comment cites .github/workflows/check-do-not-merge.yml line 19 which is exactly the changed line in the diff hunk, and an approve verdict with only a blocking:false nit is consistent.",
  "concerns_addressed": [
    {"concern": "correctness: new ::error:: text hardcodes the bypass set (Admin repository role and Integration 3907226); workflow cannot read the config so the message will go stale", "status": "addressed"},
    {"concern": "correctness: message attributes the bypass to the gate itself, but bypass is enforced by branch protection/ruleset, not this workflow", "status": "addressed"},
    {"concern": "security: naming exact bypass principals (Admin role and integration id 3907226) in a public repo documents who can merge past the gate", "status": "addressed"},
    {"concern": "tests: no verification that the stated contract matches configuration; consider a source-of-truth pointer or drift check", "status": "addressed"},
    {"concern": "correctness: new Unreleased CHANGELOG section above v0.16.2 must match repo conventions and not break release tooling", "status": "addressed"}
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
  "merged_at": "2026-11-08T17:59:32Z",
  "head_ref_oid": "ca7fcaf10fe9c9d908bc0a7f0bcf0ab208ef77ca"
}
```
