---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/kafka-topic-resend.git
current_job: ""
job_started_at: "2026-11-08T17:46:37Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:44:08Z"
ref: 23ce6c0324908a2719bfe4f5e129e1c01999c5ac
stage: prod
status: completed
target_vault: openclaw
task_identifier: 682893d2-0a69-58a5-a7a0-7e8a79fc9a90
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 0
trigger_scope: planning:23ce6c03
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/kafka-topic-resend/pull/19

**Repo:** [bborbe/kafka-topic-resend](https://github.com/bborbe/kafka-topic-resend)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/kafka-topic-resend/pull/19",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "master",
  "head_branch": "chore/restate-do-not-merge-contract",
  "files_changed": [
    ".github/workflows/check-do-not-merge.yml",
    "CHANGELOG.md"
  ],
  "scope": "docs",
  "focus_areas": ["correctness", "security", "tests"],
  "concerns": [
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Text-only change to the ::error:: message. Verify the stated bypass set (Admin repository role plus Integration 3907226) matches the actual branch-protection bypass configuration; the workflow itself does not enforce actor exemptions, so an inaccurate message misleads readers."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Merge-gate workflow. Confirm the jq label check, the exit 1, and the required check name are untouched and the gate still fails closed on the do-not-merge label."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "New Unreleased section added above v0.3.1; check entry format and placement match existing conventions."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No test or workflow-lint change accompanies the message edit; confirm no test asserts on the old error string."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Docs-only change to a merge-gate workflow: the `::error::` message now restates the do-not-merge contract (blocked for everyone except the Admin repository role and Integration 3907226), plus an `## Unreleased` CHANGELOG bullet. The gate logic itself — the jq label check, the `exit 1`, and the job/step names — is byte-identical, so the check still fails closed. The stated bypass set is external branch-protection configuration with no in-repo record either way (gh/API unavailable: rate limit exhausted), and nothing in the diff contradicts it. Judgment tier ran selector-mode: 9 candidates, 6 applicable, 0 violations; mechanical funnel ran clean (0 findings). precommit skipped (selector mode) — CI covers lint+test. No findings.",
  "comments": [],
  "concerns_addressed": [
    {
      "concern": "correctness: .github/workflows/check-do-not-merge.yml — verify the stated bypass set (Admin repository role plus Integration 3907226) matches the actual branch-protection bypass configuration; the workflow itself does not enforce actor exemptions, so an inaccurate message misleads readers.",
      "disposition": "not-an-issue",
      "detail": "Examined. The message describes the effective branch-protection outcome ('blocks everyone except …'), not a claim that the workflow checks actors; the workflow's unconditional `exit 1` is exactly consistent with a bypass-list-based contract. The bypass list lives in external repository settings — there is no in-repo record of it (checked .maintainer.yaml, README, all workflows) and `gh` could not be queried (rate limit 0/12500 remaining). Nothing in the diff contradicts the claim, and this PR is a restatement of it, not a change to enforcement."
    },
    {
      "concern": "security: .github/workflows/check-do-not-merge.yml — merge-gate workflow; confirm the jq label check, the exit 1, and the required check name are untouched and the gate still fails closed on the do-not-merge label.",
      "disposition": "not-an-issue",
      "detail": "Verified against the worktree: line 18 `jq -e 'any(.[]; .name == \"do-not-merge\")'`, line 20 `exit 1`, and the job/step identifiers (`check-do-not-merge`, `do-not-merge`, `Check for do-not-merge label`) are unchanged. The diff touches only the `echo \"::error::…\"` string literal. The gate still fails closed."
    },
    {
      "concern": "correctness: CHANGELOG.md — new Unreleased section added above v0.3.1; check entry format and placement match existing conventions.",
      "disposition": "not-an-issue",
      "detail": "Verified: `## Unreleased` sits after the frozen SemVer preamble and above `## v0.3.1` (the only Unreleased section — no duplicate), and the bullet `- chore: restate the do-not-merge contract in the gate workflow` uses the `chore:` conventional prefix the repo already uses. `changelog/conventional-prefix-required` and `changelog/preamble-frozen`/`unreleased-entry-required` all pass."
    },
    {
      "concern": "tests: .github/workflows/check-do-not-merge.yml — no test or workflow-lint change accompanies the message edit; confirm no test asserts on the old error string.",
      "disposition": "not-an-issue",
      "detail": "Grepped the whole tree for `Merge blocked by the do-not-merge`: the only hit is the workflow file itself (line 19). No Go test, shell script, or workflow asserts on the old error string, so the message edit breaks nothing."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:08:34Z outcome: success review_id: 5819020370
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns are addressed in concerns_addressed (all dispositioned not-an-issue with reasoning), there are no comments to hallucinate against, and the approve verdict with zero blocking comments is consistent.",
  "concerns_addressed": [
    {"concern": "correctness: workflow ::error:: message — verify stated bypass set (Admin role + Integration 3907226) matches branch-protection config", "status": "addressed"},
    {"concern": "security: workflow — confirm jq label check, exit 1, and required check name untouched; gate still fails closed", "status": "addressed"},
    {"concern": "correctness: CHANGELOG.md — new Unreleased section format and placement match conventions", "status": "addressed"},
    {"concern": "tests: workflow — confirm no test asserts on the old error string", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
