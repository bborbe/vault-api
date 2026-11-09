---
assignee: pr-reviewer-agent
base_ref: master
clone_url: https://github.com/bborbe/git-ai-sync.git
current_job: ""
job_started_at: "2026-11-08T17:47:22Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:40:19Z"
ref: 70b7c69f266456a4c01dd73ffd010bf51c62d6fb
spawn_notification: true
stage: prod
status: completed
target_vault: openclaw
task_identifier: 2d5433fc-70ce-503e-bc02-976ee51ea4b7
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 2
trigger_scope: ai_review:70b7c69f
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/git-ai-sync/pull/23

**Repo:** [bborbe/git-ai-sync](https://github.com/bborbe/git-ai-sync)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan


```json
{
  "pr_url": "https://github.com/bborbe/git-ai-sync/pull/23",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "master",
  "head_branch": "chore/restate-do-not-merge-contract",
  "files_changed": [
    ".github/workflows/check-do-not-merge.yml",
    "CHANGELOG.md"
  ],
  "scope": "docs",
  "focus_areas": ["security", "correctness", "tests"],
  "concerns": [
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The do-not-merge gate is a merge-safety control; the new message advertises a specific bypass set (Admin repository role plus Integration 3907226). Verify branch protection actually lists that integration as a bypass actor and that the setting preventing bypass is not enabled — otherwise the gate states a guarantee it does not enforce."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Bypass is enforced at the branch-protection layer, not by this job, so actors allowed to bypass never reach the error echo. Confirm the wording describes who bypasses the required check rather than implying the check itself exempts them."
    },
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "Integration 3907226 is asserted with no source in the diff. Confirm it is the correct app or integration ID (not an installation ID) and will remain stable, since the message is now the documented contract."
    },
    {
      "area": "tests",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "No automated test or lint covers the emitted message string or the gate behavior; the changed line is only exercised when the label is present, so regressions in the stated contract would go unnoticed."
    }
  ]
}
```
## Review


```json
{
  "verdict": "approve",
  "summary": "Docs-only change: the do-not-merge gate's error message now states the branch-protection bypass contract, plus a CHANGELOG entry. No gate behavior changes (the job still exits 1 for everyone). Selector traceability — candidates: 7 (changelog/conventional-prefix-required, git-commit/feature-branch-no-tag, git-commit/imperative-mood, git-workflow/never-direct-commit-to-master, go-security/resource-ownership, go-security/tenant-isolation, markdown-todo/lowercase-x-for-complete); applicable: 3; skipped: go-security/resource-ownership (no Go code in diff), go-security/tenant-isolation (no Go code in diff), git-workflow/never-direct-commit-to-master (feature-branch PR, not a direct master commit), markdown-todo/lowercase-x-for-complete (no markdown checkboxes changed). Mechanical funnel: 0 findings. .reviewignore excluded 0 files. precommit skipped (selector mode) — CI covers lint+test. CHANGELOG bullet uses an allowed `chore:` prefix, commits are imperative and untagged.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "nit",
      "blocking": false,
      "blocking_reason": "",
      "message": "Nice to Have (Optional) — the message now hardcodes the bypass contract (Admin repository role + Integration 3907226) with no link back to the branch-protection ruleset that owns that list. Nothing in the repo asserts or checks this claim, so if the ruleset's bypass actors change the message silently drifts out of date. Consider referencing where the contract is configured (or where it is verified) so the next change to the ruleset also updates this line. `3907226` is also unqualified — worth stating whether it is an app/integration ID versus an installation ID."
    }
  ],
  "concerns_addressed": [
    {
      "concern": "security: verify branch protection actually lists that integration as a bypass actor and that the setting preventing bypass is not enabled",
      "disposition": "not-an-issue",
      "detail": "Examined .github/workflows/check-do-not-merge.yml and searched the repo for the ID/bypass config. The diff changes only the emitted `::error::` string; the job still exits 1 for every actor, so no enforcement behavior is altered. The branch-protection bypass list is external repository state that cannot be read from this sandbox (GitHub API rate limit exhausted, 0/12500), and no in-repo artifact contradicts the claim — no defect in the change is demonstrated."
    },
    {
      "concern": "correctness: confirm the wording describes who bypasses the required check rather than implying the check itself exempts them",
      "disposition": "not-an-issue",
      "detail": "The wording says the merge is blocked by the label and then names who is exempt from that block ('blocks everyone except actors holding...'). That matches GitHub's branch-protection bypass semantics: the check fails for everyone and bypass actors are exempt at the protection layer. The job body (jq label test + `exit 1`) is unchanged, so the check itself exempts no one."
    },
    {
      "concern": "correctness: confirm Integration 3907226 is the correct app/integration ID (not an installation ID) and will remain stable",
      "disposition": "not-an-issue",
      "detail": "Searched the repo — no source for 3907226 exists in-tree; the identifier originates from external ruleset configuration. I examined the claim and cannot confirm or refute it from the sandbox, and there is no in-repo evidence it is wrong, so no finding is asserted. The message-only nature means a wrong value would be a stale hint, not a broken gate. Noted as a nit to qualify the identifier type."
    },
    {
      "concern": "tests: no automated test or lint covers the emitted message string or gate behavior",
      "disposition": "not-an-issue",
      "detail": "Examined the change: it modifies one literal in an `echo` inside a GitHub Actions run step plus a CHANGELOG bullet. The meaningful gate logic (jq label detection) is unchanged and is not a Go/Python unit; this repo has no workflow-string test harness, and adding one to pin a human-facing message would be low value. The workflow itself is exercised by CI on PR events. No test gap is introduced by this diff."
    }
  ]
}
```
## Diagnostics


job_run: 2026-11-08T18:13:59Z outcome: success review_id: 5819037829
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All four plan concerns appear in concerns_addressed with dispositions, the single comment cites .github/workflows/check-do-not-merge.yml line 19 — a changed file and the exact modified line in the diff hunk — and the approve verdict is consistent with a non-blocking nit. No concern was silently dropped and no hallucinated citation was found.",
  "concerns_addressed": [
    {"concern": "security: verify branch protection lists Integration 3907226 as a bypass actor and that the bypass-prevention setting is not enabled", "status": "addressed"},
    {"concern": "correctness: wording describes who bypasses the required check rather than implying the check exempts them", "status": "addressed"},
    {"concern": "correctness: Integration 3907226 is the correct app/integration ID (not an installation ID) and remains stable", "status": "addressed"},
    {"concern": "tests: no automated test or lint covers the emitted message string or gate behavior", "status": "addressed"}
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
  "merged_at": "2026-11-08T18:17:30Z",
  "head_ref_oid": "70b7c69f266456a4c01dd73ffd010bf51c62d6fb"
}
```
