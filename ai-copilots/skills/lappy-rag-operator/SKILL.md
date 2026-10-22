---
name: lappy-rag-operator
description: Operate the lappy-rag local Markdown RAG stack (make serve, ingest, Go CLIs) from the product git root and go/ module.
---

# lappy-rag operator

**Moral:** Run inspect commands before mutating the stack. Never start daemons on bare binary invoke.

## When to load

- Operating or extending lappy-rag in this repository
- Docker compose, ingest, MCP, or Go services under `go/`

---

## Core constraints

**CONSTRAINT:** Quality and build from the product root use `make format`, `make lint`, `make vet`, `make test`, and `make build`. From `go/` use `go tool task format`, `lint`, `vet`, `test`, and `build`. MUST NOT call PATH `golangci-lint` or PATH `gofumpt`.

- Enforcement: Read Makefile and `go/Taskfile.yml` before running gates.
- Violation: STOP, use `make` or `go tool task` recipes only.

**CONSTRAINT:** The long-running stack is `make serve` and `make serve-down`. `make up` is an alias for `serve`. MUST NOT document `make up` as the only stack name.

- Enforcement: Operator docs and replies name `serve` first.
- Violation: STOP, correct to `make serve`.

**CONSTRAINT:** Inspect before execute: `make help`, `cd go && go tool task --list`, and `go run ./cmd/ingest-api` (agent guide). Execute only after: `make serve`, `ingest-job run --project <id>`.

- Enforcement: Do not run ingest or compose until inspect passes.
- Violation: STOP, run inspect commands.

**CONSTRAINT:** MUST NOT start HTTP servers by invoking `ingest-api`, `mcp-server`, or `ingest-job` with no subcommand.

- Enforcement: Bare binary must print the agent guide and exit 0.
- Violation: STOP, use `<bin> serve` or `ingest-job run`.

CORRECT:

```text
make serve
ingest-api serve
ingest-job run --project my-bundle
```

PROHIBITED:

```text
ingest-api
# starts listening with no subcommand
make up
# only documented name with no mention of serve
```

---

## Pre-completion checklist

- [ ] `AGENTS.md` and `ai-copilots/BOOTSTRAP.md` exist at git root
- [ ] `make help` lists `format` and `serve`
- [ ] `cd go && go run ./cmd/ingest-api` prints `AGENTS.md` and exits 0
