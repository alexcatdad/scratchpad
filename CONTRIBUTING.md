# Contributing

Thanks for helping. Read [AGENTS.md](AGENTS.md) first: it holds the project's
invariants, layout and checks, for people and agents alike.

## Setup

```sh
brew install go go-task node@24
npm ci
task check
```

Swift checks need Xcode for macOS 26. PostgreSQL, pgvector, browser and
container acceptance need extra services; see
[the development runbook](docs/runbooks/development.md).

## Changes

- The canonical repository is on Forgejo; GitHub is a push mirror. Branch from
  `main` and open a pull request back to `main` on Forgejo.
- Use Conventional Commit messages (`feat:`, `fix:`, `docs:`, `test:`, `ci:`, `chore:`).
- Run `task check` before pushing; add tests for new behaviour.
- Use synthetic fixtures, never real credentials or personal data.
- Describe the observable problem, the change, and the checks you ran.

Forgejo Actions runs `task lint`, `task build` and `task test` on every pull
request, without Swift; run `task check` locally for `macos/` changes. Pull
requests are squash-merged.

## Releases

Maintainers release locally with release-tools; see the Release section of
AGENTS.md. Contributors never need signing credentials.
