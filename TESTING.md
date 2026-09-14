# Verification evidence

Verified on 2026-09-14 with Go 1.26.5 on Windows, and on GitHub Actions Linux.

- Local `go test -count=1 -cover ./...`, `go vet ./...`, `go build ./...`: passed.
- Vault package statement coverage: 68.9%; HTTP package: 60.2% after adding quota rollback and Range/auth boundary tests.
- Real running server plus Windows PowerShell `scripts/demo.ps1`: passed. Two 49-byte uploads resulted in two files, one blob, 98 logical bytes, 49 physical content bytes and 49 saved bytes. A one-shot share allowed one GET and rejected the second with 410. These are sample data, not a universal compression ratio.
- Initial Linux CI including race detection: https://github.com/xr-xiaoran/pocketvault/actions/runs/34847081323
- Current commit CI: https://github.com/xr-xiaoran/pocketvault/actions

## Tests that protect business guarantees

`TestConcurrentLimitedTicketsAcrossConnections`: two independently opened SQLite handles, 40 contenders, 3 admissions, exactly 3 successes.

`TestDedupAndReferenceCleanup`: verifies byte reuse, two logical references, safe deletion of one reference, and final blob cleanup.

`TestOwnershipIsolation`: blocks cross-owner delete/share/revoke and does not expose cross-owner dedup reuse.

`TestOneShotAndNoRawTokenAtRest`: validates token digest persistence and exhaustion.

`TestExpiredAtExactBoundary`, `TestRevokeIsIdempotentAndDeniesDownload`, `TestDeleteInvalidatesShares`: validate expiration, revocation and deletion semantics.

`TestPersistenceAfterReopen`: actual on-disk database reopen preserves file bytes and tickets.

`TestHTTPUploadShareHeadDownloadExhaustion`: real HTTP upload, owner checks, HEAD behavior, download headers, one-shot exhaustion and request size limit.

## Not established by these tests

No public production deployment, file encryption, malware scanning, distributed storage, resumable upload or benchmark QPS is claimed. Quota counts admitted GET requests, not client-confirmed complete transfers. Logical and physical content statistics are not on-disk database file sizes.
