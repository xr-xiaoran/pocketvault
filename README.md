# PocketVault

Small-file handoff service in Go: content-addressed deduplication, revocable download tickets, and a transparent storage savings meter.

临时文件交接箱：同一用户上传相同内容只保存一份字节；可以生成“限定次数 + 自动过期”的分享凭证；后台查看剩余额度、撤销分享，以及去重节省的空间。不是完整网盘或大文件对象存储。

## Run in two minutes

Requires Go 1.26+. No external database, Docker or C compiler needed for normal build/test.

```powershell
$env:VAULT_KEYS='alice=alice-local-demo-key-12345,bob=bob-local-demo-key-123456'
go run ./cmd/pocketvault
```

In a second PowerShell window:

```powershell
$env:VAULT_KEY='alice-local-demo-key-12345'
powershell -ExecutionPolicy Bypass -File ./scripts/demo.ps1
```

Sample keys are local demo credentials, not deployed secrets. Optional `ADDR` defaults to `127.0.0.1:8082`; `DB_PATH` defaults to `pocketvault.db`. Ctrl+C shuts down. `VAULT_KEYS` is a trusted operator-provisioned map of owners and unique API keys, not a registration/login system.

## API

`/api/` requests require `Authorization: Bearer <owner key>`. Ownership comes from that key, never from a user-supplied owner header or query parameter.

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Database readiness |
| POST | `/api/files?name=example.txt` | Raw bytes body; up to 4 MiB |
| GET | `/api/files` | Owner's files, at most 1000 |
| DELETE | `/api/files/{id}` | Remove reference; cascade shares; reclaim unreferenced blob |
| GET | `/api/stats` | Files/blobs/logical bytes/physical content bytes/savings |
| POST | `/api/files/{id}/shares` | `{"downloads":1,"ttl_seconds":60}` |
| GET | `/api/shares` | Last 100 shares, no raw tokens |
| DELETE | `/api/shares/{id}` | Revoke; repeated revocation is safe |
| GET | `/s/{token}` | Consume one ticket and download as attachment |

## Design details

- Tenant-scoped `(owner, sha256)` content address prevents cross-owner dedup status leaks.
- BLOB bytes and metadata live in one SQLite transaction, simplifying crash consistency.
- Per-owner limits: 64 MiB physical content and 1000 logical files. Each upload is at most 4 MiB.
- Share tokens use 32 cryptographically random bytes. Only SHA-256 of token is stored; raw token is returned once on creation.
- Atomic `UPDATE ... WHERE remaining > 0 AND expires_at > now AND revoked=0 RETURNING` admits downloads; concurrent traffic cannot overspend quota.
- File bytes are loaded before commit. Database read failure rolls back admission.
- **Quota means admitted GET requests, not guaranteed completed downloads.** Network failure after commit still spends an admission. No automatic refund.
- HEAD and Range do not consume a ticket; HEAD is rejected with 405, Range with 416.
- Responses use attachment, nosniff and no-store; deleting a file invalidates its shares.
- Revocation prevents future admissions; it cannot erase a copy already downloaded or stop bytes from a previously admitted request.

Physical content bytes are the sum of live BLOB payload sizes, not the `.db` file size. SQLite pages, WAL and free pages are excluded. No encryption at rest, malware scanning, resumable upload, CDN, distributed replication or public hosting is claimed. Protect the local database and provision HTTPS before exposing the service remotely.

## Verify

```powershell
go test -count=1 -cover ./...
go vet ./...
go build ./...
```

Tests use actual SQLite databases and HTTP listeners. The concurrency test launches 40 contenders through two independent database handles for 3 admissions and requires exactly 3 successes. No benchmark/QPS claim is made. See [TESTING.md](TESTING.md) and [完整学习与面试手册](docs/学习与面试手册.md).

## Source map

- `internal/vault/store.go`: schema, dedup, quotas, ownership and tickets
- `internal/httpapi/api.go`: raw upload, owner auth, headers and downloads
- `cmd/pocketvault/main.go`: validated key configuration and HTTP lifecycle
- `scripts/demo.ps1`: two identical uploads, one-shot share and storage stats

Created as an AI-assisted learning project. Work through the tests and modify the implementation before presenting independent ownership in an interview.

## References

- https://www.sqlite.org/lang_returning.html
- https://www.sqlite.org/wal.html
- https://pkg.go.dev/crypto/rand
- https://pkg.go.dev/crypto/sha256
- https://pkg.go.dev/net/http
