---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/driftline.git
current_job: ""
job_started_at: "2026-11-08T17:45:42Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:40:29Z"
ref: 7752eb8e82dd06dc6d8751b4a386688c30906200
stage: prod
status: completed
target_vault: openclaw
task_identifier: 12975457-f889-5bd8-af27-9f8935e36d56
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 0
trigger_scope: planning:7752eb8e
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/driftline/pull/177

**Repo:** [bborbe/driftline](https://github.com/bborbe/driftline)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/driftline/pull/177",
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
      "note": "The new error text asserts an exact bypass set (Admin repository role plus Integration 3907226). Verify it matches the repository's actual ruleset bypass actors and that describing required-check bypass as a property of the label gate is accurate; a wrong message misinforms reviewers."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Confirm the run logic and the required check name are truly untouched by this diff, and that the message is emitted only on the blocked path, not on the success path."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The message publicly enumerates who can bypass the gate, including a hardcoded integration ID. Check this disclosure is intended for a public workflow file and that the ID is not stale; also check for workflow-command escaping issues in the ::error:: annotation."
    },
    {
      "area": "tests",
      "file": "CHANGELOG.md",
      "note": "No tests appear to cover the gate message. Check whether any test, script, or other workflow greps for the previous message string; if so it will break. Also confirm the Unreleased entry follows the repo changelog conventions."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Two-file change (a workflow error-message string plus an Unreleased CHANGELOG bullet) with no behavioral, security, or test impact: the message restates the contract already documented at AGENTS.md:112, the gate's run logic/check name/success path are untouched, and nothing in the repo greps the old string. Judgment tier: selector ran (0 findings; deterministic funnel 0 findings); precommit skipped (selector mode) — CI covers lint+test; Intent: unavailable — gh invocation was denied in this sandbox; Reviewed commit: 7752eb8e82dd06dc6d8751b4a386688c30906200; 0 files excluded by .reviewignore.",
  "comments": [],
  "concerns_addressed": [
    {
      "concern": "correctness: the new error text asserts an exact bypass set (Admin repository role plus Integration 3907226); verify it matches the repository's actual ruleset bypass actors and that describing required-check bypass as a property of the label gate is accurate",
      "disposition": "not-an-issue",
      "detail": "The message matches AGENTS.md:112 in substance and framing ('blocks everyone except actors holding the Admin repository role and the Integration 3907226, both bypass_mode: always in the master-protection ruleset'), which the immediately preceding commit (03cb404/19aa905) added as the repo's stated contract. The workflow file restates it, so a wrong-message risk would have to originate in AGENTS.md, not here. The live ruleset itself could not be queried — `gh` invocations were denied in this sandbox and the task reports GitHub rate-limit exhaustion — so the committed contract is the only available source of truth; the phrasing matches it."
    },
    {
      "concern": "correctness: confirm the run logic and the required check name are truly untouched by this diff, and that the message is emitted only on the blocked path, not on the success path",
      "disposition": "not-an-issue",
      "detail": "Diff is 1 insertion / 1 deletion in .github/workflows/check-do-not-merge.yml and touches only line 20, the `echo \"::error::...\"` inside the `if ... jq -e 'any(.[]; .name == \"do-not-merge\")'` branch, immediately before `exit 1`. Workflow name (`check-do-not-merge`), job id (`do-not-merge`), trigger list, the jq predicate, and the success-path echo on line 23 are all byte-identical. The message is unreachable on the success path."
    },
    {
      "concern": "security: the message publicly enumerates who can bypass the gate, including a hardcoded integration ID — check the disclosure is intended for a public workflow file, the ID is not stale, and there is no workflow-command escaping issue in the ::error:: annotation",
      "disposition": "not-an-issue",
      "detail": "No new disclosure: the same Admin-role/Integration-3907226 exemption is already published in AGENTS.md:112 in this public repo, added one PR earlier. A GitHub App integration ID is a public identifier, not a credential. The annotation body contains no `%`, CR, LF, or `::` sequence, so no workflow-command escaping (`%25`/`%0D`/`%0A`) is required; the em dash is plain UTF-8. Staleness could not be checked against the GitHub API (gh denied / rate-limited), but the ID agrees with the repo's own current documentation."
    },
    {
      "concern": "tests: no tests appear to cover the gate message — check whether any test, script, or other workflow greps the previous message string, and confirm the Unreleased entry follows the repo changelog conventions",
      "disposition": "not-an-issue",
      "detail": "Repo-wide search for `Merge blocked by the do-not-merge label`, `check-do-not-merge`, and `do-not-merge` returns only .github/workflows/check-do-not-merge.yml, AGENTS.md:112, and .maintainer.yaml:11 — no test, script, or second workflow asserts on the string, so nothing breaks. The CHANGELOG entry sits under a new `## Unreleased` heading placed after the frozen SemVer preamble, and its bullet uses the allowed `chore:` prefix (rule `changelog/conventional-prefix-required`); AGENTS.md requires a bullet for every PR. CI (ci.yml) runs npm test/build only, neither of which exercises this YAML."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:11:26Z outcome: success review_id: 5819029606
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns are addressed with reasoned not-an-issue dispositions, the review has zero comments (so there are no file citations that could be hallucinations), and the approve verdict is consistent with the absence of any blocking comment. The review's diff-specific claims check out against the provided diff: the workflow change is a single-line replacement at line 20 inside the blocked branch, the success-path echo and check name are untouched, and the CHANGELOG adds a new `## Unreleased` bullet with an allowed `chore:` prefix.",
  "concerns_addressed": [
    {
      "concern": "correctness: error text's bypass set (Admin role + Integration 3907226) matches the actual ruleset and the label-gate framing is accurate",
      "status": "addressed"
    },
    {
      "concern": "correctness: run logic and required check name untouched; message emitted only on the blocked path",
      "status": "addressed"
    },
    {
      "concern": "security: public disclosure of bypass actors/integration ID is intended, ID not stale, no workflow-command escaping issue",
      "status": "addressed"
    },
    {
      "concern": "tests: no test/script/workflow greps the old message string; Unreleased entry follows changelog conventions",
      "status": "addressed"
    }
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
