# /// script
# requires-python = ">=3.11"
# ///
"""Trigger a full re-ingest for one or all projects and wait for completion.

Usage:
    uv run scripts/reingest_full.py              # ingest all enabled projects
    uv run scripts/reingest_full.py <project-id> # ingest a single project
"""

import json
import sys
import time
import urllib.request

API_URL = "http://localhost:8090"


def call_api(method: str, path: str, body: dict[str, object] | None = None) -> dict[str, object]:
    data = json.dumps(body).encode() if body else None
    req = urllib.request.Request(
        f"{API_URL}{path}",
        data=data,
        method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())


try:
    call_api("GET", "/healthz")
except Exception as exc:
    print(f"Error: ingest-api not reachable at {API_URL}: {exc}", file=sys.stderr)
    sys.exit(1)

if len(sys.argv) >= 2 and sys.argv[1].strip():
    projects = [sys.argv[1].strip()]
else:
    all_projects: list[dict[str, object]] = call_api("GET", "/projects")  # type: ignore[assignment]
    projects = [str(p["id"]) for p in all_projects if p.get("enabled")]
    if not projects:
        print("No enabled projects found.", file=sys.stderr)
        sys.exit(1)
    print(f"Ingesting all enabled projects: {', '.join(projects)}")

try:
    call_api("GET", "/healthz")
except Exception as exc:
    print(f"Error: ingest-api not reachable at {API_URL}: {exc}", file=sys.stderr)
    sys.exit(1)

failed: list[str] = []

for project in projects:
    print(f"\n── {project} ──")
    job = call_api("POST", "/jobs/ingest", {"project_id": project, "mode": "full"})
    job_id = str(job.get("id", "")).strip()
    if not job_id:
        print(f"Error: ingest-api did not return a job id: {job}", file=sys.stderr)
        failed.append(project)
        continue

    print(f"Queued job: {job_id}")

    timed_out = True
    for _ in range(900):
        current = call_api("GET", f"/jobs/{job_id}")
        status = str(current.get("status", ""))
        print(f"status={status} changed={current.get('files_changed', 0)} indexed={current.get('chunks_indexed', 0)}")
        if status == "completed":
            print(json.dumps(current, indent=2))
            timed_out = False
            break
        if status == "failed":
            error_str = str(current.get("error", ""))
            print(json.dumps(current, indent=2))
            print(f"Error: ingest job failed: {error_str}", file=sys.stderr)
            failed.append(project)
            timed_out = False
            break
        time.sleep(2)

    if timed_out:
        print(f"Error: timed out waiting for {project}.", file=sys.stderr)
        failed.append(project)

if failed:
    print(f"\nFailed projects: {', '.join(failed)}", file=sys.stderr)
    sys.exit(1)
