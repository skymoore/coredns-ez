# syntax=docker/dockerfile:1
#
# Builds CoreDNS v1.14.7 with every default plugin plus the out-of-tree
# plugins in this repo: dns-update-persistent, ixfr, admin (after file),
# secondary-persistent (after secondary). A small CoreDNS patch lets the
# DoH listener multiplex the admin UI and /api/v1 onto the same 443 port.
#
# Build from the repository root:
#   docker build -f integration-test/Dockerfile .

ARG GO_IMAGE=golang:1.26
ARG NODE_IMAGE=node:22-bookworm-slim
ARG COREDNS_VERSION=v1.14.7
ARG DEBIAN_IMAGE=debian:bookworm-slim

# -----------------------------------------------------------------------------
# Admin UI (embedded into the admin plugin).
# -----------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS ui
WORKDIR /ui
COPY admin/ui/package.json admin/ui/package-lock.json ./
RUN npm ci
COPY admin/ui ./
RUN npm run build

# -----------------------------------------------------------------------------
# Compile CoreDNS with both plugins.
# -----------------------------------------------------------------------------
FROM ${GO_IMAGE} AS build
ARG COREDNS_VERSION
ENV CGO_ENABLED=0 \
    GOTOOLCHAIN=auto \
    GOFLAGS=-buildvcs=false \
    GOPROXY=https://proxy.golang.org,direct

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
RUN git clone --depth 1 --branch "${COREDNS_VERSION}" https://github.com/coredns/coredns.git

WORKDIR /plugins
COPY go.mod go.sum ./
COPY dns-update-persistent ./dns-update-persistent
COPY ixfr ./ixfr
COPY secondary-persistent ./secondary-persistent
COPY admin ./admin
COPY --from=ui /ui/dist ./admin/ui/dist
COPY internal ./internal
COPY patches ./patches

WORKDIR /src/coredns
# plugin.cfg order is the request chain. These two must sit next to the
# in-tree plugins they replace rather than at the end (COREDNS_PLUGINS
# would append them after whoami/forward, which is the wrong slot).
RUN sed -i \
    -e '/^file:file$/a dns-update-persistent:github.com/skymoore/coredns-ez/dns-update-persistent' \
    plugin.cfg \
    && sed -i \
    -e '/^dns-update-persistent:/a ixfr:github.com/skymoore/coredns-ez/ixfr' \
    plugin.cfg \
    && sed -i \
    -e '/^ixfr:/a admin:github.com/skymoore/coredns-ez/admin' \
    plugin.cfg \
    && sed -i \
    -e '/^transfer:transfer$/a qstat:github.com/skymoore/coredns-ez/admin' \
    plugin.cfg \
    && sed -i \
    -e '/^secondary:secondary$/a secondary-persistent:github.com/skymoore/coredns-ez/secondary-persistent' \
    plugin.cfg \
    && git apply /plugins/patches/coredns-http-handler.patch \
    && printf '\nreplace github.com/skymoore/coredns-ez => /plugins\n' >> go.mod \
    && printf '\nreplace github.com/coredns/coredns => /src/coredns\n' >> /plugins/go.mod

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go generate coredns.go \
    && go get \
    && go build -tags grpcnotrace \
        -ldflags="-s -w -X github.com/coredns/coredns/coremain.GitCommit=integration-${COREDNS_VERSION}" \
        -o /out/coredns .

# -----------------------------------------------------------------------------
# Runtime image used by primary and secondary.
# -----------------------------------------------------------------------------
FROM ${DEBIAN_IMAGE} AS coredns
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /var/lib/coredns /etc/coredns /zones

COPY --from=build /out/coredns /usr/bin/coredns
COPY integration-test/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh /usr/bin/coredns

# Integration tests rewrite bind-mounted persist files; run as root so the
# volume owner does not have to match a distroless uid.
EXPOSE 53 53/udp 8080 8443 9153
ENTRYPOINT ["/entrypoint.sh"]
CMD ["-conf", "/etc/coredns/Corefile"]

# -----------------------------------------------------------------------------
# Test runner: dig, nsupdate, bash.
# -----------------------------------------------------------------------------
FROM ${DEBIAN_IMAGE} AS tester
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bind9-dnsutils \
        bash \
        ca-certificates \
        curl \
        grep \
        gawk \
        coreutils \
        iproute2 \
        python3 \
        sqlite3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /scripts
COPY integration-test/scripts /scripts
RUN chmod 0755 /scripts/*.sh /scripts/*.py
ENTRYPOINT ["/scripts/run-tests.sh"]
