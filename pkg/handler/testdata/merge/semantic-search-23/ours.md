---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/semantic-search.git
current_job: ""
job_started_at: "2026-11-08T17:45:24Z"
phase: done
previous_assignee: pr-reviewer-agent
ref: 5771931f36031a0a01de299ba163e439bee34bf0
stage: prod
status: completed
target_vault: openclaw
task_identifier: e9dfb20b-09b2-5560-8d23-b3c9aa68f9a0
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 0
trigger_scope: planning:5771931f
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/semantic-search/pull/23

**Repo:** [bborbe/semantic-search](https://github.com/bborbe/semantic-search)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/semantic-search/pull/23",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "master",
  "head_branch": "chore/restate-do-not-merge-contract",
  "files_changed": [
    ".github/workflows/check-do-not-merge.yml",
    "CHANGELOG.md"
  ],
  "scope": "docs",
  "focus_areas": ["correctness", "docs", "tests"],
  "concerns": [
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Text-only change to the ::error:: message. Verify the stated bypass set (Admin repository role and Integration 3907226) matches the actual branch-protection configuration; an inaccurate contract message would mislead users about who can bypass the gate. Also check the phrase blocks everyone except is not confused with the step logic, which still exits 1 purely on the label."
    },
    {
      "area": "docs",
      "file": "CHANGELOG.md",
      "note": "Confirm the Unreleased entry accurately describes the change and follows the existing changelog conventions."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No test asserts the emitted message text; confirm whether the exact wording is covered by any workflow test or if this is acceptable for a text-only change."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Selector-mode review of a two-file diff (`.github/workflows/check-do-not-merge.yml` error-message text + a CHANGELOG `## Unreleased` bullet). Judgment tier: selector ran — 10 candidates, 1 applicable, 0 violations; deterministic tier: ran — 79 YAMLs, 0 findings; security tier: skipped — `--security` not requested; precommit skipped (selector mode) — CI covers lint+test; Intent: unavailable — no `--intent`/`--pr` supplied and the GitHub API is rate-limited (0/12500 remaining); 0 files excluded by `.reviewignore`; reviewed commit 5771931f36031a0a01de299ba163e439bee34bf0; Result: clean. No blocking or non-blocking defects: the workflow still exits 1 purely on label presence, and the bypass set named in the new message is external master-protection ruleset configuration that this sandbox cannot read — it is surfaced as a note rather than a finding, since no inaccuracy could be confirmed.",
  "comments": [],
  "concerns_addressed": [
    {
      "concern": "correctness: verify the stated bypass set (Admin repository role and Integration 3907226) matches the actual branch-protection configuration; check the phrase does not confuse the contract with the step logic, which still exits 1 purely on the label",
      "disposition": "not-an-issue",
      "detail": "Read the full workflow at HEAD: `.github/workflows/check-do-not-merge.yml:18-21` still branches solely on `any(.[]; .name == \"do-not-merge\")` and exits 1, with no actor/role check added — so the changed line is message text only and cannot alter gate behavior. The named bypass set is GitHub master-protection *ruleset* configuration, not repository content: grepping the tree finds `3907226` and the Admin-role wording only on the changed line, and the sibling `.github/workflows/check-unfinished-pipeline.yml:6-7` confirms the repo's required checks are wired into a 'master-protection ruleset', which is the GitHub construct that carries a bypass list of roles and integrations. The live ruleset could not be read from the sandbox (GitHub API rate-limited, 0/12500 remaining; `gh` not permitted here), so the claim's accuracy against the live config is unverified — reported as a note, not a finding, per the funnel-inject contract. The wording is internally consistent with GitHub's ruleset bypass model ('Repository admin' role + an integration/app ID)."
    },
    {
      "concern": "docs: confirm the Unreleased entry accurately describes the change and follows existing changelog conventions",
      "disposition": "not-an-issue",
      "detail": "`CHANGELOG.md:11-13` adds `## Unreleased` directly after the frozen SemVer preamble and above `## v0.23.2`, with a single flat bullet `- chore: restate the do-not-merge contract in the gate workflow`. That satisfies `docs/changelog-guide.md` RULE changelog/preamble-frozen (section after the preamble, newest first), RULE changelog/conventional-prefix-required (`chore:` is in the allowed prefix set), and RULE changelog/unreleased-entry-required (non-empty section). The description is accurate: the only source change is the gate workflow's `::error::` message. Prior `chore:` bullets exist in this file (lines 87, 94, 117, 139, 163), so the prefix is established convention. Note (not a defect): `.maintainer.yaml` sets `release.autoRelease: true`, so merging this will cut a patch release for a message-only change — that is the repo's designed flow and the author explicitly added the entry."
    },
    {
      "concern": "tests: no test asserts the emitted message text; confirm whether the exact wording is covered by any workflow test or if this is acceptable for a text-only change",
      "disposition": "not-an-issue",
      "detail": "Grepped `tests/` for `do-not-merge` and `workflow` — no file matches, and the repo has no workflow-test harness; CI (`.github/workflows/ci.yml`) runs `ruff check`, `mypy src`, and `pytest`, none of which exercise GitHub Actions annotation text. The change is a single `echo \"::error::...\"` string with no logic, control-flow, or escaping-sensitive characters (`%`, CR/LF) that a test could meaningfully pin, so the absence of a message-text assertion is acceptable and not a test gap."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:13:50Z outcome: success review_id: 5819037347
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All three plan concerns are explicitly addressed in concerns_addressed with concrete reasoning (workflow logic unchanged, changelog conventions checked, test coverage assessed); the review contains zero comments, so no hallucinated file citations exist; and an approve verdict with no blocking comments is consistent.",
  "concerns_addressed": [
    {"concern": "correctness: verify the stated bypass set (Admin repository role and Integration 3907226) matches the actual branch-protection configuration; check the phrase is not confused with the step logic, which still exits 1 purely on the label", "status": "addressed"},
    {"concern": "docs: confirm the Unreleased entry accurately describes the change and follows existing changelog conventions", "status": "addressed"},
    {"concern": "tests: no test asserts the emitted message text; confirm whether the exact wording is covered by any workflow test or if this is acceptable for a text-only change", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
