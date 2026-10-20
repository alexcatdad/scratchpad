# Go runtime and TypeScript backend retirement

This runbook records the acceptance boundary for making the Go service the
default and retiring the TypeScript API implementation. It separates local
behavior evidence from CI, image, rollback, and deployed-runtime acceptance.

## Behavior gates

| Behavior                                                                                     | Go acceptance                                                                                                           | Remaining gap before deleting the legacy source                                                             |
| -------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| Authenticated capture, projects, search, detail, curation, portability, AI/RAG and readiness | `backend/*_test.go`; SQLite and PostgreSQL service shards                                                               | Run the complete Node 24 backend suite on both database engines.                                            |
| Dashboard and passkey flows                                                                  | `tests/go-dashboard.spec.ts`, `tests/go-github.spec.ts`; `tests/workflow-harness.ts` starts the Go public process       | Run the full browser suite against the current built renderer and Go API.                                   |
| Private AI settings and document/retrieval flows                                             | `tests/post-mvp.spec.ts`; `tests/real-ai.spec.ts` remains explicit opt-in                                               | Verify required encryption-key fixture and current renderer build.                                          |
| GitHub OAuth, published key lifecycle, outages, and recovery                                 | `backend/github_test.go`; browser coverage in the Go GitHub suite                                                       | Preserve the lifecycle matrix before removing `tests/github-workflow.spec.ts`.                              |
| Generated TypeScript and Go clients                                                          | `backend/sdk_test.go` runs both clients against a Go process; `packages/clients/test.ts` requires that external fixture | `npm run sdk:test` must invoke the targeted Go test.                                                        |
| Historical numeric JSONL identity and raw source preservation                                | `backend/legacy_numeric_identity_review_test.go` freezes TypeScript-emitted IDs and hashes for synthetic inputs         | Run both SQLite and PostgreSQL cases.                                                                       |
| Private operator-history acceptance                                                          | `scripts/validate-history.ts` exercises a disposable Go SQLite process through HTTP                                     | Keep this optional and local; never upload inputs or output artifacts to CI.                                |
| Schema and database restoration                                                              | Go migration, schema validation, and portability suites                                                                 | Keep the old-runtime refusal/restore test against the frozen legacy image until the rollback window closes. |

## Replaced TypeScript server test coverage

The removed server unit tests must not be treated as disposable just because the
Go suite has similarly named tests. These named Go tests retain the behaviors that
the retirement review identified:

| Former TypeScript test area                                                               | Go behavior gates                                                                                                                                                                                                                                                                                                                                                  |
| ----------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `api.test.ts`, `auth-abuse.test.ts`                                                       | `TestRecordRequiresAuthentication`, `TestAuthTrustedProxySelectsNearestUntrustedClientAndRejectsSpoofing`, `TestTrustedProxyKeepsIndependentAuthRoutesEnrollmentAndRecoveryAvailable`, `TestCredentialRevocationStopsAgentAccess`                                                                                                                                  |
| `webauthn.test.ts`, `ssh-recipient.test.ts`                                               | `TestHistoricalPasskeyLoginAndProofFailures`, `TestOwnerSetupAndRecoveryCLI`, `TestLocalSSHEnrollmentAllowsRealAgentRead`, `TestCredentialRevocationStopsAgentAccess`                                                                                                                                                                                              |
| `context.test.ts`, `date-search.test.ts`                                                  | `TestProjectContextOrdersPreciseStateAndBoundsHistory`, `TestProjectContextKeepsHistoricalAndUnverifiedKnowledgeSeparate`, `TestSearchPreservesCuratedAndTemporalFilters`                                                                                                                                                                                          |
| `imports.test.ts`                                                                         | `TestLegacyJSONLMapsAllTypesWithoutDiscardingDisabledHistory`, `TestLegacyJSONLRetainsSourceEvidenceAndUnverifiedAuthority`, `TestLegacyJSONLRollsBackLateImportAuditFailure`, `TestLegacyNumericIdentityMatchesFrozenImporterGoldenWithoutRoundingEvidence`, `TestNativeArchiveReplayComparesExactNumericalMeaning`                                               |
| `github-access.test.ts`, `github-profile.test.ts`                                         | `TestGithubOAuthEstablishesBrowserOwner`, `TestGithubCompleteKeySyncAndSSHLogin`, `TestGithubFailedSyncPreservesCompleteCache`, `TestGithubLocalBlocksAndIndependentUnlink`, `TestGithubStableAccountReplacementAndRecovery`, `TestPublicGithubProfileDoesNotAuthorizeOwner`                                                                                       |
| `readiness.test.ts`, `sqlite-permissions.test.ts`, `postgres.test.ts`, `pgvector.test.ts` | `TestGoServerReadiness`, `TestSQLiteRejectsUnsafeFiles`, `TestSQLitePrivateCreationUnderPermissiveUmask`, `TestSQLiteRejectsUnsafeDirectoryAndSidecars`, `TestSQLiteRejectsFIFOWithoutBlocking`, `TestSQLiteRejectsDarwinACL`, `TestUnsupportedSchemaIsRefusedWithoutChangingBackup`, database-specific startup/backup tests, and the pgvector container scenarios |
| `ai.test.ts`                                                                              | `TestProviderConfigurationChangeStopsFutureRequestsWithoutHoldingWrites`, `TestEncryptedProviderSecretSupportsRestartAndBlocksWrongKeys`, `TestAIConfigurationSupportsIfMatchAndRejectsInvalidChanges`, plus the AI job, completion, export and RAG suites                                                                                                         |

The browser acceptance remains `tests/go-dashboard.spec.ts` and the migrated
GitHub/passkey workflow suites, which exercise a real Go process and real browser
HTTP. Keep the server unit-test source available in the pinned rollback artifact
only for the historical API refusal-and-restore proof; current product tests run
against Go.

## Runtime and packaging gates

The development entrypoint must launch the Go API and a renderer configured for
UI-only use. The renderer must not receive database, GitHub OAuth, or encryption
secrets. Test setup and administration use the Go CLI. Production startup,
health checks, SQLite permissions, PostgreSQL configuration, and the default
container image must all exercise the same Go runtime contract.

The Go image test must explicitly target the isolated local Docker daemon or a
documented disposable remote daemon. It must never inherit the configured
homelab Docker context. The retained historical image may be started solely for
rollback proof: it must refuse the Go-owned schema without damaging the
database, and the previously captured database must restore to the historical
runtime with credentials and records available.

## Retirement completion criteria

Remove the TypeScript API implementation only after the behavior gates above
pass on the current source and generated renderer. Keep the three renderer
health/API route stubs UI-only, retain the compact numeric oracle, and retain
the frozen old image only for the agreed rollback window. Remove backend-only
dependencies and lockfile entries after import scans and CI demonstrate that
the frontend, documentation, generator, and test runners no longer require
them. Record the exact CI commit and image digest used for acceptance before
closing the rollback window.
