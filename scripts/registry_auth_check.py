# /// script
# requires-python = ">=3.11"
# ///
"""Verify private Go proxy access using credentials from environment."""

import os
import subprocess
import sys

user = os.environ.get("REGISTRY_USER", "").strip()
token = os.environ.get("REGISTRY_TOKEN", "").strip()

if not user or not token:
    print("skip: REGISTRY_USER/REGISTRY_TOKEN unset")
    sys.exit(0)

check_url = os.environ.get("REGISTRY_GO_CHECK_URL", "").strip()
if not check_url:
    print(
        "Error: REGISTRY_GO_CHECK_URL is required when credentials are set.",
        file=sys.stderr,
    )
    sys.exit(1)

if "@" in user:
    user = user.split("@")[0]

result = subprocess.run(
    [
        "curl",
        "-fsS",
        "-o",
        os.devnull,
        "-I",
        "-u",
        f"{user}:{token}",
        "--connect-timeout",
        "5",
        "--max-time",
        "10",
        check_url,
    ],
    capture_output=True,
)

if result.returncode != 0:
    print(
        f"Error: Registry auth check failed (curl exit {result.returncode}). "
        "Verify REGISTRY_USER (login only, no @domain), REGISTRY_TOKEN, "
        "REGISTRY_GO_CHECK_URL, and VPN/network.",
        file=sys.stderr,
    )
    sys.exit(1)

print("Registry auth OK.")
