# Public GitHub profile enrichment

This runbook covers the separate descriptive profile interface. Optional verified GitHub owner authentication is documented in the [OAuth runbook](github-oauth.md); an existing presentation snapshot is never promoted into that binding.

Scratchpad uses this public GitHub profile for presentation only. It does not verify account ownership, enroll a credential, authorize access, or replace your owner display name. Your existing passkey or SSH credential continues to authenticate you.

## Link, refresh and unlink

1. Sign in to the dashboard and open **Settings**.
2. Under **Public GitHub profile**, enter the username you want to display and choose **Link GitHub profile**.
3. Review the linked username, public display name, avatar and retrieval time. The profile link opens GitHub separately.
4. Choose **Refresh GitHub profile** to retrieve current public information. Editing the username selects another personal profile. There is no background synchronization.
5. Choose **Unlink GitHub profile** to remove the current presentation snapshot. The ordinary profile audit trail retains the previous snapshot.

No GitHub token, OAuth application, PAT or private permissions are required. Only the chosen username is sent to GitHub. The browser loads the optional public avatar from GitHub's avatar host without a referrer. GitHub receives that browser request when the linked profile is displayed.

## API and persistence

Authenticated `POST /api/v1/profile/github` accepts `{username, expectedVersion}`. Authenticated `DELETE /api/v1/profile/github` accepts `{expectedVersion}`. Both return `{profile}`. Use the current profile version from `GET /api/v1/profile`; stale updates return `409 CONFLICT`. A concurrent profile change during retrieval also prevents the result from overwriting the newer profile.

The optional `profile.github` snapshot contains `username`, nullable `displayName`, nullable `avatarUrl`, `profileUrl` and `fetchedAt`. Unlink sets it to `null`. The same profile entity, optimistic version and audit mechanisms work on SQLite and PostgreSQL and participate in native knowledge export/import.

The server makes one unauthenticated request to `https://api.github.com/users/{username}`, using GitHub REST version `2026-03-10`. Redirects are rejected, retrieval has a five-second deadline, and response content is capped at 64 KiB. Only personal accounts with a matching username are accepted. Returned profile URLs are constructed from the validated username; avatar URLs are restricted to HTTPS on `avatars.githubusercontent.com`. Other upstream fields are discarded.

## Troubleshooting and verification

- A missing or renamed account returns `GITHUB_PROFILE_NOT_FOUND`. Enter its current username explicitly.
- GitHub rate limiting returns `GITHUB_RATE_LIMITED`; wait before refreshing.
- Network failure, deadline expiry, malformed responses or an organization profile return `GITHUB_UNAVAILABLE`. The last saved snapshot is preserved.
- A stale profile version returns `CONFLICT`; reload Settings before retrying.

Run the focused tests from the repository root:

```sh
npm exec -w @scratchpad/web -- vitest run src/server/github-profile.test.ts
npm run typecheck -w @scratchpad/web
```

These tests exercise fixed-origin/no-token requests, public-field filtering, path and avatar safety, organization rejection, response/time bounds, authenticated link/refresh/unlink, audit entries, unchanged credentials and conflict protection. The dashboard acceptance fixture covers the rendered presentation and controls without depending on GitHub availability.

GitHub documents the unauthenticated public-user endpoint and versioned request headers in its [REST user API reference](https://docs.github.com/en/rest/users/users#get-a-user).
