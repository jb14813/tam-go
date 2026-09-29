# Go-only data-integrity review

September 29, 2026. Implementation on `fix/event-recovery`, based on `21fa9d5ce5c2d28291d49bf3ab8d5e17348e2701`.

The app now supports the Go client, Go server and Go-native event backups. No temporary migration code, data conversion or event-data deletion was added. Manual connection settings, Auth Keys and Push remain supported.

## Corrections

| Reproduced problem | Result and regression coverage |
|---|---|
| Restoring an unresolved backup falsely marked alternatives reviewed; subsequent import/recovery silently chose one winner. | Restore preserves unresolved ancestry through re-export, client import and server reconstruction. `internal/store/native_integrity_test.go`. |
| HTML 200 or an incomplete acknowledgement could remove a save that the server never stored. | Direct and queued delivery require native capabilities and a receipt matching the operation and each accepted value; missing or invalid acknowledgements leave the journal pending. `internal/remote/native_test.go`, `internal/store/receipt_validation_test.go`, `internal/client/native_protocol_test.go`, `internal/sync/native_protocol_test.go`. |
| Importing newer operation history over an older database reused local counters and prevented subsequent corrections. | Imported history advances matching local operation counters. Store and actual client save-order allocation are both covered by rollback/import regressions. |
| Deleting an already absent prefix returned 404 after committing its history, losing the accepted ancestry. | Idempotent deletion returns a complete successful receipt; direct and queued cases retain ancestry after server replacement. `internal/client/native_restore_test.go`. |
| The old two-request restore workaround could overwrite a concurrent winner. | Native restore uses one server operation; no follow-up drawing overwrite. `internal/client/native_restore_test.go`. |
| Two clients sharing a key hid one another's pending queue. | Presence is keyed by access key plus stable client identity. Status shows separate pending, refused and recovering state. After server restart, unknown queue state remains unknown. `internal/admin/native_status_test.go`, `internal/presence/presence_test.go`. |
| Concatenating contact fields collapsed different buyers into the same count. | Counts use the actual contact tuple. `internal/store/native_integrity_test.go`. |
| A prefix called Total collided with the aggregate row. | Aggregate rows have an explicit `is_total` flag in the API and UI. Store and browser regressions cover this. |
| Prefix Settings reloaded after acknowledgement and discarded newer typing. | Acknowledgements reconcile only the saved values; later edits remain visible and unsaved. `scripts/browser/data-integrity.spec.js`. |
| IDs larger than JavaScript's exact integer range could round into another record's identity. | Inputs, backup metadata, lookup ranges and browser records require exact integers in 0..9007199254740991. Invalid mixed requests reject atomically. Existing unsafe records cannot be edited through rounded browser IDs. Store, HTTP and browser regressions cover the boundary. |

Independent review found and verified two further corrections: imports must advance the actual client's save allocator, and existing queued Go prefix operations must keep their retry identity. The load test also exposed an added handshake exceeding the page's save timeout; one five-second deadline now covers handshake, write and retries. Each case has a regression test. Final review found no remaining actionable issues.

## Native-only contract

- New exports carry `format: "tam-native-v1"`, including empty backups. Earlier Go exports with recognizable native ownership or history metadata remain supported. Original or ambiguous three-list files are rejected before mutation.
- Ordinary server writes require a stable client identity, a positive save number and a complete native acknowledgement. Operator restore has an explicit native restore request. Recovery and review endpoints require native snapshots and identity. Existing numbered Go prefix Push operations retain their validation and retry identity; journals need no conversion.
- Removed old-server restore replay, receiptless acceptance, the alternate original key header, original-app compatibility scripts/tests and the compatibility CI job.
- Existing Go databases and known Go upgrades remain supported. Unknown columns are left alone. Retained ownership and accepted history are preserved.
- Documentation now describes the current protocol; dated old validation results are clearly historical.

## Validation

Final integrated checks passed. Evidence is retained in `build/validation/go-only-fix/`; original defect reproductions remain in `build/validation/go-only-review/`. The former opt-in probes are now ordinary correctness regressions.

| Check | Result |
|---|---|
| Production frontend and both Linux programs | Built successfully. |
| `go test -count=1 ./...` | All 15 test packages passed. |
| `go test -race -count=1 ./...` | All 15 test packages passed. |
| Chromium browser suite | All 65 passed. |
| 50-client HTTP event, 9,000 tickets, 1,000 baskets | All 26 checks passed; server restart, Wi-Fi loss, client crashes and queue replay. |
| 20-client TLS event, 6,000 tickets, 1,000 baskets, 45-second server outage | All 26 checks passed. |
| Cross-build and vet | Passed for Windows, Linux and macOS on amd64 and arm64. |
| Formatting and diff checks | Clean. |

Runtime validation used disposable synthetic event data in Linux containers. Cross-builds do not constitute Windows/macOS runtime tests. No release packaging, deployment or live event data was involved.
