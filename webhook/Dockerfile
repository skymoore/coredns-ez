# syntax=docker/dockerfile:1
#
# Static cert-manager DNS-01 webhook. Build from webhook/:
#   docker build -t ghcr.io/skymoore/coredns-ez-webhook:local .
#
ARG GO_IMAGE=golang:1.26-bookworm

FROM ${GO_IMAGE} AS build
WORKDIR /src
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=local \
    GOFLAGS=-buildvcs=false
COPY src/go.mod src/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY src/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/webhook .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/webhook /webhook
USER 65532:65532
ENTRYPOINT ["/webhook"]
