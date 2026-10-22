# /// script
# requires-python = ">=3.11"
# ///
"""Build the rag-api image with podman using a portable temp context directory."""

import argparse
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument("--registry-proxy", default="")
parser.add_argument("--corp-ca-cert-url", default="")
parser.add_argument("--goproxy", default="")
parser.add_argument("--gonosumcheck", default="")
parser.add_argument("--goprivate", default="")
parser.add_argument("--gonosumdb", default="")
parser.add_argument("--package-registry-host", default="")
args = parser.parse_args()

user = os.environ.get("REGISTRY_USER", "").strip()
if "@" in user:
    user = user.split("@")[0]
token = os.environ.get("REGISTRY_TOKEN", "").strip()

root = pathlib.Path.cwd()
ctx = pathlib.Path(tempfile.mkdtemp(prefix="pctx-"))

try:
    def _ignore_binaries(_src: str, names: list[str]) -> list[str]:
        return [n for n in names if n.endswith((".exe", ".test"))]

    shutil.copytree(root / "go", ctx / "go", ignore=_ignore_binaries)

    certs_src = root / "docker" / "certs"
    if certs_src.exists():
        shutil.copytree(certs_src, ctx / "docker" / "certs")
    else:
        (ctx / "docker").mkdir(parents=True, exist_ok=True)

    build_args = [
        "--build-arg", f"DOCKER_REGISTRY_PROXY={args.registry_proxy}",
        "--build-arg", f"CORP_CA_CERT_URL={args.corp_ca_cert_url}",
        "--build-arg", f"GOPROXY={args.goproxy}",
        "--build-arg", f"GONOSUMCHECK={args.gonosumcheck}",
        "--build-arg", f"GOPRIVATE={args.goprivate}",
        "--build-arg", f"GONOSUMDB={args.gonosumdb}",
        "--build-arg", f"REGISTRY_USER={user}",
        "--build-arg", f"REGISTRY_TOKEN={token}",
        "--build-arg", f"PACKAGE_REGISTRY_HOST={args.package_registry_host}",
    ]

    result = subprocess.run(
        ["podman", "build", "-f", str(root / "docker" / "ingest-api.Dockerfile"), "-t", "docker_rag-api"]
        + build_args
        + [str(ctx)],
    )
    sys.exit(result.returncode)
finally:
    shutil.rmtree(ctx, ignore_errors=True)
