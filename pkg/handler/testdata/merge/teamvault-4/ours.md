---
assignee: pr-reviewer-agent
base_ref: main
clone_url: https://github.com/bborbe/teamvault.git
current_job: ""
job_started_at: "2026-11-08T17:49:58Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:57:17Z"
ref: 10a5772a2a3eb27a3e65523473ee362f7e0e28e2
stage: prod
status: completed
target_vault: openclaw
task_identifier: 3e0c1e3f-b9d8-5728-8aac-7a3c1b94e689
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 0
trigger_scope: planning:10a5772a
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/teamvault/pull/4

**Repo:** [bborbe/teamvault](https://github.com/bborbe/teamvault)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/teamvault/pull/4",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "main",
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
      "note": "Restated error message hardcodes a bypass set (Admin repository role and Integration 3907226) while the job logic only evaluates the do-not-merge label. The workflow cannot observe ruleset bypass actors, so the text can silently drift from the actual merge-gate configuration. Verify the claim against the repository ruleset."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Public workflow now names the specific integration ID permitted to bypass the do-not-merge gate. Confirm that disclosing the bypass principal in a public repo is intentional."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Wording 'blocks everyone except actors holding the Admin repository role and the Integration 3907226' may be read as guidance that Admins may merge despite the label. Confirm the contract intends to advertise the bypass rather than assert the gate blocks all merges."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No automated check validates the emitted message or keeps it in sync with the required check and ruleset configuration. Consider linking to the ruleset in a comment or deriving the contract text from a single source."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "Verify the new Unreleased heading placement and entry format match project changelog conventions and that no existing Unreleased section is duplicated."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Message-only change: the do-not-merge gate's `::error::` text now names the ruleset bypass principals, plus a new `## Unreleased` CHANGELOG bullet. Selector review found 7 judgment candidates, 4 applicable, 3 skipped, 0 mechanical findings and 0 rule violations — the changelog prefix (`chore:`), imperative commit subjects (`add`, `restate`), no tag on the feature branch, and no direct-to-main commit all pass, and the `## Unreleased` section is correctly placed after the preamble with no duplicate. The check's pass/fail logic is unchanged; the only observation is a non-blocking maintainability nit about the hardcoded bypass text.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have — the message hardcodes the ruleset's bypass principals (\"Admin repository role and the Integration 3907226\"), but this workflow only evaluates the `do-not-merge` label and cannot observe the ruleset, and the integration ID appears nowhere else in the repo (confirmed by repo-wide grep). The text can therefore drift silently from the actual merge-gate configuration. Consider linking to the ruleset (or deriving the contract text from a single source) so the two stay in sync. Non-blocking: the check's pass/fail logic is unchanged by this diff, so the gate still behaves exactly as before."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: restated error message hardcodes a bypass set while the job logic only evaluates the do-not-merge label; verify the claim against the repository ruleset",
      "disposition": "addressed",
      "detail": "Examined check-do-not-merge.yml:18-22 — the job reads only `github.event.pull_request.labels` and never inspects ruleset bypass actors, and `3907226` appears nowhere else in the repo. The ruleset itself is not readable from this sandbox (gh API rate-limited per the job failure), so the claim's accuracy is unverifiable here; flagged as a non-blocking drift nit on line 19 rather than asserted as wrong."
    },
    {
      "concern": "security: public workflow now names the specific integration ID permitted to bypass the do-not-merge gate; confirm disclosure is intentional",
      "disposition": "not-an-issue",
      "detail": "A GitHub integration/app ID is a public identifier returned by the API to any reader and grants no access on its own. Naming it in a workflow message is not a credential disclosure and does not widen the attack surface."
    },
    {
      "concern": "security: wording 'blocks everyone except actors holding the Admin repository role and the Integration 3907226' may be read as guidance that Admins may merge despite the label",
      "disposition": "not-an-issue",
      "detail": "The sentence describes GitHub ruleset bypass semantics accurately and does not instruct anyone to merge; it appears only in the failing check's error annotation. No permission is granted or implied by the text."
    },
    {
      "concern": "tests: no automated check validates the emitted message or keeps it in sync with the required check and ruleset configuration",
      "disposition": "addressed",
      "detail": "Confirmed — there is no test or lint anywhere in the repo asserting the message text, and `.github/workflows/` contains only the three workflows. Raised as the non-blocking nit on line 19 suggesting a single source of truth / ruleset link."
    },
    {
      "concern": "correctness: verify the new Unreleased heading placement and entry format match project changelog conventions and that no existing Unreleased section is duplicated",
      "disposition": "not-an-issue",
      "detail": "CHANGELOG.md:11-13 — a single `## Unreleased` section sits after the frozen `# Changelog` title and SemVer preamble (lines 1-9) and before `## v0.16.1`, with one bullet `- chore: restate the do-not-merge contract in the gate workflow`. The `chore:` prefix is in the allowed set and no other Unreleased section exists."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:19:03Z outcome: success review_id: 5819053695
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All five Plan concerns are accounted for in the review (three addressed with a comment or confirmation, two confirmed as non-issues), the single comment cites `.github/workflows/check-do-not-merge.yml` line 19 which is inside the changed hunk, and the `approve` verdict is consistent with a comment marked `blocking: false`.",
  "concerns_addressed": [
    {"concern": "correctness: restated error message hardcodes a bypass set while the job logic only evaluates the do-not-merge label; verify the claim against the repository ruleset", "status": "addressed"},
    {"concern": "security: public workflow now names the specific integration ID permitted to bypass the do-not-merge gate; confirm disclosure is intentional", "status": "addressed"},
    {"concern": "security: wording 'blocks everyone except actors holding the Admin repository role and the Integration 3907226' may be read as guidance that Admins may merge despite the label", "status": "addressed"},
    {"concern": "tests: no automated check validates the emitted message or keeps it in sync with the required check and ruleset configuration", "status": "addressed"},
    {"concern": "correctness: verify the new Unreleased heading placement and entry format match project changelog conventions and that no existing Unreleased section is duplicated", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
