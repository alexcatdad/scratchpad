# Optional extension acceptance

The owner requested these additions on 2 October 2026. Published v0.2.0 remains the prior product foundation; implementation, canonical checks, publication and owner deployment are separate outcomes.

| Requirement             | Behavior and evidence                                                                                                                                                                                                                                                                                                                                                                                                                            |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Native pgvector         | Explicit PostgreSQL opt-in; full 2,560-dimensional exact cosine; compatible authorized candidates before ranking and consent rechecked afterward; transactional update/delete and startup rebuild; missing extension/schema readiness refusal; original-vector preservation and documented float32/unsupported-dimension fallback. Actual disposable pgvector tests pass, alongside vanilla PostgreSQL backup/restore and SQLite/AI regressions. |
| Public GitHub profile   | Authenticated link/refresh/unlink of public user metadata; fixed upstream API, bounded body/deadline, validated URLs, optimistic conflicts and audit; no credential/session/source changes. Focused API/provider tests, restart persistence, imported-data sanitization, deterministic rendered controls and a real public GitHub fetch pass.                                                                                                    |
| Generated clients       | OpenAPI-derived TypeScript and Go clients cover all 44 paths and 52 operations. Deterministic regeneration, compilation, real SSH challenge/verify, authenticated capture/retrieval, typed failures and packaged TypeScript consumption must pass together. Generators do not replace the MCP HTTP boundary.                                                                                                                                     |
| Operations and delivery | New runbooks and public guides describe configuration and consumption. Canonical CI includes extension-enabled PostgreSQL and generated-client integration. Published v0.3.0, fresh installation acceptance and owner upgrade pass as recorded below.                                                                                                                                                                                            |

Implementation checks use disposable records, keys and databases. The owner's live instance now runs published v0.3.0 after the verified backup and upgrade. No public profile is linked and no AI/project permissions are enabled automatically.

Canonical [CI run 36933787062](https://github.com/alexcatdad/scratchpad/actions/runs/36933787062)
passed all jobs at `f5f1158c11ee9b99f0d4caa02c399135b20184ff`, including native
pgvector, ordinary PostgreSQL, Docker, authenticated generated-client integration,
Go and web/document checks. Local Node 24 Docker browser acceptance passed both
the AI/settings and authenticated memory/MCP/restart workflows. Release workflow
changes after that source require a new exact-source check before tagging.

## Published delivery

- Product `v0.3.0` and Go module `packages/clients/go/v0.3.0` both identify
  `b97c5636593d212487fdc6796ee461e3433aa98b`. All jobs in
  [exact-source CI 36934425536](https://github.com/alexcatdad/scratchpad/actions/runs/36934425536)
  passed, including native pgvector and generated-client authentication.
- [Release 36935307686](https://github.com/alexcatdad/scratchpad/actions/runs/36935307686)
  passed preflight, both macOS signing/source/notarization verifications, both Linux
  builds, TypeScript packing, multiarchitecture image publication, checksums,
  publication and Homebrew tap delivery.
- [Fresh acceptance 36935929030](https://github.com/alexcatdad/scratchpad/actions/runs/36935929030)
  passed macOS/Linux Homebrew installation, installed MCP authentication and restart,
  both anonymous image pulls/readiness, published-image AI and backup/restore, and
  independent TypeScript tarball/remote Go module consumers using real SSH-authenticated
  HTTP calls. Linux ARM64 readiness uses QEMU; native ARM64 operation is also verified
  on the owner's Mac.
- Every downloaded release checksum passes locally. The independent released client
  test passes locally, including authentication, capture/retrieval and typed failures.
  Installed Homebrew MCP reports 0.3.0; its formula test and Apple notarization
  requirement pass. The installed binary and published ARM64 image passed the
  isolated authenticated backup/restore workflow in 37.1 seconds.
- The owner deployment runs `ghcr.io/alexcatdad/scratchpad:v0.3.0`, ARM64 image
  `sha256:410ce7b9963b987701cc195f70dffe923df4834be17365a1a50c837b851fbb7f`.
  A protected online backup was verified against the published image in an isolated
  restore. After upgrade, readiness and exact preservation of all 27 entity rows,
  four projects and four records pass. The original Compose project, volume and
  origin are retained. The existing owner browser session displays all four records
  and the new public GitHub profile controls. AI is still disabled and no public
  profile is linked automatically.

The release image index digest is
`sha256:b2b1abe036b1594eef78b153c4699a67697186d1e79ecb85ce9ef6d35fd7512b`.
Runtime and backup verification containing private installation details stays outside
Git. Product delivery is complete; selecting a public GitHub username, enabling
pgvector in a PostgreSQL deployment and opting projects into AI remain owner choices.
