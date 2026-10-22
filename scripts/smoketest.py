# /// script
# requires-python = ">=3.11"
# ///
"""Quick health checks across all stack services."""

import json
import os
import pathlib
import sys
import urllib.request

ENV_FILE = pathlib.Path("docker/env/.env")


def load_env_file() -> dict[str, str]:
    """Load key-value pairs from docker/env/.env if present."""
    if not ENV_FILE.exists():
        return {}

    values: dict[str, str] = {}
    for raw_line in ENV_FILE.read_text().splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def get_env_value(name: str, default: str, env_file_values: dict[str, str]) -> str:
    """Read setting from process env first, then docker/env/.env, then default."""
    value = os.environ.get(name, "").strip()
    if value:
        return value
    file_value = env_file_values.get(name, "").strip()
    if file_value:
        return file_value
    return default


def build_urls(env_file_values: dict[str, str]) -> list[str]:
    """Build health check URLs using configured service ports."""
    localai_port = get_env_value("LOCALAI_PORT", "8080", env_file_values)
    meili_port = get_env_value("MEILI_PORT", "7700", env_file_values)
    ingest_port = get_env_value("INGEST_API_PORT", "8090", env_file_values)
    mcp_port = get_env_value("MCP_PORT", "3000", env_file_values)

    return [
        f"http://localhost:{localai_port}/readyz",
        f"http://localhost:{meili_port}/health",
        f"http://localhost:{ingest_port}/healthz",
        f"http://localhost:{mcp_port}/health",
    ]


def load_master_key() -> str:
    """Load MEILI_MASTER_KEY from env or docker/env/.env file.

    Returns:
        The master key string, or empty string if not found.
    """
    key = os.environ.get("MEILI_MASTER_KEY", "").strip()
    if key:
        return key
    env_values = load_env_file()
    key = env_values.get("MEILI_MASTER_KEY", "").strip()
    if not key:
        return ""
    return key


env_values = load_env_file()
for url in build_urls(env_values):
    try:
        urllib.request.urlopen(url, timeout=20)
        print(f"OK  {url}")
    except Exception as exc:
        print(f"FAIL {url}: {exc}", file=sys.stderr)
        sys.exit(1)

master_key = load_master_key()
if not master_key:
    print("Error: MEILI_MASTER_KEY is required (set env var or create docker/env/.env).", file=sys.stderr)
    sys.exit(1)

req = urllib.request.Request(
    "http://localhost:7700/experimental-features",
    headers={"Authorization": f"Bearer {master_key}"},
)
with urllib.request.urlopen(req, timeout=20) as resp:
    features = json.loads(resp.read())

chat_enabled = features.get("chatCompletions", False)
composite_enabled = features.get("compositeEmbedders", False)

if not (chat_enabled or composite_enabled):
    print(
        "Error: neither chatCompletions nor compositeEmbedders is enabled in Meilisearch experimental features.",
        file=sys.stderr,
    )
    sys.exit(1)

print(
    f"OK  meilisearch experimental embedding feature enabled: "
    f"chatCompletions={chat_enabled}, compositeEmbedders={composite_enabled}"
)
