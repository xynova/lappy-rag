# Ingest

The ingest pipeline crawls your local Markdown folders, chunks each file, embeds the chunks, and writes them to Meilisearch. You can run it as a one-shot job or trigger it via the API.

## What You'll Learn

**Concepts:**
- [Ingest Modes](#-ingest-modes): full, incremental, and watch
- [Job States](#-job-states): lifecycle of a job

**Implementation:**
- [Running as a One-Shot Job](#-running-as-a-one-shot-job): via `docker compose run`
- [Running via the API](#-running-via-the-api): trigger and monitor jobs over HTTP

---

## ⚙️ Ingest Modes

| Mode | Behaviour |
| --- | --- |
| `full` | Re-indexes every file in the project. Use for a first run or to recover from a corrupted index. |
| `incremental` | Compares file hashes against saved state. Only re-indexes files that have changed or been added. Deletes chunks for removed files. |
| `watch` | Runs a continuous incremental loop. Not recommended for manual runs. |

State files (file hashes) are written to `STATE_DIR` (`/app/state` by default), which is mounted from `docker/state/` on the host.

---

## 🔍 Job States

| Status | Meaning |
| --- | --- |
| `queued` | Job accepted, waiting for a concurrency slot. |
| `running` | Actively crawling and indexing. |
| `completed` | Finished. Check `files_changed` and `chunks_indexed` in the response. |
| `failed` | Stopped due to an error. Check the `error` field. |

The API enforces `MAX_CONCURRENT_JOBS` globally and `MAX_CONCURRENT_PER_PROJECT` per project. Submitting a second job for the same project while one is running returns `409`.

---

## 📦 Running as a One-Shot Job

The `ingest-job` service runs and exits. It uses the `manual` profile so it does not start with the main stack.

**Full run (first time or full re-index):**

```
docker compose -f docker/compose.yml run --rm ingest-job --project=<id> --mode=full

The container entrypoint is `ingest-job run`; compose passes `--project` and `--mode` as extra arguments to that command.
```

**Incremental run (changed files only):**

```
docker compose -f docker/compose.yml run --rm ingest-job --project=<id> --mode=incremental
```

The job prints a JSON summary on completion:

```json
{
  "files_scanned": 120,
  "files_changed": 4,
  "files_skipped": 116,
  "files_deleted": 0,
  "chunks_planned": 31,
  "chunks_indexed": 31
}
```

---

## 🔗 Running via the API

The `ingest-api` service exposes an HTTP API for triggering jobs without restarting containers. It is useful for scheduled or event-driven ingest.

**Trigger a job:**

```
POST http://localhost:8090/jobs/ingest
Content-Type: application/json

{
  "project_id": "<id>",
  "mode": "incremental"
}
```

Returns a job ID immediately.

**Poll job status:**

```
GET http://localhost:8090/jobs/<job-id>
```

**List available projects:**

```
GET http://localhost:8090/projects
```

**Reload project configs without restarting:**

```
POST http://localhost:8090/projects/reload
```
