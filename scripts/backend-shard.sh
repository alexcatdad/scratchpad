#!/usr/bin/env bash
set -euo pipefail

# Run from the backend module. The measured slow group takes about half the
# suite time; its complement includes every other test, including new tests.
# Keep top-level names anchored so subtests stay with their parent.
slow_tests=(
  TestNativeLargeExportUsesBoundedServerMemoryAndRemainsReplayable
  TestPostgresNativeExportKeepsSnapshotPastDefaultWriteDeadlineAndCleansCancellation
  TestPostgresNativeImportCompletesPastOrdinaryRequestDeadline
  TestLegacyJSONLUTF16LimitPreservesMultibyteTextAndAtomicity
  TestAnalysisWorkerAuditFailureLeavesNoActivatedArtifacts
  TestRequestedDocumentRejectsEmptyProviderArtifactResponse
  TestNativeImportAllowsSlowAuthenticatedUpload
  TestRecordDetailReturnsAllAssociatedAuditsAboveNativeBindBoundary
  TestAISchedulingFindsValidOverlappingScopePastMalformedHistoricalJobs
  TestCredentialRevocationIsIndependentOfExpiredSessionHistory
  TestRealStdioAIWorkflowUsesGoWorkers
  TestPublicHostnameMatchesBrowserOriginAndTrustedSSHRecipient
  TestAISchedulePersistsAndDeduplicatesAcrossServersAndRestart
  TestProviderTransportInitiationReleasesWritesBeforeTLSHandshake
  TestHistoricalWorkerDiscardsAdmittedOutputAfterInputConsentWithdrawal
  TestAnalysisWorkerValidatesMandatoryFieldsBeforeModeFiltering
  TestPostgresPreUpgradeSnapshotRestoresRetainedTypeScriptAPI
  TestCaptureRetryIdentityIsScopedToCredential
)
pattern=$(IFS='|'; printf '^(%s)$' "${slow_tests[*]}")

case "${1:-}" in
  1) selection=-run ;;
  2) selection=-skip ;;
  *) printf 'Usage: bash backend-shard.sh {1|2}\n' >&2; exit 2 ;;
esac

# exec preserves test/race failures. Both halves compile all packages with race
# instrumentation and execute uncached tests with the original timeout.
exec go test -json -race -count=1 -timeout 30m "$selection" "$pattern" ./...

