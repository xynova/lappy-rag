# Native LocalAI (Advanced)

## What You'll Learn

**Getting Started:**
- [Why bother?](#-why-bother): when native outperforms Docker
- [Platform support](#-platform-support): Linux and macOS only — Windows path explained

**Implementation:**
- [Download LocalAI](#-download-localai): grab the right binary for your platform
- [Install the llama-cpp backend](#-install-the-llama-cpp-backend): one command, correct variant auto-selected
- [Wire into the stack](#-wire-into-the-stack): point compose at your native LocalAI instead of the container

**Advanced Usage:**
- [GPU acceleration](#-gpu-acceleration): CUDA, Metal, ROCm variant selection
- [Corporate proxy](#-corporate-proxy): getting `backends install` through TLS inspection / Artifactory

---

## 💡 Why bother?

The default Docker-based setup runs LocalAI inside a Linux container. On Linux this adds negligible overhead, but on macOS and Windows the story differs.

**macOS (Apple Silicon):** Docker runs inside a Linux VM. The VM has no access to the Metal GPU — inference falls back to CPU. Running LocalAI natively gives the llama-cpp backend direct access to the unified memory and Metal GPU, which typically doubles or triples token throughput on M-series chips.

**Windows:** LocalAI has no native Windows binary. WSL 2 is required regardless. CUDA via WSL 2 works but carries ~5–15% overhead compared to a bare Linux install. There is no meaningful advantage over the container path on Windows, so this guide does not apply there.

**Linux (x86-64 / arm64):** Docker overhead is ~1–3%. The main reason to go native on Linux is custom compilation (e.g. to target a specific CUDA version or ROCm build) or to avoid Docker altogether on a headless server.

---

## ⚙️ Platform support

| Platform | Native binary | GPU acceleration | Notes |
|---|---|---|---|
| Linux x86-64 | ✅ | CUDA 12/13, ROCm, Vulkan, Intel SYCL | Pre-built ELF via Releases |
| Linux arm64 | ✅ | NVIDIA Jetson (L4T) | Pre-built ELF via Releases |
| macOS Apple Silicon | ✅ | Metal | Pre-built Mach-O or `.dmg` |
| macOS Intel | ✅ (CPU only) | none | Pre-built Mach-O |
| Windows | ❌ | — | No native binary; use WSL 2 + Linux path |

---

## 📦 Download LocalAI

Go to the [LocalAI releases page](https://github.com/mudler/LocalAI/releases/latest) and download the binary for your platform.

**Linux x86-64:**
```bash
curl -Lo local-ai https://github.com/mudler/LocalAI/releases/latest/download/local-ai-Linux-x86_64
chmod +x local-ai
sudo mv local-ai /usr/local/bin/
```

**macOS Apple Silicon:**
```bash
curl -Lo local-ai https://github.com/mudler/LocalAI/releases/latest/download/local-ai-Darwin-arm64
chmod +x local-ai
sudo mv local-ai /usr/local/bin/
```

Verify:
```bash
local-ai --version
```

---

## 📦 Install the llama-cpp backend

`local-ai backends install` reads [`backend/index.yaml`](https://github.com/mudler/LocalAI/blob/master/backend/index.yaml), auto-detects your hardware, selects the correct pre-compiled OCI image from `quay.io/go-skynet/local-ai-backends`, and extracts the binary into `~/.local/share/local-ai/backends/` (or the path set by `BACKENDS_PATH`).

```bash
local-ai backends install llama-cpp
```

The capability mapping it uses:

| Host condition | Backend installed |
|---|---|
| CPU only (default) | `latest-cpu-llama-cpp` |
| NVIDIA CUDA 12 | `latest-gpu-nvidia-cuda-12-llama-cpp` |
| NVIDIA CUDA 13 | `latest-gpu-nvidia-cuda-13-llama-cpp` |
| Apple Silicon (Metal) | `latest-metal-darwin-arm64-llama-cpp` |
| AMD ROCm | `latest-gpu-rocm-hipblas-llama-cpp` |
| Intel SYCL | `latest-gpu-intel-sycl-f16-llama-cpp` |
| Vulkan | `latest-gpu-vulkan-llama-cpp` |

No compilation happens. You are downloading a pre-compiled binary that the LocalAI project built and published.

---

## 🔗 Wire into the stack

Once LocalAI is running natively, you have two options for the compose stack:

**Option A — remove the localai service from compose and run it directly:**

Start LocalAI manually pointing at the model files:
```bash
MODELS_PATH=./docker/localai/models \
LOCALAI_DISABLE_GALLERY=true \
local-ai run --address 0.0.0.0:8080
```

Then comment out the `localai:` service in `docker/compose.yml` and update `LOCALAI_PORT` in your `.env` to match. The MCP server and ingest services connect to `localhost:8080` regardless of whether that port is served by Docker or a native process.

**Option B — keep compose but override the image:**

Not recommended; the compose `localai` service bakes models into the image at build time. Mixing a native binary with the Dockerised model paths adds unnecessary complexity.

---

## ⚙️ GPU acceleration

No extra flags are needed at runtime — the backend installed by `local-ai backends install llama-cpp` already contains the correct accelerated binary for your detected hardware. LocalAI loads it automatically.

To verify which backend was installed:
```bash
local-ai backends list
```

If detection chose the wrong variant (e.g. you want ROCm but CPU was selected), force it:
```bash
local-ai backends install llama-cpp --capability amd
```

Valid capability values: `default`, `nvidia`, `nvidia-cuda-12`, `nvidia-cuda-13`, `metal`, `amd`, `intel`, `vulkan`.

---

## ⚙️ Corporate proxy

`local-ai backends install` pulls from `quay.io` via standard OCI image pull. If your environment blocks direct internet:

**Via HTTP proxy** (corporate egress):
```bash
HTTP_PROXY=http://proxy.example.com:8080 \
HTTPS_PROXY=http://proxy.example.com:8080 \
NO_PROXY=localhost,127.0.0.1,.internal.example.com,artifactory.example.com \
local-ai backends install llama-cpp
```

**Via private registry quay.io mirror** (Artifactory or Nexus):

Add to `/etc/containers/registries.conf` (Linux) or `~/.config/containers/registries.conf` (macOS). Artifactory example:
```toml
[[registry]]
prefix = "quay.io"
location = "artifactory.example.com/<your-quay-remote-repo>"
insecure = false
```

Nexus orgs often use a connector host instead, for example `nexus.example.com:8082` (see `DOCKER_REGISTRY_PROXY` in `.env.example`).

Then run `local-ai backends install llama-cpp` without proxy env vars when the mirror is configured.
