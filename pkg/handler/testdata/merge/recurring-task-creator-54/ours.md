---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/recurring-task-creator.git
current_job: ""
job_started_at: "2026-11-08T18:21:23Z"
phase: done
queued_at: "2026-11-08T17:45:02Z"
ref: 94f0b51f96ca78196c2d17ecb06f39329b3911f7
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 2cde5eda-e898-539c-af14-89e4836681a4
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 1
trigger_scope: planning:94f0b51f
---
# PR Review: chore: restate the do-not-merge contract


https://github.com/bborbe/recurring-task-creator/pull/54

**Repo:** [bborbe/recurring-task-creator](https://github.com/bborbe/recurring-task-creator)
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/recurring-task-creator/pull/54",
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
      "note": "The new error text asserts the gate blocks everyone except Admin-role holders and Integration 3907226, but the visible run logic only exits 1 when the do-not-merge label is present; the bypass set actually lives in branch-protection configuration and is not enforced or asserted anywhere in this diff. Verify the claim is accurate before it becomes the documented contract."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Naming a specific bypass integration ID inside a publicly emitted ::error:: annotation documents the allowlist for a required merge gate. Confirm the repository visibility makes this disclosure intentional, and that it is the integration ID rather than anything credential-like."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "A policy statement is being embedded in a runtime error string with no check to detect drift; if the branch-protection bypass list changes, this message silently goes stale and misinforms the next person who hits the gate."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No test or validation asserts the label-present to exit-1 path or the emitted message text; the file's own comments reference a prior bug where the gate always reported green, so regression coverage for this check would be valuable."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Docs-only change: one error-message string in the do-not-merge gate workflow plus a CHANGELOG Unreleased entry. The workflow logic is unchanged (still exits 1 only when the do-not-merge label is present), the CHANGELOG section placement and `chore:` prefix are correct, and the mechanical funnel reported zero findings. The only substantive note is that the message now restates a branch-protection bypass policy that lives outside the repo and can drift — non-blocking.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "The error message now restates a merge-bypass policy (Admin repository role + Integration 3907226) that is configured in the repo's master-protection ruleset, not in this workflow. The workflow's own logic still just `exit 1`s whenever the label is present, so nothing here verifies or enforces that bypass set; if the ruleset's bypass actors change, this string silently goes stale and misinforms the next person who hits the gate. Consider adding a comment pointing at the ruleset as the source of truth, or softening the wording to \"may be bypassed by configured actors\" so the message cannot contradict the actual protection config."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: error text asserts gate blocks everyone except Admin-role holders and Integration 3907226, but the visible logic only exits 1 on the label and the bypass set lives in branch-protection config",
      "disposition": "not-an-issue",
      "detail": "Examined .github/workflows/check-do-not-merge.yml:18-22 and the sibling check-unfinished-pipeline.yml, whose header states the job names are wired into the repo's master-protection ruleset. The bypass set is indeed ruleset config, not workflow logic, so the message is describing merge-time bypass rather than check-run behavior. It is corroborated by .maintainer.yaml (autoMerge.trivial: true — the maintainer integration auto-merges trivial PRs). No evidence the claim is inaccurate; it cannot be machine-verified from the checkout because the ruleset is GitHub-side config."
    },
    {
      "concern": "security: naming a specific bypass integration ID in a publicly emitted ::error:: annotation documents the allowlist for a required merge gate",
      "disposition": "not-an-issue",
      "detail": "The value 3907226 is a GitHub App/integration ID, which is public metadata, not a credential — knowing it does not grant bypass, since acting as the integration requires its private key. The repo is public and Actions logs are public by design, so this is intentional documentation of the gate's bypass contract. No credential-like material is disclosed and the actual protection (required check + ruleset) is unchanged."
    },
    {
      "concern": "correctness: policy statement embedded in a runtime error string with no drift detection",
      "disposition": "addressed",
      "detail": "Reported as a non-blocking nit at .github/workflows/check-do-not-merge.yml:19 suggesting the message cite the ruleset as source of truth or soften the wording; no code change is required for this docs-only PR."
    },
    {
      "concern": "tests: no test/validation asserts the label-present to exit-1 path or the emitted message text",
      "disposition": "not-an-issue",
      "detail": "The diff changes only a message string; the label-detection logic (jq over toJson labels) is untouched and pre-existing. The repo has no GitHub-Actions test harness (no act/actionlint workflow-test setup), and the workflow itself runs on every PR, exercising the label-absent path. Demanding workflow-unit-test coverage for a one-line string edit is out of proportion and out of scope for this change."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T17:57:15Z outcome: success review_id: 5818984844
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns are accounted for in concerns_addressed (three reasoned not-an-issue dispositions and one non-blocking nit comment), the single comment cites .github/workflows/check-do-not-merge.yml line 19 which is the added line inside the diff hunk, and an approve verdict with only blocking:false comments is consistent.",
  "concerns_addressed": [
    {"concern": "correctness: error text asserts bypass set (Admin role + Integration 3907226) not enforced by visible logic", "status": "addressed"},
    {"concern": "security: naming bypass integration ID in a public ::error:: annotation", "status": "addressed"},
    {"concern": "correctness: policy statement in runtime error string with no drift detection", "status": "addressed"},
    {"concern": "tests: no coverage of label-present → exit-1 path or message text", "status": "addressed"}
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
  "merged_at": "2026-11-08T17:58:11Z",
  "head_ref_oid": "94f0b51f96ca78196c2d17ecb06f39329b3911f7"
}
```
