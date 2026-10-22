# Setup

## What You'll Learn

**Getting Started:**
- [Prerequisites](#-prerequisites): what you need before starting
- [Environment Variables](#-environment-variables): required and optional config

**Implementation:**
- [First Run](#-first-run): build and start the stack
- [Verify the Stack](#-verify-the-stack): confirm all services are healthy

---

## ⚠️ Prerequisites

**Required:**
- Docker with Compose v2 (`docker compose version`)
- Git

**For corporate / proxy environments:**
- Shipped defaults in `.env.example` are **empty** so public-internet builds work without a private registry.
- Set `REGISTRY_VENDOR` to `artifactory` or `nexus` when using a private registry (leave empty for public internet). Fill full URLs for `PACKAGE_REGISTRY_HOST`, `DOCKER_REGISTRY_PROXY` (must end with `/` when set), `DOCKER_PUSH_DOMAIN`, `GOPROXY`, `CORP_CA_CERT_URL`, and optionally `PIP_INDEX_URL` in `docker/env/.env`. See both commented overlay blocks in `.env.example`.
- If you copied `.env` before renaming registry env keys, recopy from `.env.example` (keep your `MEILI_MASTER_KEY` and `CONTENT_ROOT_BASE`).

**Corporate package registry (optional):**
- Set `REGISTRY_USER` and `REGISTRY_TOKEN` in your shell before `make serve` (alias: `make up`) / `docker compose build`. When credentials are set, also set `REGISTRY_GO_CHECK_URL` and `PACKAGE_REGISTRY_HOST`. Run `make registry-auth-check` to verify access. Preflight scripts skip when credentials are unset.

---

## ⚙️ Environment Variables

Copy the example file:

```
cp docker/env/.env.example docker/env/.env
```

<details>
<summary><strong>📋 Full variable reference</strong> (click to expand)</summary>

| Variable | Default | Notes |
| --- | --- | --- |
| `MEILI_MASTER_KEY` | *(required)* | Set this to any secret string before starting. |
| `MEILI_PORT` | `7700` | |
| `MEILI_INDEX` | `documents` | Meilisearch index name used by the MCP server. |
| `INGEST_API_PORT` | `8090` | |
| `EMBED_PORT` | `8082` | |
| `EMBED_MODEL` | `bge-base-en-v1.5` | |
| `EMBED_DIMENSIONS` | `768` | Must match the model. |
| `EMBED_DOCUMENT_TEMPLATE` | *(optional)* | Liquid template controlling what text is embedded. |
| `LOCALAI_PORT` | `8080` | |
| `MCP_PORT` | `3000` | Port the MCP server listens on. |
| `CONTENT_ROOT_BASE` | `C:/repos` | Host path mounted into containers as `/content/projects`. Set to your local repos root. On macOS/Linux use `/home/<you>/repos`. |
| `STATE_DIR` | `/app/state` | Container path for ingest state files (tracks file hashes for incremental runs). |
| `MAX_CONCURRENT_JOBS` | `3` | Max ingest jobs running simultaneously across all projects. |
| `MAX_CONCURRENT_PER_PROJECT` | `1` | Max concurrent jobs for a single project. |
| `REGISTRY_VENDOR` | *(empty)* | `artifactory` or `nexus` when using a private registry; empty for public internet. |
| `PACKAGE_REGISTRY_HOST` | *(empty)* | Hostname for private registry (netrc / `GOINSECURE` during Go image builds). |
| `DOCKER_REGISTRY_PROXY` | *(empty)* | Prefix for Docker image pulls; must end with `/` when set. |
| `DOCKER_PUSH_DOMAIN` | *(empty)* | Host for `make model-images-push` (no trailing slash). |
| `CORP_CA_CERT_URL` | *(empty)* | URL to a PEM CA cert to inject during image builds. |
| `GOPROXY` | *(empty)* | Go module proxy URL; path must match `REGISTRY_VENDOR` when both are set. |
| `REGISTRY_GO_CHECK_URL` | *(empty)* | Required in the shell when `REGISTRY_USER` / `REGISTRY_TOKEN` are set (`make registry-auth-check`). |

</details>

---

## 📦 First Run

1. **Set a master key.** Edit `docker/env/.env` and change `MEILI_MASTER_KEY` to any non-empty string.

2. **Configure your content root.** Set `CONTENT_ROOT_BASE` to the directory that contains your local repos/doc folders. On Windows use forward slashes: `C:/repos`.

3. **Build and start:**

   ```
   docker compose -f docker/compose.yml up --build
   ```

   First build downloads model weights. Expect several minutes on first run.

4. **Configure at least one bundle.** See [03-bundles.md](03-bundles.md) to point the ingest job at your Markdown folders.

5. **Run an initial ingest:**

   ```
   docker compose -f docker/compose.yml run --rm ingest-job --project=<your-project-id> --mode=full
   ```

6. **Verify Meilisearch embedded chat is enabled.** The ingest job now enables Meilisearch experimental `chatCompletions` automatically.

    ```
    curl -s http://localhost:7700/experimental-features \
       -H "Authorization: Bearer <MEILI_MASTER_KEY>"
    ```

    Confirm the response includes:

    ```json
    {
       "chatCompletions": true
    }
    ```

---

## 🔍 Verify the Stack

Check that all services are healthy:

```
docker compose -f docker/compose.yml ps
```

All services should show `healthy` or `running`. Key health endpoints:

| Service | Endpoint |
| --- | --- |
| embed-server | `http://localhost:8082/health` |
| localai | `http://localhost:8080/readyz` |
| meilisearch | `http://localhost:7700/health` |
| ingest-api | `http://localhost:8090/healthz` |
| mcp-server | `http://localhost:3000/health` |
