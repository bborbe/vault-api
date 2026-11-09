---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/sentry-proxy.git
current_job: ""
job_started_at: "2026-11-08T17:46:08Z"
phase: done
previous_assignee: pr-reviewer-agent
ref: ab7456f437f434916f237055e24207ea9cf7fc5d
stage: prod
status: completed
target_vault: openclaw
task_identifier: 3db77c23-2abd-5f1e-94df-d33198b997d9
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 0
trigger_scope: planning:ab7456f4
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/sentry-proxy/pull/26

**Repo:** [bborbe/sentry-proxy](https://github.com/bborbe/sentry-proxy)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/sentry-proxy/pull/26",
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
      "note": "The new error text asserts a bypass set (Admin repository role plus Integration 3907226) that the workflow logic does not implement — the jq label check is unchanged and any bypass comes from branch-protection settings. Verify the wording accurately describes the real enforcement point rather than implying the script performs the exemption."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Hardcoded integration ID 3907226 in the message will silently drift if the integration is rotated or the bypass policy changes; confirm it matches current repository/branch-protection configuration and consider whether naming a bypass actor in CI output leaks policy detail."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Message claims it blocks everyone except Admin role and the integration, but the workflow itself exits 1 for all actors when the label is present; the check name and run logic are stated as unchanged, so verify no behavioral change slipped in and that the required-check contract still holds."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No test or assertion ties the emitted message to the actual bypass contract, so future drift between text and policy will not be caught; consider whether a lightweight check is warranted."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "New Unreleased section adds a chore bullet; verify placement above v0.5.4 and that the entry matches the release-classification conventions used in this repo."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Text-only change: the do-not-merge gate's `::error::` message is restated and a `## Unreleased` CHANGELOG bullet is added. The workflow logic is unchanged (the jq label test and `exit 1` still fire for every actor), so there is no behavioral change and no blocking defect. The new message asserts a branch-protection bypass set (Admin repository role + Integration 3907226) that this workflow does not implement and that cannot be verified from the repo — noted as a non-blocking wording nit.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have (Optional) — The restated text names a bypass set (\"Admin repository role and Integration 3907226\") that this workflow does not implement: the jq label test (line 18) and `exit 1` (line 20) are unchanged and still fail for every actor; the exemption lives in branch-protection bypass settings, not in this file. The wording conflates the check's behavior with the merge-blocking policy, and the hardcoded integration ID will silently drift if the bypass policy or integration changes. Consider phrasing it as the merge contract (\"merge is blocked unless the actor has branch-protection bypass\") rather than implying the script grants the exemption."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: the new error text asserts a bypass set (Admin repository role plus Integration 3907226) that the workflow logic does not implement — the jq label check is unchanged and any bypass comes from branch-protection settings.",
      "disposition": "not-an-issue",
      "detail": "Verified against the worktree: the diff changes only the `echo` string at check-do-not-merge.yml:19. The jq label test (line 18) and `exit 1` (line 20) are byte-identical, so the workflow still fails for every actor when the label is present. The named bypass is enforced by branch protection, not by this script; the message describes the effective merge block rather than claiming the script performs the exemption. Surfaced as a non-blocking wording nit (integration-ID drift)."
    },
    {
      "concern": "security: hardcoded integration ID 3907226 in the message will silently drift if the integration is rotated or the bypass policy changes; confirm it matches current configuration and consider whether naming a bypass actor in CI output leaks policy detail.",
      "disposition": "not-an-issue",
      "detail": "An integration ID is a public numeric identifier, not a credential, so naming it in a CI annotation is not a credential leak. Drift risk is real but non-blocking for a static message and is covered by the wording nit; the value cannot be cross-checked from the repo (branch-protection config is external)."
    },
    {
      "concern": "correctness: message claims it blocks everyone except Admin role and the integration, but the workflow itself exits 1 for all actors when the label is present; verify no behavioral change slipped in and that the required-check contract still holds.",
      "disposition": "not-an-issue",
      "detail": "Confirmed no behavioral change: only the echo text changed; the jq parse, the condition, `exit 1`, and the job/check name are unchanged, so the required status check still fails whenever the label is applied."
    },
    {
      "concern": "tests: no test or assertion ties the emitted message to the actual bypass contract, so future drift between text and policy will not be caught.",
      "disposition": "not-an-issue",
      "detail": "The repo has no workflow-test infrastructure and this is a static message string; a dedicated assertion would add maintenance without covering runtime behavior. YAML validity and workflow execution are covered by CI. Non-blocking."
    },
    {
      "concern": "correctness: New Unreleased section adds a chore bullet; verify placement above v0.5.4 and that the entry matches the release-classification conventions used in this repo.",
      "disposition": "not-an-issue",
      "detail": "`## Unreleased` is placed after the frozen SemVer preamble and above `## v0.5.4`; the bullet starts with the allowed `chore:` prefix, matching the changelog/conventional-prefix-required rule and the repo's existing entries."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:13:44Z outcome: success review_id: 5819037004
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All five plan concerns are individually addressed in the review's concerns_addressed list, the single comment cites a file and line (check-do-not-merge.yml:19) that lies inside the diff hunk, and the approve verdict carries no blocking comment, so all three checks hold.",
  "concerns_addressed": [
    {"concern": "correctness: new error text asserts a bypass set the workflow does not implement", "status": "addressed"},
    {"concern": "security: hardcoded integration ID 3907226 drift / policy-detail leak", "status": "addressed"},
    {"concern": "correctness: message claims blocks everyone except Admin role and integration; verify no behavioral change", "status": "addressed"},
    {"concern": "tests: no test ties emitted message to the actual bypass contract", "status": "addressed"},
    {"concern": "correctness: CHANGELOG Unreleased placement and release-classification conventions", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
