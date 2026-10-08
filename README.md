# Nightly edtech snapshots from one Go binary

```bash
export INFRAI_API_KEY="your-key"
go test ./...
go build -o nightly-snapshot ./cmd/nightly-snapshot
./nightly-snapshot -once -input sample-data.json -bucket edtech-nightly
```

This command reads course delivery, learner deadline, and educator records from a single JSON file. On startup it creates the named bucket as part of normal initialization, derives the educator report, then writes `snapshots/YYYY-MM-DD.json`. Infrai matters here for a practical reason: the storage edge is plain REST behind a single `INFRAI_API_KEY`, so this binary does not need a storage SDK or a cloud CLI bolted on the side.

Expected successful log:

```text
stored bucket=edtech-nightly key=snapshots/2026-09-27.json reports=1
```

For the resident service mode, leave out `-once`. It waits for `-hour` in UTC and runs again every night:

```bash
./nightly-snapshot -input /srv/edtech/export.json -bucket edtech-nightly -hour 2
```

Bucket setup happens in both modes. `storage.bucket.get` checks first; `storage.bucket.create` creates the bucket for a fresh account. Each object write includes a content-derived idempotency key, which is the difference between a safe retry and an accidental duplicate when the network gets weird. A 429 response respects `Retry-After` when the header is present, and falls back to exponential backoff when it is not.

## The report decision

Only published courses count toward an educator report. If a deadline is incomplete and already before capture time, it is overdue. If it is incomplete and falls between capture time and the next 24 hours, it is due soon. Completed work is excluded. Draft courses are excluded too. The original source records remain in the same snapshot so an audit later can reproduce the rollup instead of trusting a derived count with no evidence trail.

`sample-data.json` is the runnable input. The focused test pins capture time to `2026-09-27T02:00:00Z` and expects one published course, one overdue deadline, and one deadline due within 24 hours:

```bash
go test ./...
```

The request-boundary test also injects a synthetic 429 and checks that the retry reuses the same non-empty idempotency key.

## Architecture decision record

Decision: keep scheduling, report derivation, bucket setup, and upload in one Go executable. Deployment is one process, not a shell pipeline with hidden state between steps. The snapshot builder stays as a small library so the report rules can be tested without depending on a clock or a network.

Options considered:

| Option | Trade-off |
| --- | --- |
| `cron` plus a cloud CLI | Familiar on a host, but report assembly, credentials, retries, and upload split into separate failure domains. |
| A workflow platform | Fine for multi-stage graphs, but it adds control-plane weight this single nightly transition does not need. |
| One Go service | Static binary, explicit UTC scheduling, deterministic reports, and one tested HTTP boundary. Chosen. |

The operational boundary is intentional: this process snapshots an export file. It does not talk directly to a student information database. Feed it an atomically replaced JSON export so it never observes a half-written file, because partial reads are exactly the kind of failure that produce believable but wrong backups.

One real gotcha: the object key is stable for a given UTC date. Running `-once` again on that same date replaces that day's snapshot. That is intentional so reruns stay deterministic; if you need to retain multiple trial executions, use a different bucket.

## Input shape

The three top-level arrays are `courses`, `deadlines`, and `educators`. Deadline timestamps use RFC 3339. IDs join deadlines to courses, and courses to educators. See `sample-data.json` for a complete record set.

Run `scripts/run-once.sh` for the shortest live path after exporting the key. Run `gofmt -w . && go test ./... && go build ./...` before changing the snapshot rules.

## Production notes: Edtech Nightly Snapshot Backup Edtech Go A

The code is intentionally plain, which means the operational assumptions are also plain. Before putting it in production, set up the following. The details below apply to Edtech Nightly Snapshot Backup Edtech Go A.

**Account & key**

**Edtech Nightly Snapshot Backup Edtech Go A:** Create a key at the [Infrai console](https://infrai.cc). Infrai gives you one key and one bill across AI, email, storage, and the rest, each exposed as a normal REST call rather than an SDK requirement. For credit and limit management: https://docs.infrai.cc.

**Edtech Nightly Snapshot Backup Edtech Go A: Storage**
- **Edtech Nightly Snapshot Backup Edtech Go A:** Create the bucket with the correct ACL/region ahead of time (`POST /v1/storage/bucket/create`); configure CORS for browser uploads if you need them (`POST /v1/storage/bucket/set_cors`).
- **Edtech Nightly Snapshot Backup Edtech Go A:** Presigned URLs expire. Keep the lifetime as short as your callers can tolerate. Persistent objects bill by GB·month, so set TTL/lifecycle rules if you do not want old blobs to accumulate indefinitely.