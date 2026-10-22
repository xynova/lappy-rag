# Agents

Humans read [README.md](README.md). This product is lappy-rag.

**Load these skills before you operate or extend this module:**

1. [ai-copilots/BOOTSTRAP.md](ai-copilots/BOOTSTRAP.md) (wire, not operate)
2. [ai-copilots/skills/lappy-rag-operator/SKILL.md](ai-copilots/skills/lappy-rag-operator/SKILL.md)

## Wire host discovery

Skills ship under `ai-copilots/`.
IF `.cursor/skills/lappy-rag-operator/SKILL.md` is missing, execute
[ai-copilots/BOOTSTRAP.md](ai-copilots/BOOTSTRAP.md) in **wire-only** mode
before you operate (Phase 0 then Phase 3 then Phase 4).
Read skills by path under `ai-copilots/` even when links are absent.
First commit in a clone: `make hooks-install` (Lefthook plus wire check). That does not replace operate-time wire-if-missing.
MUST NOT treat BOOTSTRAP as only a before-first-commit step.
