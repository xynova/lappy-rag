# syntax=docker/dockerfile:1
ARG DOCKER_REGISTRY_PROXY=
FROM ${DOCKER_REGISTRY_PROXY}golang:1.25-bookworm AS build

ARG CORP_CA_CERT_URL=
ARG GOPROXY=
ARG GONOSUMCHECK=*
ARG GOPRIVATE=
ARG GONOSUMDB=
ARG PACKAGE_REGISTRY_HOST=
ARG REGISTRY_USER=
ARG REGISTRY_TOKEN=

ENV GONOSUMCHECK=${GONOSUMCHECK} \
    GOPRIVATE=${GOPRIVATE} \
    GONOSUMDB=${GONOSUMDB}

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY docker/certs/ /tmp/certs/
RUN if [ -f /tmp/certs/corp-ca.crt ]; then \
        cp /tmp/certs/corp-ca.crt /usr/local/share/ca-certificates/corp-ca.crt \
        && update-ca-certificates; \
    elif [ -n "${CORP_CA_CERT_URL}" ]; then \
        curl -kfsSL -o /usr/local/share/ca-certificates/corp-ca.crt "${CORP_CA_CERT_URL}" \
        && update-ca-certificates; \
    fi

RUN REGISTRY_USER=$(printf '%s' "${REGISTRY_USER}" | tr -d '\r\n') && \
    REGISTRY_TOKEN=$(printf '%s' "${REGISTRY_TOKEN}" | tr -d '\r\n') && \
    PACKAGE_REGISTRY_HOST=$(printf '%s' "${PACKAGE_REGISTRY_HOST}" | tr -d '\r\n') && \
    if [ -n "$REGISTRY_USER" ] && [ -n "$REGISTRY_TOKEN" ]; then \
        if [ -z "$PACKAGE_REGISTRY_HOST" ]; then \
            echo "Error: PACKAGE_REGISTRY_HOST is required when REGISTRY_USER and REGISTRY_TOKEN are set." >&2; \
            exit 1; \
        fi; \
        REGISTRY_USER_LOGIN=${REGISTRY_USER%@*}; \
        printf '%s\n' 'package main' 'import ("fmt";"net/url";"os")' 'func main(){if len(os.Args)<2{return};fmt.Print(url.QueryEscape(os.Args[1]))}' > /tmp/urlencode.go; \
        ENC_USER_LOGIN=$(go run /tmp/urlencode.go "$REGISTRY_USER_LOGIN"); \
        ENC_TOKEN=$(go run /tmp/urlencode.go "$REGISTRY_TOKEN"); \
        rm -f /tmp/urlencode.go; \
        if [ -n "${GOPROXY}" ]; then \
            AUTH_GOPROXY=$(printf '%s' "${GOPROXY}" | sed "s#https://#https://${ENC_USER_LOGIN}:${ENC_TOKEN}@#"); \
            printf '%s' "${AUTH_GOPROXY}" > /tmp/auth_goproxy; \
        else \
            printf '' > /tmp/auth_goproxy; \
        fi; \
    else \
        printf '%s' "${GOPROXY}" > /tmp/auth_goproxy; \
    fi \
    && true

WORKDIR /src/go

COPY go/go.mod go/go.sum* ./
RUN set -e; \
    AUTH_GOPROXY=$(cat /tmp/auth_goproxy); \
    GO_MOD_ENV="GONOSUMCHECK=${GONOSUMCHECK} GOPRIVATE=${GOPRIVATE} GONOSUMDB=${GONOSUMDB}"; \
    if [ -n "$AUTH_GOPROXY" ]; then GO_MOD_ENV="$GO_MOD_ENV GOPROXY=$AUTH_GOPROXY"; fi; \
    if [ -n "${PACKAGE_REGISTRY_HOST}" ]; then GO_MOD_ENV="$GO_MOD_ENV GOINSECURE=${PACKAGE_REGISTRY_HOST}"; fi; \
    eval "$GO_MOD_ENV go mod download"

COPY go/cmd ./cmd
COPY go/internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/mcp-server ./cmd/mcp-server

FROM ${DOCKER_REGISTRY_PROXY}golang:1.25-bookworm
COPY --from=build /usr/local/share/ca-certificates/ /usr/local/share/ca-certificates/
RUN update-ca-certificates
WORKDIR /app
COPY --from=build /out/mcp-server /usr/local/bin/mcp-server
EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/mcp-server", "serve"]
