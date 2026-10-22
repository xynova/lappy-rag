# /// script
# requires-python = ">=3.11"
# ///
"""Enable Meilisearch experimental embedding feature."""

import json
import os
import pathlib
import sys
import time
import urllib.request

MEILI_URL = "http://localhost:7700"


def load_master_key() -> str:
    """Load MEILI_MASTER_KEY from env or docker/env/.env file.

    Returns:
        The master key string, or empty string if not found.
    """
    key = os.environ.get("MEILI_MASTER_KEY", "").strip()
    if key:
        return key
    env_file = pathlib.Path("docker/env/.env")
    if not env_file.exists():
        return ""
    for line in env_file.read_text().splitlines():
        if line.startswith("MEILI_MASTER_KEY="):
            return line[len("MEILI_MASTER_KEY="):].strip()
    return ""


def get_features(master_key: str) -> dict[str, object]:
    """Fetch current experimental features from Meilisearch.

    Args:
        master_key: Meilisearch master key for authorization.

    Returns:
        Parsed JSON response as a dictionary.
    """
    req = urllib.request.Request(
        f"{MEILI_URL}/experimental-features",
        headers={"Authorization": f"Bearer {master_key}"},
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        return json.loads(resp.read())


def patch_features(master_key: str, payload: dict[str, object]) -> None:
    """Enable experimental features in Meilisearch via PATCH.

    Args:
        master_key: Meilisearch master key for authorization.
        payload: Feature flags to enable, e.g. {"chatCompletions": True}.
    """
    data = json.dumps(payload).encode()
    req = urllib.request.Request(
        f"{MEILI_URL}/experimental-features",
        data=data,
        method="PATCH",
        headers={
            "Authorization": f"Bearer {master_key}",
            "Content-Type": "application/json",
        },
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        resp.read()


master_key = load_master_key()
if not master_key:
    print("Error: MEILI_MASTER_KEY is required (set env var or create docker/env/.env).", file=sys.stderr)
    sys.exit(1)

features = None
for _ in range(30):
    try:
        features = get_features(master_key)
        break
    except Exception:
        time.sleep(1)

if features is None:
    print(f"Error: Meilisearch experimental-features endpoint not reachable at {MEILI_URL}.", file=sys.stderr)
    sys.exit(1)

if "chatCompletions" in features:
    payload = {"chatCompletions": True, "compositeEmbedders": True}
    feature_name = "chatCompletions+compositeEmbedders"
elif "compositeEmbedders" in features:
    payload = {"compositeEmbedders": True}
    feature_name = "compositeEmbedders"
else:
    print("Error: no supported embedding experimental feature key found in Meilisearch response.", file=sys.stderr)
    sys.exit(1)

patch_features(master_key, payload)
print(f"Enabled meilisearch experimental feature: {feature_name}")
