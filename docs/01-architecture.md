# Architecture

## What You'll Learn

**Concepts:**
- [The Data Flow](#-the-data-flow): how a question becomes an answer
- [Components](#-components): what each service does
- [Ports and Interfaces](#-ports-and-interfaces): service endpoints at a glance

---

## 💡 The Data Flow

The system has two pipelines: **ingest** (offline) and **query** (online).

**Ingest pipeline:**
1. The ingest job reads your bundle configs and scans local Markdown folders.
2. Each file is chunked using the strategy defined in your bundle rules.
3. Chunks are embedded via the embed server (BGE model, 768 dimensions).
4. Embedded chunks are stored in Meilisearch.

**Query pipeline:**
1. An MCP client sends a query to the MCP server.
2. The MCP server runs a hybrid search (keyword + vector) against Meilisearch.
3. Matching chunks are returned to the client as context.
4. The `chat_with_docs` tool additionally calls LocalAI to produce a natural-language answer from those chunks.

---

## ⚙️ Components

| Service | Image / Source | Purpose |
| --- | --- | --- |
| **embed-server** | `docker/embed-server/` | OpenAI-compatible `/v1/embeddings` endpoint backed by `bge-base-en-v1.5`. Meilisearch calls this when indexing documents. |
| **localai** | `docker/localai/` | OpenAI-compatible chat completions serving `gemma-4-12b`. Used by `chat_with_docs` to generate answers from retrieved chunks. |
| **meilisearch** | `getmeili/meilisearch:v1.14` | Hybrid vector + keyword search index. Stores all document chunks and their embeddings. |
| **ingest-api** | `go/cmd/ingest-api/` | HTTP API for triggering and monitoring ingest jobs. Manages concurrency across projects. |
| **ingest-job** | `go/cmd/ingest-job/` | One-shot CLI job that crawls a single project and populates Meilisearch. Run via `docker compose run`. |
| **mcp-server** | `go/cmd/mcp-server/` | MCP server over SSE. Exposes search and chat tools to any MCP-compatible client. |

The embed server and LocalAI are separate by design. Embedding runs at ingest time; chat runs at query time. Either can be replaced independently.

---

## 🔗 Ports and Interfaces

| Service | Default port | Interface |
| --- | --- | --- |
| localai | `8080` | OpenAI-compatible REST API |
| embed-server | `8082` | OpenAI-compatible `/v1/embeddings` |
| meilisearch | `7700` | Meilisearch REST API |
| ingest-api | `8090` | HTTP REST (`/healthz`, `/projects`, `/jobs/ingest`) |
| mcp-server | `3000` | MCP over SSE (`/sse`) |

All ports bind to `127.0.0.1` by default except Meilisearch. Override any port via the `.env` file.
