---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/teamvault-docker.git
current_job: ""
job_started_at: "2026-11-08T17:50:39Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:57:19Z"
ref: aafbbd4f0b7a7fee72c75eb09f4c60dd6c68e27d
stage: prod
status: completed
target_vault: openclaw
task_identifier: f4428632-43e2-5af0-948b-edbeede699fe
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 0
trigger_scope: planning:aafbbd4f
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/teamvault-docker/pull/12

**Repo:** [bborbe/teamvault-docker](https://github.com/bborbe/teamvault-docker)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/teamvault-docker/pull/12",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "master",
  "head_branch": "chore/restate-do-not-merge-contract",
  "files_changed": [".github/workflows/check-do-not-merge.yml"],
  "scope": "docs",
  "focus_areas": ["correctness", "security"],
  "concerns": [
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Single-line change to an ::error:: message string; verify the YAML block scalar/quoting still parses and that the em dash and apostrophe-free text do not break the echo or the workflow syntax"
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "PR body claims text-only change with unchanged run: logic and required check name; verify the diff contains no change to the jq predicate, exit code, or job/check name, since this gate is the do-not-merge enforcement point"
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "New message asserts the bypass set is Admin repository role plus Integration 3907226; check whether that claim matches the actual GitHub ruleset/branch protection config, since an inaccurate security message could mislead responders"
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "Single-line change to the ::error:: annotation text in .github/workflows/check-do-not-merge.yml (1 insertion, 1 deletion). Verified the change is message-only: the jq predicate (line 18), exit 1 (line 20), job id do-not-merge (line 7), workflow name (line 1) and trigger types (line 4) are untouched, so the do-not-merge enforcement point behaves identically. YAML/shell safety verified: the string sits in a literal block scalar with consistent 12-space indentation, no tabs or trailing whitespace, and contains no double quote, $, backtick, backslash or % that could break the echo or the workflow-command parser; the em dash is valid UTF-8 and already used unquoted elsewhere in the same file (lines 17, 22). Step 4 skipped: no rule-relevant files changed (the only changed path is a .github/workflows/*.yml, matching none of the rule-relevant extensions/globs), consistent with the pre-run mechanical funnel reporting 0 findings. precommit skipped (selector mode) — CI covers lint+test. LICENSE present. No Must Fix or Should Fix findings; one non-blocking maintainability nit.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have (Optional): the annotation now embeds the ruleset's bypass-actor list (\"Admin repository role\" plus \"Integration 3907226\") as a static string, but that list lives in GitHub's ruleset configuration outside this repo and is not represented anywhere in-tree — grep for 3907226/ruleset/bypass returns only this line. The phrasing does match GitHub's bypass-actor vocabulary exactly, so it reads as copied from the real config, but nothing here will detect it going stale if the ruleset's bypass actors change. Consider a comment in the workflow naming where the authoritative config lives, so the next person knows what to re-check rather than trusting a message that can silently drift."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "correctness: single-line change to an ::error:: message string; verify the YAML block scalar/quoting still parses and that the em dash and apostrophe-free text do not break the echo or the workflow syntax",
      "disposition": "not-an-issue",
      "detail": "Verified at .github/workflows/check-do-not-merge.yml:13-22 — run: | is a literal block scalar and line 19 carries the same 12-space indentation as its siblings; no tabs or trailing whitespace (regex \\t| +$ returns no matches). The only non-ASCII character in the file is U+2014 em dash, already present unquoted on pre-existing lines 17 and 22. The echoed string contains no double quote, $, backtick, backslash or % — so no shell expansion, quote termination, or GitHub workflow-command percent-encoding hazard. CI (make precommit + workflow load) is the runtime verifier."
    },
    {
      "concern": "security: PR body claims text-only change with unchanged run: logic and required check name; verify the diff contains no change to the jq predicate, exit code, or job/check name, since this gate is the do-not-merge enforcement point",
      "disposition": "not-an-issue",
      "detail": "git diff --stat origin/master...HEAD is exactly 1 file, 1 insertion, 1 deletion, and the hunk touches only line 19 (the echo string). Confirmed unchanged in the file at HEAD: jq predicate any(.[]; .name == \"do-not-merge\") at line 18, exit 1 at line 20, job id do-not-merge at line 7, workflow name check-do-not-merge at line 1, and the trigger types at line 4. No other file in the repo greps for the old error string, so nothing downstream keys off it. The required-check identity is intact."
    },
    {
      "concern": "correctness: new message asserts the bypass set is Admin repository role plus Integration 3907226; check whether that claim matches the actual GitHub ruleset/branch protection config, since an inaccurate security message could mislead responders",
      "disposition": "not-an-issue",
      "detail": "Examined: no ruleset or branch-protection configuration exists in-tree (find .github -type f returns only ci.yml and check-do-not-merge.yml; grep for ruleset|bypass|Admin repository role|3907226 matches only the new line itself), and the GitHub API is unavailable this run (rate limit exhausted), so the claim cannot be confirmed or refuted from here. The wording does mirror GitHub's bypass-actor vocabulary verbatim ('Admin repository role' = repository-role bypass actor, 'Integration <id>' = GitHub App bypass actor), which is strong evidence it was copied from the live ruleset rather than invented. Because it is unverifiable in-repo it is not reportable as a defect; the real, verified residual risk is staleness of an out-of-tree fact embedded in a static string, which I surfaced as a non-blocking nit at line 19 rather than a blocking finding."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:17:59Z outcome: success review_id: 5819050442
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All three plan concerns are explicitly addressed (two verified as non-issues with concrete evidence, one transparently confirmed unverifiable in-repo and surfaced as a non-blocking nit rather than dropped), the single comment cites a file and line present in the diff hunk, and the approve verdict is consistent with a blocking:false comment.",
  "concerns_addressed": [
    {"concern": "correctness: YAML block scalar/quoting still parses; em dash and text do not break echo or workflow syntax", "status": "addressed"},
    {"concern": "security: verify no change to jq predicate, exit code, or job/check name at the do-not-merge enforcement point", "status": "addressed"},
    {"concern": "correctness: verify the new message's bypass-set claim (Admin repository role + Integration 3907226) against the actual ruleset/branch-protection config", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
