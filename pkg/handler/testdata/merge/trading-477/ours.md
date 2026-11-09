---
assignee: ""
base_ref: master
clone_url: https://github.com/bborbe/trading.git
current_job: ""
job_started_at: "2026-11-08T17:52:05Z"
phase: done
previous_assignee: pr-reviewer-agent
queued_at: "2026-11-08T17:56:02Z"
ref: ae63d7fac8f874e56e2dda606d1db77433fe1ed4
stage: prod
status: completed
target_vault: openclaw
task_identifier: fff5231e-d7dd-5d3e-b461-26a54b8d49fc
task_type: pr-review
title: 'chore: restate the do-not-merge contract'
trigger_count: 2
trigger_scope: planning:ae63d7fa
---
# PR Review: chore: restate the do-not-merge contract



https://github.com/bborbe/trading/pull/477

**Repo:** [bborbe/trading](https://github.com/bborbe/trading)
## Failure


- **Reason:**

```
GH rate limit nearly exhausted (0/12500 remaining) — retry after reset
```
## Plan

```json
{
  "pr_url": "https://github.com/bborbe/trading/pull/477",
  "pr_title": "chore: restate the do-not-merge contract",
  "base_branch": "master",
  "head_branch": "chore/restate-do-not-merge-contract",
  "files_changed": [
    ".github/workflows/check-do-not-merge.yml",
    "CHANGELOG.md"
  ],
  "scope": "docs",
  "focus_areas": ["correctness", "security"],
  "concerns": [
    {
      "area": "correctness",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "The new error text hardcodes the bypass set (Admin repository role plus Integration 3907226). Verify it matches the actual branch-protection bypass configuration; the workflow itself only inspects labels and cannot enforce or confirm this claim, so a stale message would misinform reviewers."
    },
    {
      "area": "security",
      "file": ".github/workflows/check-do-not-merge.yml",
      "note": "A merge-gate message that names who can bypass the gate should not overstate the block. Confirm the phrasing does not imply the gate is universal when admins/integration can still merge, and that exposing the integration ID is intentional in this public repo."
    },
    {
      "area": "correctness",
      "file": "CHANGELOG.md",
      "note": "New bullet is placed above existing fix: entries under Unreleased; check it follows the changelog ordering and conventional-prefix conventions used by surrounding entries."
    }
  ]
}
```
## Review

```json
{
  "verdict": "approve",
  "summary": "PR #477 changes exactly one string in `.github/workflows/check-do-not-merge.yml` (the `::error::` message emitted when the `do-not-merge` label is present) plus one `## Unreleased` CHANGELOG bullet. Reviewed against master tip `9b33f4ddcd` at head `ae63d7fa`. Mechanical funnel (Step 4a) returned 0 findings over 79 YAMLs; 2 files reviewed, 0 excluded by `.reviewignore` (neither changed path matches `/prompts/`, `/specs/`, `mocks/`, or `go.sum`). Selector traceability: 93 judgment-tier rules in the index were candidates (Step 4b-i includes every trigger-less judgment rule unconditionally; of the trigger-bearing ones only `changelog/conventional-prefix-required` matches `CHANGELOG.md`, and the four `@commits` rules are always included). Applicable: 3 — `changelog/conventional-prefix-required`, `git-commit/imperative-mood`, `git-commit/feature-branch-no-tag`. All three pass: the bullet starts with the allowed `chore:` prefix (changelog-guide.md § RULE changelog/conventional-prefix-required), both commit subjects are imperative (`restate`, `add`) at 40 and 26 chars, and no tag is reachable from the feature branch tip. Skips, each justified against its own `applies_when`: every `go-*` rule → \"diff contains no Go source\"; `go-security/resource-ownership` and `go-security/tenant-isolation` → \"no Go handler or service in diff\"; `agent-cmd/*` → \"no agents/, commands/, skills/ files\"; `adr/required-for-irreversible-architecture-decisions` → \"workflow message text, not an architecture decision\"; `claude-md/*` → \"no CLAUDE.md change\"; `git-workflow/never-direct-commit-to-master` → \"both commits on a feature branch\". `git-commit/subject-under-50-chars`, `git-workflow/no-ai-attribution-in-commits`, `changelog/preamble-frozen` and `changelog/unreleased-entry-required` are script-tier, verified by hand: no AI-attribution trailer in either commit body, preamble untouched, and a bullet is present under `## Unreleased`. precommit skipped (selector mode) — CI covers lint+test; this diff touches no compiled code, so there is nothing for it to exercise. One non-blocking finding: the new message hardcodes a bypass-actor claim that has no in-repo source of truth. Approving — it is a log-string accuracy risk, not a defect this change introduces in any executable path.",
  "comments": [
    {
      "file": ".github/workflows/check-do-not-merge.yml",
      "line": 19,
      "severity": "minor",
      "blocking": false,
      "blocking_reason": "",
      "message": "The error message now asserts a specific bypass set — \"blocks everyone except actors holding the Admin repository role and the Integration 3907226\" — but the workflow cannot observe that set: it only reads `github.event.pull_request.labels` and has no access to the branch-protection/ruleset bypass list. `3907226` appears nowhere else in the repo (grep over the whole tree returns only this line), and there is no checked-in ruleset or branch-protection config, so nothing fails when the actual bypass configuration changes. The message will silently drift into a false statement about who can merge, which is precisely the misinforming-a-reviewer failure this PR sets out to fix. Either cite the source of truth for the bypass list next to the line (e.g. a comment naming the ruleset so a reader knows where to re-verify it), or drop the enumerated actors and keep the message to what this job actually knows: the label is applied, so merge is blocked unless the actor is on the branch-protection bypass list. Not blocking — it is log text on a path that still exits 1 correctly."
    }
  ],
  "concerns_addressed": [
    {
      "area": "correctness",
      "concern": "The new error text hardcodes the bypass set (Admin repository role plus Integration 3907226). Verify it matches the actual branch-protection bypass configuration; the workflow itself only inspects labels and cannot enforce or confirm this claim, so a stale message would misinform reviewers.",
      "disposition": "addressed",
      "detail": "Examined the whole tree: `3907226` occurs only on the new line 19 of `.github/workflows/check-do-not-merge.yml`; there is no checked-in ruleset, branch-protection export, or doc recording the bypass list (grep for `bypass|ruleset|branch protection` under docs/ hits only unrelated security docs). The claim is therefore unverifiable in-repo and cannot be kept in sync by anything the repo runs. Raised as the single non-blocking comment on line 19 with a concrete fix (cite the source of truth or drop the enumerated actors). The workflow's exit-1 behavior is unchanged and still correct."
    },
    {
      "area": "security",
      "concern": "A merge-gate message that names who can bypass the gate should not overstate the block. Confirm the phrasing does not imply the gate is universal when admins/integration can still merge, and that exposing the integration ID is intentional in this public repo.",
      "disposition": "not-an-issue",
      "detail": "The phrasing explicitly discloses the limitation (\"blocks everyone except ...\") rather than overstating it, so it does not claim universality. `3907226` is a GitHub App (\"Integration\") ID, a public identifier — it appears in the app's installation URL and in the JWT `iss` claim — and is not a credential; the app's private key is the secret and is not disclosed here. No exploitable exposure and no overstated gate claim."
    },
    {
      "area": "correctness",
      "concern": "New bullet is placed above existing fix: entries under Unreleased; check it follows the changelog ordering and conventional-prefix conventions used by surrounding entries.",
      "disposition": "not-an-issue",
      "detail": "`CHANGELOG.md:13` starts with `chore:`, which is in the allowed prefix set of `changelog/conventional-prefix-required` (changelog-guide.md line 80) and maps to a patch bump. Placement at the top of `## Unreleased` matches the surrounding newest-first convention (e.g. the existing `chore:` bullet at line 19), and changelog-guide.md only pins ordering between version headings (line 72: \"Newest version first\"), not between bullets within a section. The preamble (lines 1-11) is untouched."
    }
  ]
}
```
## Diagnostics

job_run: 2026-11-08T18:13:43Z outcome: success review_id: 5819036964
## Verdict

```json
{
  "verdict": "pass",
  "reason": "All three plan concerns are explicitly addressed (one via a non-blocking comment, two confirmed as non-issues), the single comment cites .github/workflows/check-do-not-merge.yml line 19 which is the added line inside the diff hunk, and an approve verdict with only blocking:false comments is internally consistent.",
  "concerns_addressed": [
    {"concern": "correctness: hardcoded bypass set (Admin role + Integration 3907226) matches actual branch-protection configuration", "status": "addressed"},
    {"concern": "security: merge-gate message does not overstate the block; exposing the integration ID is intentional", "status": "addressed"},
    {"concern": "correctness: CHANGELOG bullet ordering and conventional-prefix conventions under Unreleased", "status": "addressed"}
  ],
  "hallucinations": [],
  "verdict_consistency": "consistent"
}
```
