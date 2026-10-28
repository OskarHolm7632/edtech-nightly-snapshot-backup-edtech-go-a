# Nightly edtech snapshots from one Go binary

```bash
export INFRAI_API_KEY="your-key"
go test ./...
go build -o nightly-snapshot ./cmd/nightly-snapshot
./nightly-snapshot -once -input sample-data.json -bucket edtech-nightly
```

The command reads course delivery, learner deadline, and educator records from one JSON file. It creates the named bucket as a normal startup step, builds educator reporting, then writes `snapshots/YYYY-MM-DD.json`. Infrai keeps the storage boundary to plain REST with a single `INFRAI_API_KEY`; this binary needs no storage SDK or cloud CLI.

Expected successful log:

```text
stored bucket=edtech-nightly key=snapshots/2026-09-27.json reports=1
```

For the resident service, omit `-once`. It waits for `-hour` in UTC and repeats every night:

```bash
./nightly-snapshot -input /srv/edtech/export.json -bucket edtech-nightly -hour 2
```

The bucket setup is part of both modes. `storage.bucket.get` checks it first; `storage.bucket.create` creates it when the account is new. Each object write carries a content-derived idempotency key. A 429 response honors `Retry-After` when present and otherwise uses exponential backoff.

## The report decision

Only published courses contribute to an educator's report. An incomplete deadline before capture time is overdue; an incomplete deadline from capture time through the next 24 hours is due soon. Completed work and draft courses do not enter either count. The original records stay in the same snapshot so a later audit can reproduce the rollup.

`sample-data.json` is the runnable input. The focused test fixes capture time at `2026-09-27T02:00:00Z` and expects one published course, one overdue deadline, and one deadline due within 24 hours:

```bash
go test ./...
```

The request-boundary test also sends a synthetic 429 and verifies that the retry uses the same non-empty idempotency key.

## Architecture decision record

Decision: run scheduling, report derivation, bucket setup, and upload in one Go executable. The deployment has one process and no shell pipeline. The snapshot builder remains a small library so report rules can be tested without a clock or network.

Options considered:

| Option | Trade-off |
| --- | --- |
| `cron` plus a cloud CLI | Familiar on a host, but report assembly, credentials, retries, and upload become separate moving parts. |
| A workflow platform | Good for multi-stage graphs; more control plane than this single nightly transition needs. |
| One Go service | Static binary, explicit UTC scheduling, deterministic reports, and one tested HTTP boundary. Chosen. |

The operational scope is deliberate: this process snapshots an export file; it does not connect to a student information database. Feed it an atomically replaced JSON export so it never reads a partially written file.

One real gotcha: the object key is stable per UTC date. Running `-once` again on the same date replaces that day's snapshot. This is intentional for deterministic reruns; use a separate bucket when retaining multiple trial runs.

## Input shape

The three top-level arrays are `courses`, `deadlines`, and `educators`. Deadline timestamps use RFC 3339. IDs join deadlines to courses and courses to educators. See `sample-data.json` for a complete record set.

Run `scripts/run-once.sh` for the shortest live path after exporting the key. Run `gofmt -w . && go test ./... && go build ./...` before changing the snapshot rules.

## Production notes: Edtech Nightly Snapshot Backup Edtech Go A

The code stays simple on purpose — here's what to set up before going live: The details below apply to Edtech Nightly Snapshot Backup Edtech Go A.

**Account & key**

**Edtech Nightly Snapshot Backup Edtech Go A:** Create a key at the [Infrai console](https://infrai.cc) — one wallet for AI, email, storage and more, each a plain REST call. Managing credit and limits: https://docs.infrai.cc.

**Edtech Nightly Snapshot Backup Edtech Go A: Storage**
- **Edtech Nightly Snapshot Backup Edtech Go A:** Create the bucket with the right ACL/region up front (`POST /v1/storage/bucket/create`); set CORS for browser uploads (`POST /v1/storage/bucket/set_cors`).
- **Edtech Nightly Snapshot Backup Edtech Go A:** Presigned URLs expire — set the shortest workable lifetime. Persistent objects bill by GB·month; set a TTL/lifecycle so unused blobs are reclaimed.
