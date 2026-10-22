ARG DOCKER_REGISTRY_PROXY=""

# ── Stage 1: pull BGE embedding model (scratch image, GGUF pre-built) ────────
FROM ${DOCKER_REGISTRY_PROXY}xynova/bge-base-model:latest AS bge-model

# ── Stage 2: pull Gemma4 chat model (scratch image, no shell) ────────────────
FROM ${DOCKER_REGISTRY_PROXY}xynova/gemma4-12b-model:latest AS gemma-model

# ── Stage 3: LocalAI runtime ──────────────────────────────────────────────────
# master-aio-cpu has a newer runtime with updated model/backend support.
# latest-cpu (v3.0.0) predates gemma4; latest-aio-cpu (v3.12.1) uses OCI-layer
# backends pulled at runtime from GitHub gallery — unusable in an air-gapped/proxy
# environment.
FROM ${DOCKER_REGISTRY_PROXY}localai/localai:master-aio-cpu

ARG CORP_CA_CERT_URL=

# Optional corporate CA for TLS inspection or private registries.
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && if [ -n "${CORP_CA_CERT_URL}" ]; then \
        curl --insecure -o /tmp/corp-ca-bundle.pem "${CORP_CA_CERT_URL}" \
        && csplit -s -z -f /tmp/corp-ca- /tmp/corp-ca-bundle.pem '/-----BEGIN CERTIFICATE-----/' '{*}' \
        && for f in /tmp/corp-ca-*; do \
            if grep -q 'BEGIN CERTIFICATE' "$f"; then \
                mv "$f" "/usr/local/share/ca-certificates/$(basename "$f").crt"; \
            else \
                rm -f "$f"; \
            fi; \
        done \
        && rm -f /tmp/corp-ca-bundle.pem \
        && update-ca-certificates; \
    fi

ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

# Preinstall llama-cpp backend at build time so runtime can keep gallery disabled.
RUN LOCALAI_DISABLE_GALLERY=false /local-ai backends install llama-cpp

# Bake both model artefacts into the image at build time.
# BGE: Q4_K_M GGUF at root of image → llama backend (embeddings: true in model yaml)
COPY --from=bge-model /bge-base-en-v1.5-q4_k_m.gguf /build/models/bge-base-en-v1.5-q4_k_m.gguf

# Gemma4: GGUF model + multimodal projector (vision support) → llama backend
COPY --from=gemma-model /gemma-4-12B-it-QAT-Q4_0.gguf /build/models/gemma-4-12B-it-QAT-Q4_0.gguf
COPY --from=gemma-model /mmproj-gemma-4-12B-it-QAT-BF16.gguf /build/models/mmproj-gemma-4-12B-it-QAT-BF16.gguf

# Copy LocalAI model configuration files (context is docker/, so source is localai/models/)
COPY localai/models/ /build/models/
