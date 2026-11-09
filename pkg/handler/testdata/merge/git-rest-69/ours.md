---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/git-rest.git
current_job: ""
job_started_at: "2026-11-08T17:53:31Z"
phase: done
queued_at: "2026-11-08T17:41:20Z"
ref: 39289136bdf1ccc7b0b0813822addc9b88fa8cc9
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 709a4785-8c85-500f-943f-54cfef73a71b
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 1
trigger_scope: execution:39289136
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/git-rest/pull/69

**Repo:** [bborbe/git-rest](https://github.com/bborbe/git-rest)
## Plan


```json
{
  "pr_url": "https://github.com/bborbe/git-rest/pull/69",
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
      "note": "New ::error:: text asserts the bypass set is 'actors holding the Admin repository role and the Integration 3907226'. That set is enforced by GitHub ruleset bypass actors, not by this workflow, so it cannot be verified from the repo. Confirm it matches current branch protection and that the hardcoded integration ID 3907226 is still the correct app ID; a stale claim would mislead reviewers."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The message discloses the gate's bypass configuration (Admin role plus a specific integration ID) in a user-facing error annotation. Confirm this disclosure is intentional and that the ID is not sensitive; consider whether the claim belongs in repo docs rather than a runtime message that can drift."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "Adds a new '## Unreleased' section above '## v0.41.0'. Verify placement and format match the repo's changelog conventions and that the bullet wording is consistent with existing entries."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "PR description states the run logic and required check name are unchanged — the diff confirms only the echo string changed. Verify the final message stays on one line and does not break the ::error:: annotation parsing or line-length expectations."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Docs-only change restating the do-not-merge gate's bypass contract in its error annotation, plus a matching `## Unreleased` changelog bullet; the run logic, jq predicate, and required check name are unchanged. Selector mode: 7 candidates, 3 applicable, 4 skipped (no rule violations found); precommit skipped (selector mode) — CI covers lint+test.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have (Optional) — The annotation now hardcodes the ruleset bypass set (\"Admin repository role and the Integration 3907226\"). This set is enforced by GitHub branch-protection/ruleset config, not by this workflow, so the message cannot be validated from the repo and will silently drift if the ruleset or app ID changes; the wording \"the Integration 3907226\" also reads awkwardly. Consider keeping the authoritative bypass list in repo docs and either dropping the specifics from the runtime annotation or deriving them from a single source."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: New ::error:: text asserts the bypass set is 'actors holding the Admin repository role and the Integration 3907226'. That set is enforced by GitHub ruleset bypass actors, not by this workflow, so it cannot be verified from the repo. Confirm it matches current branch protection and that the hardcoded integration ID 3907226 is still the correct app ID; a stale claim would mislead reviewers.",
      "disposition": "not-an-issue",
      "detail": "Examined .github/workflows/check-do-not-merge.yml:19 and the full diff. The bypass set lives in GitHub ruleset settings, which are not stored in this repo, so it cannot be confirmed from a repo-only review; the change is a message string only and introduces no code defect. Raised as a non-blocking nit recommending the claim be validated against the live ruleset and kept in one authoritative place."
    },
    {
      "concern": "security: The message discloses the gate's bypass configuration (Admin role plus a specific integration ID) in a user-facing error annotation. Confirm this disclosure is intentional and that the ID is not sensitive; consider whether the claim belongs in repo docs rather than a runtime message that can drift.",
      "disposition": "not-an-issue",
      "detail": "Examined the changed line. GitHub App/integration IDs are public identifiers, not credentials, and \"Admin role can bypass\" is standard GitHub ruleset semantics; the workflow file is already public in this public repo. No secret or credential is disclosed, so there is no security defect. The drift/placement suggestion is covered by the non-blocking nit."
    },
    {
      "concern": "correctness: Adds a new '## Unreleased' section above '## v0.41.0'. Verify placement and format match the repo's changelog conventions and that the bullet wording is consistent with existing entries.",
      "disposition": "not-an-issue",
      "detail": "Verified CHANGELOG.md:11-17: `## Unreleased` sits after the frozen SemVer preamble and before `## v0.41.0`, and the bullet `- chore: restate the do-not-merge contract in the gate workflow` matches the repo's `- <prefix>: <lowercase description>` style and satisfies rule changelog/conventional-prefix-required (recognised `chore:` prefix)."
    },
    {
      "concern": "correctness: PR description states the run logic and required check name are unchanged — the diff confirms only the echo string changed. Verify the final message stays on one line and does not break the ::error:: annotation parsing or line-length expectations.",
      "disposition": "not-an-issue",
      "detail": "Verified the diff touches only the echo string at line 19; the jq predicate, `exit 1`, job/step names, and workflow `name:` are unchanged. The annotation remains a single physical line with no newline, no `%`, and no `\\r` needing GitHub workflow-command escaping, so `::error::` parsing is intact; message length is well under any annotation limit."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T17:55:47Z outcome: success review_id: 5818980414
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns are explicitly dispositioned in concerns_addressed, the sole comment cites .github/workflows/check-do-not-merge.yml line 19, which is present in the diff's changed files and inside the changed hunk, and an approve verdict with no blocking comments is internally consistent.",
  "concerns_addressed": [
    {"concern": "correctness: ::error:: bypass-set claim (Admin role + Integration 3907226) unverifiable from repo; confirm against live ruleset", "status": "addressed"},
    {"concern": "security: error message discloses bypass configuration (Admin role + integration ID)", "status": "addressed"},
    {"concern": "correctness: new '## Unreleased' section placement/format in CHANGELOG.md", "status": "addressed"},
    {"concern": "correctness: only echo string changed; message stays one line and ::error:: parsing intact", "status": "addressed"}
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
  "merged_at": "2026-11-08T17:57:33Z",
  "head_ref_oid": "39289136bdf1ccc7b0b0813822addc9b88fa8cc9"
}
```
