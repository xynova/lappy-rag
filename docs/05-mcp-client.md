# MCP Client

The MCP server exposes documentation search and chat tools over SSE (Server-Sent Events). Any MCP-compatible client can connect to it.

## What You'll Learn

**Getting Started:**
- [Available Tools](#-available-tools): what the server exposes

**Implementation:**
- [Connect from VS Code](#-connect-from-vs-code): settings.json config
- [Connect from Another Client](#-connect-from-another-client): generic SSE endpoint
- [MCP Features Roadmap](06-mcp-features.md): migration priorities and planned capabilities

---

## ⚙️ Available Tools

| Tool | When to use |
| --- | --- |
| `search_docs` | Default search. Semantic-first. Use for conceptual or paraphrased questions where exact keywords may differ. |
| `ask_docs` | Ask a natural-language question. Retrieves relevant chunks and returns a generated answer. |
| `get_document` | Retrieve all chunks from a specific file by path or doc ID. |
| `get_section` | Jump to a specific section within a known document by heading text. |
| `list_sources` | List all indexed source folders. Use to discover what is available before searching. |
| `trigger_ingest` | Trigger an ingest job for a project and optionally wait for completion. |

The server instructs connected clients to use these tools only for external Markdown documentation, not for source code (use workspace context for code).

---

## 📦 Connect from VS Code

Add the following to your `settings.json` (user or workspace level):

```json
{
  "mcp": {
    "servers": {
      "local-docs": {
        "type": "sse",
        "url": "http://localhost:3000/sse"
      }
    }
  }
}
```

The server name (`local-docs`) is the label shown in the MCP tools panel. Use any name you prefer.

If you run multiple Meilisearch indexes for different doc sets, you can point each at a separate `mcp-server` instance by changing `MCP_PORT` and `MEILI_INDEX` per instance.

---

## 🔗 Connect from Another Client

The SSE endpoint is at:

```
http://localhost:3000/sse
```

Any MCP client that supports SSE transport can connect using this URL. Change the port via `MCP_PORT` in `docker/env/.env`.

---

## 🧪 Test MCP Integration

Use this flow to validate that MCP and Meilisearch chat work end-to-end without wiring into Copilot first.

1. **Start required services:**

  ```
  docker compose -f docker/compose.yml up -d meilisearch localai mcp-server
  ```

2. **Verify MCP server health:**

  ```
  curl http://localhost:3000/health
  ```

3. **Verify Meilisearch is reachable:**

  ```
  curl http://localhost:7700/health
  ```

4. **Open MCP Inspector and connect to SSE:**

  ```
  npx @modelcontextprotocol/inspector
  ```

  In Inspector, connect to:

  ```
  http://localhost:3000/sse
  ```

5. **Run tools in this order:**
  - `tools/list` to confirm the tool set includes `search_docs`, `ask_docs`, `get_document`, `get_section`, `list_sources`, and `trigger_ingest`
  - `tools/call` with `list_sources` to confirm documents are indexed
  - `tools/call` with `search_docs` and a sample query, for example:

  ```json
  {
    "query": "What is the setup process for this stack?",
    "top_k": 5
  }
  ```

  - `tools/call` with `ask_docs` for a generated answer, for example:

  ```json
  {
    "question": "What is the setup process for this stack?",
    "top_k": 5
  }
  ```
