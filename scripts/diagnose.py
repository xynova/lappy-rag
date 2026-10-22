# /// script
# requires-python = ">=3.11"
# ///
"""Call /diagnostics and pretty-print results."""

import json
import sys
import time
import http.client
import urllib.error
import urllib.request

API_URL = "http://localhost:8090"

print("Waiting for ingest-api...")
for _ in range(30):
    try:
        urllib.request.urlopen(f"{API_URL}/healthz", timeout=2)
        break
    except Exception:
        time.sleep(2)
else:
    print("Error: ingest-api not reachable after 60s.", file=sys.stderr)
    sys.exit(1)

data = None
last_error = ""
for _ in range(10):
    try:
        with urllib.request.urlopen(f"{API_URL}/diagnostics", timeout=120) as resp:
            data = json.loads(resp.read())
            break
    except urllib.error.HTTPError as exc:
        data = json.loads(exc.read())
        break
    except (urllib.error.URLError, TimeoutError, http.client.RemoteDisconnected) as exc:
        last_error = str(exc)
        time.sleep(2)

if data is None:
    print(f"Error: could not fetch diagnostics: {last_error}", file=sys.stderr)
    sys.exit(1)

icons = {"ok": "OK  ", "info": "INFO", "warn": "WARN"}
hard_fail = False
for check in data["checks"]:
    status = check.get("status", "")
    icon = icons.get(status, "FAIL")
    if status not in {"ok", "info", "warn"}:
        hard_fail = True
    print(f"{icon}  [{check['latency_ms']}ms]  {check['name']} - {check['message']}")

if hard_fail:
    print("One or more diagnostic checks failed.", file=sys.stderr)
    sys.exit(1)
