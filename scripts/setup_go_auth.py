# /// script
# requires-python = ">=3.11"
# ///
"""Write ~/.netrc for Go proxy authentication."""

import os
import pathlib
import subprocess
import sys
from urllib.parse import urlparse

PUBLIC_GO_HOSTS = frozenset({"proxy.golang.org", "goproxy.io"})


def resolve_registry_host() -> str:
    package_host = os.environ.get("PACKAGE_REGISTRY_HOST", "").strip()
    if package_host:
        return package_host

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

    first = goproxy.split(",")[0].strip()
    if first in {"", "direct", "off"}:
        print(
            "Error: PACKAGE_REGISTRY_HOST is required when credentials are set "
            "and GOPROXY is not a private registry URL.",
            file=sys.stderr,
        )
        sys.exit(1)

    url = first if "://" in first else f"https://{first}"
    host = urlparse(url).hostname
    if not host or host in PUBLIC_GO_HOSTS:
        print(
            "Error: PACKAGE_REGISTRY_HOST is required when credentials are set "
            "and GOPROXY points at a public Go proxy.",
            file=sys.stderr,
        )
        sys.exit(1)
    return host


user = os.environ.get("REGISTRY_USER", "").strip()
token = os.environ.get("REGISTRY_TOKEN", "").strip()

if not user or not token:
    print("skip: REGISTRY_USER/REGISTRY_TOKEN unset")
    sys.exit(0)

if "@" in user:
    user = user.split("@")[0]

machine = resolve_registry_host()
netrc = pathlib.Path.home() / ".netrc"
netrc.write_text(
    f"machine {machine}\nlogin {user}\npassword {token}\n",
    encoding="ascii",
)
if os.name != "nt":
    netrc.chmod(0o600)

print(f"Wrote Go auth file: {netrc}")
