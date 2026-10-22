ARG DOCKER_REGISTRY_PROXY=""
ARG PIP_INDEX_URL=""
ARG PIP_TRUSTED_HOST=""

# Pull model files from the scratch data image at build time (no shell needed)
FROM ${DOCKER_REGISTRY_PROXY}xynova/bge-base-model:bge-base-en-v1.5 AS model-data

FROM ${DOCKER_REGISTRY_PROXY}python:3.12-slim

ARG PIP_INDEX_URL
ARG PIP_TRUSTED_HOST
ARG CORP_CA_CERT_URL=

# Bake model into the image — eliminates model-init + shared volume complexity
COPY --from=model-data /bge-base-en-v1.5 /model

WORKDIR /app

# Optional corporate CA: fetch only when CORP_CA_CERT_URL is set at build time.
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && if [ -n "${CORP_CA_CERT_URL}" ]; then \
        curl --insecure -o /tmp/corp-ca-bundle.pem "${CORP_CA_CERT_URL}" \
        && csplit -s -z -f /usr/local/share/ca-certificates/corp-ca- /tmp/corp-ca-bundle.pem '/-----BEGIN CERTIFICATE-----/' '{*}' \
        && for f in /usr/local/share/ca-certificates/corp-ca-*; do mv "$f" "$f.crt"; done \
        && rm /tmp/corp-ca-bundle.pem \
        && update-ca-certificates; \
    fi

ENV PIP_INDEX_URL=${PIP_INDEX_URL} \
    PIP_TRUSTED_HOST=${PIP_TRUSTED_HOST} \
    REQUESTS_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt

RUN pip install --no-cache-dir \
    fastapi \
    "uvicorn[standard]" \
    sentence-transformers

COPY server.py .
