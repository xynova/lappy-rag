# /// script
# requires-python = ">=3.11"
# ///
"""Optional check that GOPROXY matches corporate registry when registry credentials are set."""

import inspect
import os
import subprocess
import sys

ALLOWED_VENDORS = frozenset({"artifactory", "nexus"})


def first_goproxy_url() -> str:
    goproxy = os.environ.get("GOPROXY", "").strip()
    if not goproxy:
        result = subprocess.run(["go", "env", "GOPROXY"], capture_output=True, text=True)
        if result.returncode != 0:
            print(
                f"Error: failed to run 'go env GOPROXY': {result.stderr.strip()}",
                file=sys.stderr,
            )
            sys.exit(1)
        goproxy = result.stdout.strip()
    return goproxy.split(",")[0].strip()


def validate_vendor_shape(vendor: str, first_url: str) -> None:
    if not first_url or first_url in {"direct", "off"}:
        return
    if vendor == "artifactory" and "/artifactory/" not in first_url:
        print(
            "Error: REGISTRY_VENDOR=artifactory but GOPROXY first entry "
            f"does not include '/artifactory/'. Current: {first_url}",
            file=sys.stderr,
        )
        sys.exit(1)
    if vendor == "nexus" and "/repository/" not in first_url:
        print(
            "Error: REGISTRY_VENDOR=nexus but GOPROXY first entry "
            f"does not include '/repository/'. Current: {first_url}",
            file=sys.stderr,
        )
        sys.exit(1)


user = os.environ.get("REGISTRY_USER", "").strip()
token = os.environ.get("REGISTRY_TOKEN", "").strip()
package_host = os.environ.get("PACKAGE_REGISTRY_HOST", "").strip()
vendor = os.environ.get("REGISTRY_VENDOR", "").strip().lower()

if not user or not token:
    print("skip: REGISTRY_USER/REGISTRY_TOKEN unset (no corporate GOPROXY check)")
    sys.exit(0)

if not package_host:
    msg = inspect.cleandoc(
        "Error: PACKAGE_REGISTRY_HOST is required when REGISTRY_USER and "
        "REGISTRY_TOKEN are set."
    )
    print(msg, file=sys.stderr)
    sys.exit(1)

if vendor:
    if vendor not in ALLOWED_VENDORS:
        print(
            f"Error: REGISTRY_VENDOR must be 'artifactory' or 'nexus'. Current: {vendor!r}",
            file=sys.stderr,
        )
        sys.exit(1)
    validate_vendor_shape(vendor, first_goproxy_url())

result = subprocess.run(["go", "env", "GOPROXY"], capture_output=True, text=True)
if result.returncode != 0:
    print(f"Error: failed to run 'go env GOPROXY': {result.stderr.strip()}", file=sys.stderr)
    sys.exit(1)

proxy = result.stdout.strip()
if package_host not in proxy:
    msg = inspect.cleandoc(
        f"Error: GOPROXY must include PACKAGE_REGISTRY_HOST ({package_host!r}). "
        f"Current: {proxy}"
    )
    print(msg, file=sys.stderr)
    sys.exit(1)

print(f"GOPROXY OK: {proxy}")
