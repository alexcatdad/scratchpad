# Integrate the independent security PRs

The owner authorized squash auto-merge for security PRs #23–#35. Main requires
`web-and-docs`, `go`, `postgres`, `pgvector`, `infrastructure`, `docker` and
`build`. Branches must be up to date; administrators also obey these checks.

1. Confirm the checkout is clean and fetch `origin`. Keep unrelated work intact.
2. Update the next security branch against current `origin/main`. Resolve
   conflicts without dropping another security fix. In `decisions.jsonl`,
   preserve every original record; duplicate IDs with different content require
   investigation rather than silently choosing a side.
3. Temporarily hold that PR's auto-merge while verifying the integration. Run
   relevant lint, type, tests and builds. Review the updated diff against the
   immutable current-main integration baseline along standards and specification
   axes; inspect hosted review feedback too.
4. Push the branch update and wait for current-head required checks and reviews.
   Address actionable feedback before re-enabling squash auto-merge. Do not use
   administrator bypasses or interpret pending checks as success.
5. Confirm the merge, fetch the new main, and repeat for the next PR. Continue
   monitoring until every scoped PR merges or closes, then stop the monitor.

Useful verification commands:

```sh
gh api repos/alexcatdad/scratchpad/branches/main/protection
gh pr checks PR_NUMBER --repo alexcatdad/scratchpad
gh pr view PR_NUMBER --repo alexcatdad/scratchpad \
  --json state,headRefOid,mergeStateStatus,autoMergeRequest
```

These integrations do not establish deployed acceptance. SSH proof v2 requires
coordinated server/client upgrades. Authentication map saturation can temporarily
reject previously unseen owner allowances until counters expire. No manual
production deployment or credential rotation is part of this runbook.
