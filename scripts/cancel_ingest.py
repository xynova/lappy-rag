# /// script
# requires-python = ">=3.11"
# ///
"""Cancel queued/running ingest-related Meilisearch tasks via ingest-api.

Usage:
    uv run scripts/cancel_ingest.py              # cancel for all enabled projects
    uv run scripts/cancel_ingest.py <project-id> # cancel for one project
"""

import json
import sys
import urllib.request

API_URL = "http://localhost:8090"


def call_api(method: str, path: str, body: dict[str, object] | None = None) -> dict[str, object]:
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(
        f"{API_URL}{path}",
        data=data,
        method=method,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.loads(resp.read())


try:
    call_api("GET", "/healthz")
except Exception as exc:
    print(f"Error: ingest-api not reachable at {API_URL}: {exc}", file=sys.stderr)
    sys.exit(1)

project_id = ""
if len(sys.argv) >= 2 and sys.argv[1].strip():
    project_id = sys.argv[1].strip()

payload: dict[str, object] = {}
if project_id:
    payload["project_id"] = project_id

try:
    result = call_api("POST", "/jobs/cancel", payload)
except Exception as exc:
    print(f"Error: cancel request failed: {exc}", file=sys.stderr)
    sys.exit(1)

print(json.dumps(result, indent=2))

failed = int(result.get("failed", 0))
if failed > 0:
    sys.exit(1)
