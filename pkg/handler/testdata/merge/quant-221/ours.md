---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/quant.git
current_job: ""
job_started_at: "2026-11-08T18:42:23Z"
phase: done
queued_at: "2026-11-08T17:45:01Z"
ref: fba93d6c79cb8abe0d23585071f998cdb023c873
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 9440f928-3e58-5b82-b767-8fb07148da06
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 1
trigger_scope: planning:fba93d6c
---
# PR Review: chore: restate the do-not-merge contract


https://github.com/bborbe/quant/pull/221

**Repo:** [bborbe/quant](https://github.com/bborbe/quant)
## Plan


```json
{
  "pr_url": "https://github.com/bborbe/quant/pull/221",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "master",
  "head_branch": "chore/restate-do-not-merge-contract",
  "files_changed": [".github/workflows/check-do-not-merge.yml"],
  "scope": "docs",
  "focus_areas": ["correctness", "security", "tests"],
  "concerns": [
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "New error text asserts the gate blocks everyone except actors holding the Admin repository role and Integration 3907226. Verify this matches the actual branch-protection bypass configuration, since the workflow itself cannot observe bypass actors."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "This is a required do-not-merge gate. Confirm the restated contract is accurate and not misleading about enforcement, and that the run: logic and required check name are truly unchanged."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Check the long em-dash message in the ::error:: workflow command is parsed intact by GitHub Actions and not truncated or mangled in the annotation."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No automated test appears to assert the emitted message; a wording/claim mismatch would go unnoticed. Check whether any workflow test covers the output string."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "One-line message-only change to the do-not-merge gate: the jq predicate, exit 1, job/check name, and trigger list are all unchanged, so enforcement is byte-identical. The new annotation text parses intact (em dash is valid UTF-8 and outside the workflow-command escape set), but it hardcodes an external branch-protection bypass contract the repo cannot observe — noted as a non-blocking nit. Step 4 early-exited: the only changed file (.github/workflows/check-do-not-merge.yml) is not rule-relevant; the pre-computed mechanical funnel also returned 0 findings across 79 YAMLs. precommit skipped (selector mode) — CI covers lint+test.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have: the restated message hardcodes the branch-protection bypass contract (`Admin` repository role + Integration 3907226). That configuration lives in GitHub branch protection, not in this repo, so this workflow can neither observe nor verify it — if the bypass list changes, the annotation silently becomes wrong. Consider a shorter message that does not embed the bypass detail, or keep it in sync with the protection rule. (Verified: run logic, `exit 1`, jq predicate, job name, and triggers are unchanged; the em dash is valid in a `::error::` message and does not need `%`/CR/LF escaping.)"
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: New error text asserts the gate blocks everyone except actors holding the Admin repository role and Integration 3907226. Verify this matches the actual branch-protection bypass configuration, since the workflow itself cannot observe bypass actors.",
      "disposition": "not-an-issue",
      "detail": "Examined check-do-not-merge.yml and the whole repo (.maintainer.yaml, .github/). No branch-protection/bypass-actor config is committed anywhere, and `gh api .../branches/master/protection` is not permitted in this sandbox, so the claim cannot be verified from the repo. It is a documentation restatement only — the failing check's behavior is unchanged — so there is no code defect; the drift risk is raised as a non-blocking nit on line 19."
    },
    {
      "concern": "security: This is a required do-not-merge gate. Confirm the restated contract is accurate and not misleading about enforcement, and that the run: logic and required check name are truly unchanged.",
      "disposition": "not-an-issue",
      "detail": "Diff confirms only the echo string on line 19 changed: `exit 1`, the jq `any(.[]; .name == \"do-not-merge\")` predicate, the `pull_request` trigger types, and the job id `do-not-merge` are all untouched, so the required check's name and pass/fail semantics are identical. The message is not consumed programmatically and the App ID is a public identifier, not a secret."
    },
    {
      "concern": "correctness: Check the long em-dash message in the ::error:: workflow command is parsed intact by GitHub Actions and not truncated or mangled in the annotation.",
      "disposition": "not-an-issue",
      "detail": "The message follows the second `::` as free text, contains no `::`, comma-in-property-section, `%`, CR, or LF that would need escaping, and the em dash (U+2014) is valid UTF-8. The ~120-char message is far below annotation length limits, so it renders intact."
    },
    {
      "concern": "tests: No automated test appears to assert the emitted message; a wording/claim mismatch would go unnoticed. Check whether any workflow test covers the output string.",
      "disposition": "not-an-issue",
      "detail": "Repo contains exactly one workflow (.github/workflows/check-do-not-merge.yml) and no test harness for workflow output; no test exists or is warranted for a message-only change, and asserting exact prose would be brittle. The gate's behavioral contract is unchanged, and CI yamllint is the relevant verifier."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T17:58:49Z outcome: success review_id: 5818989631
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns are explicitly dispositioned in concerns_addressed with supporting detail, the single comment cites .github/workflows/check-do-not-merge.yml line 19 which is inside the changed hunk of the provided diff, and the approve verdict is consistent with the only comment being blocking: false.",
  "concerns_addressed": [
    {
      "concern": "correctness: New error text asserts the gate blocks everyone except actors holding the Admin repository role and Integration 3907226. Verify this matches the actual branch-protection bypass configuration, since the workflow itself cannot observe bypass actors.",
      "status": "addressed"
    },
    {
      "concern": "security: This is a required do-not-merge gate. Confirm the restated contract is accurate and not misleading about enforcement, and that the run: logic and required check name are truly unchanged.",
      "status": "addressed"
    },
    {
      "concern": "correctness: Check the long em-dash message in the ::error:: workflow command is parsed intact by GitHub Actions and not truncated or mangled in the annotation.",
      "status": "addressed"
    },
    {
      "concern": "tests: No automated test appears to assert the emitted message; a wording/claim mismatch would go unnoticed. Check whether any workflow test covers the output string.",
      "status": "addressed"
    }
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
  "merged_at": "2026-11-08T17:59:26Z",
  "head_ref_oid": "fba93d6c79cb8abe0d23585071f998cdb023c873"
}
```
