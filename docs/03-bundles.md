# Bundles

A **bundle** is a directory of YAML files. Each file defines one project to ingest: where its files live, how to chunk them, and what Meilisearch index to write to.

## What You'll Learn

**Getting Started:**
- [Bundle Structure](#-bundle-structure): directories and loading order
- [Project File Format](#-project-file-format): all fields explained
- [Chunking Strategies](#-chunking-strategies): how content is split

**Implementation:**
- [Creating Your First Bundle](#-creating-your-first-bundle): step-by-step
- [Sharing Bundles Across a Team](#-sharing-bundles-across-a-team): git-based sharing
- [One Repo, Different Areas](user-journeys/same-repo-different-areas.md): split behavior by project/index

---

## 🏷️ Bundle Structure

A bundle is a directory. Each `.yaml` or `.yml` file inside it defines one project. All directories under `configs/bundles/` are loaded automatically — no configuration needed.

```
configs/bundles/
  shared.example/         <- example files (not loaded at runtime)
  team-a/                 <- clone a shared team config repo here
  personal/               <- your own local projects (gitignored)
```

The compose file mounts `configs/` into containers at `/app/configs/`. Both `ingest-api` and `ingest-job` auto-detect `/app/configs/bundles/` on startup. To override the path when running `ingest-job` directly, use the `--projects` flag with a comma-separated list of bundle directories.

Duplicate project IDs across bundles are rejected at load time.

---

## 🏷️ Project File Format

Each YAML file defines one project.

<details>
<summary><strong>📋 Full project file example</strong> (click to expand)</summary>

```yaml
id: my-project
host_path: ${CONTENT_ROOT_BASE}/my-project   # host path (used outside Docker)
content_path: /content/projects/my-project   # container path (used inside Docker)
index_name: docs_my_project
chat:
  workspace: chat_my_project
  source: openAi
  base_url: http://mcp-server:3000/localai-proxy/v1
  model: gemma-4-12b
  prompts:
    system: You are a helpful assistant for this project.
rules:
  - match_path_prefix: src/
    doc_type: code
    chunk_strategy: code_markdown
  - match_path_prefix: sops/
    doc_type: sop
    chunk_strategy: markdown
  - match_path_prefix: mesh/
    doc_type: confluence
    chunk_strategy: markdown
default:
  doc_type: generic
  chunk_strategy: markdown
enabled: true
```

</details>

#### 🏷️ Identity fields

| Field | Required | Notes |
| --- | --- | --- |
| `id` | Yes | Unique across all loaded bundles. Used as the `--project` flag when running ingest. |
| `host_path` | Yes | Absolute path on your machine. Supports `${CONTENT_ROOT_BASE}` substitution. |
| `content_path` | Yes | Path inside the Docker container. Must be under `/content/projects/`. |
| `index_name` | Yes | Meilisearch index to write chunks into. |
| `chat.workspace` | No | Chat workspace UID to configure per project. |
| `chat.source` | No | Provider source. Typical value: `openAi`. |
| `chat.base_url` | No | Provider base URL used by Meili chat workspace. |
| `chat.api_key` | No | Provider API key for Meili workspace settings. |
| `chat.model` | No | Model used for chat completions for this project. |
| `enabled` | Yes | Set to `false` to skip the project without removing the file. |

#### 🔗 Chat workspace settings

Use `chat.*` fields to configure the Meili chat workspace through the bundle itself. At query time, the routed project supplies workspace, provider, model, and prompt settings so different projects can have different chat behavior.

`chat.prompts` supports:

- `system`
- `search_description`
- `search_q_param`
- `search_filter_param`
- `search_index_uid_param`

#### ⚙️ Rules

Rules are evaluated top-to-bottom. The first matching `match_path_prefix` wins. If no rule matches, the `default` strategy applies.

| Field | Notes |
| --- | --- |
| `match_path_prefix` | File path prefix relative to `content_path`. |
| `doc_type` | Label stored with each chunk. Used for filtering. |
| `chunk_strategy` | Controls how the file is split. See strategies below. |

---

## ⚙️ Chunking Strategies

| Strategy | Best for |
| --- | --- |
| `markdown` | General Markdown files. Splits on headings. |
| `code_markdown` | Source code files with embedded Markdown comments. |
| `full_document` | Small files that should not be split. Indexes the whole file as one chunk. |

---

## 📦 Creating Your First Bundle

1. Copy the example bundle:

   ```
   cp -r configs/bundles/shared.example configs/bundles/personal
   ```

2. Edit or replace the example YAML files. Set `host_path` to your local folder and give the project a unique `id`.

3. Set `CONTENT_ROOT_BASE` in `docker/env/.env` to the root directory that contains your projects. For example, if your project lives at `C:/repos/my-docs`, set `CONTENT_ROOT_BASE=C:/repos`.

4. Run an ingest to populate the index:

   ```
   docker compose -f docker/compose.yml run --rm ingest-job --project=<id> --mode=full
   ```

---

## � Sharing Bundles Across a Team

A bundle directory can be a standalone git repository. Team members clone it into their `configs/bundles/` directory and add its path to `PROJECTS_CONFIG_PATH`. Project files use `${CONTENT_ROOT_BASE}` so paths resolve correctly on any OS.

```
# Windows
CONTENT_ROOT_BASE=C:/repos

# macOS / Linux
CONTENT_ROOT_BASE=/home/<you>/repos
```
