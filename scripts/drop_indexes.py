# /// script
# requires-python = ">=3.11"
# ///
"""Delete all indexed documents for one or all enabled projects via ingest-api.

Usage:
    uv run scripts/drop_indexes.py              # drop all enabled project indexes
    uv run scripts/drop_indexes.py <project-id> # drop a single project index
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
    with urllib.request.urlopen(req, timeout=120) as resp:
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
    result = call_api("POST", "/jobs/drop", payload)
except Exception as exc:
    print(f"Error: drop request failed: {exc}", file=sys.stderr)
    sys.exit(1)

print(json.dumps(result, indent=2))
print("\nDrop initiated in background. Wait ~10 seconds, then run: make ingest")
