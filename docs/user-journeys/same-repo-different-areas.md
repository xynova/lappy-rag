# User Journey: One Repo, Different Areas, Different Behavior

Use this pattern when one repository contains different content domains (for example, SOP docs and engineering docs) and you want different retrieval and chat behavior.

## Goal

Point multiple bundle projects at the same repository path, but separate them by rules, index, and chat workspace intent.

## Why This Works

- Different `rules` let each project focus on specific path prefixes.
- Different `index_name` values keep chunk data and search settings isolated.
- Different chat workspace settings can use different system prompts and model behavior.

In this setup, platform defaults are fixed: `source=openAi`, `base_url=http://localai:8080/v1`, and a default model. You normally only vary workspace and prompt, and override model only when needed.

## Recommended Setup

1. Create one project per area in `configs/bundles`.
2. Keep the same `paths.volume.host` and `paths.volume.container` if they point to the same repo.
3. Change `index_name` for each area.
4. Scope each project with `rules` and `default`.
5. Route chat queries to the matching index/workspace.

## Example

```yaml
# Project A: SOP-focused
id: mesh-docs-sop
paths:
  volume:
    host: ${CONTENT_ROOT_BASE}/sops-docs
    container: /content/projects/sops-docs
index_name: docs_mesh_docs_sop
chat:
  workspace: chat_mesh_docs_sop
  prompts:
    system: You are a SOP-focused assistant. Prioritize procedure accuracy.
allowed_extensions:
  - .md
rules:
  - match_path_prefix: sops/
    doc_type: sop
    chunk_strategy: markdown
default:
  doc_type: generic
  chunk_strategy: markdown
enabled: true
```

```yaml
# Project B: Engineering-focused
id: mesh-docs-engineering
paths:
  volume:
    host: ${CONTENT_ROOT_BASE}/sops-docs
    container: /content/projects/sops-docs
index_name: docs_mesh_docs_engineering
chat:
  workspace: chat_mesh_docs_engineering
  prompts:
    system: You are an engineering assistant. Focus on implementation details.
allowed_extensions:
  - .md
rules:
  - match_path_prefix: src/
    doc_type: code
    chunk_strategy: code_markdown
  - match_path_prefix: mesh/
    doc_type: confluence
    chunk_strategy: confluence
default:
  doc_type: generic
  chunk_strategy: markdown
enabled: true
```

## Guardrails

- Do not share the same `index_name` across projects if you want different behavior.
- Keep project `id` values unique across all loaded bundles.
- If chat workspaces are configured per project, keep workspace names unique and aligned with index intent.
- Treat `source`, `base_url`, and `model` as platform defaults unless you have a deliberate reason to override them.

Optional override pattern:

```yaml
chat:
  workspace: chat_mesh_docs_engineering
  model: gemma-4-12b
  prompts:
    system: You are an engineering assistant. Focus on implementation details.
```
