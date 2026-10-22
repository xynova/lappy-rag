# Bundle example

This directory is a template showing the structure of a project bundle.
It is **not loaded at runtime** — it exists only as a reference.

## What is a bundle?

A bundle is a directory of per-project YAML files.
Each file defines one project to ingest, including its path rules and default chunking strategy.
Multiple bundles can be loaded together via a comma-separated `PROJECTS_CONFIG_PATH`.

## Recommended setup

1. Copy one of the example files here as a starting point.
2. Create your own bundle directory alongside this one, for example:
   ```
   configs/bundles/team-a/       ← clone a shared team config repo here
   configs/bundles/personal/     ← your own local project files (gitignored)
   ```
3. Set `PROJECTS_CONFIG_PATH` in your `.env` file to point at your bundles:
   ```
   PROJECTS_CONFIG_PATH=/app/configs/bundles/team-a,/app/configs/bundles/personal
   ```
4. On Windows set `CONTENT_ROOT_BASE=C:/repos`, on macOS set it to `/Users/<you>/repos`.

## Sharing bundles across a team

A bundle directory can be a standalone git repository.
Team members clone it and add it to their `PROJECTS_CONFIG_PATH`.
Project files reference paths via `${CONTENT_ROOT_BASE}` so they work on any OS.

## Project file format

```yaml
id: my-project
host_path: ${CONTENT_ROOT_BASE}/my-project   # local path (used outside Docker)
content_path: /content/projects/my-project   # container path (used inside Docker)
index_name: docs_my_project
rules:
  - match_path_prefix: src/
    doc_type: code
    chunk_strategy: code_markdown
default:
  doc_type: generic
  chunk_strategy: markdown
enabled: true
```
