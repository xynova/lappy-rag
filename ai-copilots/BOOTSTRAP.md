# BOOTSTRAP — lappy-rag ai-copilots

**Audience:** Any AI agent in a workspace that checks out this product repository.

**Goal:** Wire host IDE discovery to canonical content under `ai-copilots/`.
MUST NOT copy skill bodies unless symlinks fail and the user approves copy fallback.

**Module path:** `github.com/xynova/lappy-rag` (Go code under `go/`; harness at git root).

---

## When to run

| Trigger | Mode | Phases |
|---------|------|--------|
| Missing IDE discovery links (operate) | **Wire only** | 0 → 3 → 4 |
| User asks to refresh harness content | **Refresh content + wire** | 0 → 1 → 3 → 4 |
| First commit in clone (with hooks) | Wire + `make hooks-install` | 0 → 3 → 4 (+ hooks) |

IF discovery links for the current IDE are missing → run wire-only **before** operate.

---

## Phase 0 — Resolve repository root

```bash
ROOT="$(git rev-parse --show-toplevel)"
test -d "$ROOT/ai-copilots" || { echo "missing ai-copilots under $ROOT"; exit 1; }
echo "Repo root: $ROOT"
```

---

## Phase 1 — Refresh content (optional)

Edit only files under `$ROOT/ai-copilots/`. Load author-ai-copilots and agent-smith when authoring skills.

---

## Phase 2 — Ask IDE and OS if unknown

1. IDE: Cursor, GitHub Copilot, Claude Code, Codex
2. OS: macOS/Linux symlink vs Windows junction/copy
3. Workspace: product repo root (this checkout)

---

## Phase 3 — Wire discovery (Cursor)

From the **repository root**:

```bash
ROOT="$(git rev-parse --show-toplevel)"
mkdir -p "$ROOT/.cursor/skills"
ln -snf "$ROOT/ai-copilots/skills/lappy-rag-operator" "$ROOT/.cursor/skills/lappy-rag-operator"
```

---

## Phase 4 — Verify

```bash
ROOT="$(git rev-parse --show-toplevel)"
test -f "$ROOT/.cursor/skills/lappy-rag-operator/SKILL.md"
test -f "$ROOT/ai-copilots/BOOTSTRAP.md"
ls -la "$ROOT/.cursor/skills/lappy-rag-operator"
```
