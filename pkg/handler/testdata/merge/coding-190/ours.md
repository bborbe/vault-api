---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/coding.git
current_job: ""
job_started_at: "2026-11-08T18:25:21Z"
phase: done
ref: b381baebbca5f80e819916e1df7127d1795c61d7
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 3d13723b-1456-5955-b7ce-176339f9ca49
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 1
trigger_scope: planning:b381baeb
---
# PR Review: chore: restate the do-not-merge contract


https://github.com/bborbe/coding/pull/190

**Repo:** [bborbe/coding](https://github.com/bborbe/coding)
## Plan


```json
{
  "pr_url": "https://github.com/bborbe/coding/pull/190",
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
      "note": "The reworded error asserts a specific bypass set (Admin repository role and Integration 3907226). Verify this matches the actual branch-protection bypass actors; the workflow itself cannot enforce or verify the claim, so stale text would mislead reviewers. Confirm the claim is about bypass of required checks rather than the job logic."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The message names an internal Integration ID in a check failure that is visible in PR logs to anyone with read access. Confirm that disclosing this identifier is acceptable and does not leak internal infrastructure detail."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "A new Unreleased section was inserted above v0.65.5. Check it does not duplicate or conflict with existing release tooling expectations (header format, bullet style) used by the repo's changelog/release automation."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No automated assertion covers the message text, so future drift between the stated bypass contract and the real branch-protection config will go unnoticed. Assess whether a lightweight check or documented source of truth is warranted."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "One-line reword of the do-not-merge gate's failure message plus an Unreleased changelog bullet. The job logic is unchanged and correct, the YAML change is confined to the `run:` block, and the changelog entry uses an allowed `chore:` prefix in the correct single `## Unreleased` section. One non-blocking nit: the new message restates a branch-protection bypass contract that has no in-repo source of truth, so it can drift silently. Selector traceability: 10 candidates from Step 4b-i, 4 applicable, 6 skipped; 0 mechanical findings; no rule violations found.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have (Optional): the new error text restates a specific bypass contract — 'actors holding the Admin repository role and the Integration 3907226' — that exists nowhere else in the repo (no ruleset/branch-protection doc, no comment naming its source). A reviewer cannot verify it from the worktree, and nothing fails when the branch-protection ruleset changes, so the sentence can silently go stale and mislead whoever reads the red check. Consider pointing at the source of truth (e.g. a comment naming the ruleset/branch-protection config) rather than restating the bypass set inline; a test asserting the message text would be brittle and is not warranted. Note also that this file is public, so the integration id is already disclosed in source and its appearance in PR logs adds no exposure — the issue here is verifiability, not secrecy."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: the reworded error asserts a specific bypass set (Admin repository role and Integration 3907226) that must match the actual branch-protection bypass actors, and the workflow cannot enforce or verify the claim, so stale text would mislead reviewers",
      "disposition": "addressed",
      "detail": "Examined the repo: the claim is non-functional message text, the job logic is unchanged (only the `echo` string differs), and there is no in-repo record of the bypass actors to cross-check against (grep for 3907226 hits only this line; no ruleset/branch-protection doc exists). GitHub branch-protection settings are not reachable from this sandbox (no gh auth/network), so the factual claim itself is unverified. Raised as a non-blocking nit on .github/workflows/check-do-not-merge.yml:19 recommending a source-of-truth pointer instead of an inline restatement."
    },
    {
      "concern": "security: the message names an internal Integration ID in a check failure visible in PR logs, potentially leaking internal infrastructure detail",
      "disposition": "not-an-issue",
      "detail": "Verified the message lives in .github/workflows/check-do-not-merge.yml, which is committed source in this public plugin repo, so the identifier is already publicly disclosed regardless of workflow logs. GitHub app/integration ids are public identifiers, not credentials, and carry no access by themselves. No new exposure is introduced by the runtime log line."
    },
    {
      "concern": "correctness: a new Unreleased section was inserted above v0.65.5; check it does not duplicate or conflict with existing changelog/release tooling expectations (header format, bullet style)",
      "disposition": "not-an-issue",
      "detail": "Verified: exactly one `## Unreleased` section (grep for `^## Unreleased` returns line 11 only), placed immediately after the frozen preamble and directly above `## v0.65.5`, matching docs/changelog-guide.md. The bullet `- chore: restate ...` uses `chore:`, one of the recognised conventional prefixes (patch bump), and `git diff --check` reports no whitespace errors. The changelog-unreleased-fold guard's precondition (a pending Unreleased bullet above a released heading in the merge result) is unaffected."
    },
    {
      "concern": "tests: no automated assertion covers the message text, so future drift between the stated bypass contract and the real branch-protection config will go unnoticed; assess whether a lightweight check or documented source of truth is warranted",
      "disposition": "addressed",
      "detail": "Assessed: an automated assertion on exact message text would be brittle and low-value, and no CI job can read GitHub branch-protection settings. The right remedy is a documented source of truth; that recommendation is folded into the non-blocking nit on .github/workflows/check-do-not-merge.yml:19 (name the ruleset/config the bypass set comes from, or drop the specific id)."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T17:59:51Z outcome: success review_id: 5818992774
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns are explicitly dispositioned in the review (two addressed with a comment, two confirmed non-issues), the single comment cites .github/workflows/check-do-not-merge.yml — a file present in the diff — at line 19, which is exactly the changed echo line in the hunk, and an approve verdict with only a non-blocking nit is internally consistent.",
  "concerns_addressed": [
    {"concern": "correctness: reworded error asserts a specific bypass set (Admin repository role and Integration 3907226) that the workflow cannot enforce or verify, so stale text would mislead reviewers", "status": "addressed"},
    {"concern": "security: message names an internal Integration ID in a check failure visible in PR logs, potentially leaking internal infrastructure detail", "status": "addressed"},
    {"concern": "correctness: new Unreleased section inserted above v0.65.5 may duplicate or conflict with changelog/release tooling expectations", "status": "addressed"},
    {"concern": "tests: no automated assertion covers the message text, so future drift between the stated bypass contract and the real branch-protection config goes unnoticed", "status": "addressed"}
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
  "merged_at": "2026-11-08T18:02:45Z",
  "head_ref_oid": "b381baebbca5f80e819916e1df7127d1795c61d7"
}
```
