# MCP Features

This document tracks planned MCP improvements and implementation priorities.

## Priority Order

1. Move MCP server implementation from Python to Go (Go-first).
2. Keep behavior parity with the existing MCP tools before adding new features.
3. Add admin workflows after parity (reload bundles, sync chat workspace settings, verify settings).

## Feature: Go-First MCP Migration

### Goal

Replace the Python MCP server with a Go MCP server while preserving existing tool behavior and wire protocol.

### Scope

- Implement the same externally visible tools currently exposed by the Python server.
- Preserve SSE transport endpoint behavior.
- Keep Meili chat integration behavior compatible with existing clients.
- Keep bundle-driven chat workspace config behavior.

### Out of Scope (for phase 1)

- New tool categories not present in current MCP server.
- UI changes in clients.

### Acceptance Criteria

- Existing MCP clients can connect without config changes.
- Existing tool names and request/response shapes are compatible.
- `chat_with_docs_meili` behavior is equivalent for successful and fallback paths.
- Health endpoint remains available.
- Rollback path exists (Python server can be re-enabled quickly).

## Feature: MCP Admin Sync Workflow (Post-Migration)

After Go parity, add an MCP tool that can:

1. Trigger bundle reload (`/projects/reload`).
2. Force workspace settings sync for a selected project/workspace.
3. Return current Meili workspace settings for verification.

## Notes

- Platform defaults remain: `source=openAi`, `base_url=http://localai:8080/v1`, and default model.
- Bundle chat config should mainly vary workspace and prompts; model override is optional.
