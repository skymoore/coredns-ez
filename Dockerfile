# syntax=docker/dockerfile:1
#
# Production image: CoreDNS plus this repo's plugins (admin UI embedded).
# Runtime is Alpine. Build from the repository root:
#   docker build -t ghcr.io/skymoore/coredns-ez:local .
#
# UI is on http://0.0.0.0:8080 (CoreDNS https transport without tls = plain HTTP).
# DNS is :53. Recursion is off. Persist /var/lib/coredns.

ARG GO_IMAGE=golang:1.26
ARG NODE_IMAGE=node:22-bookworm-slim
ARG COREDNS_VERSION=v1.14.7
ARG ALPINE_IMAGE=alpine:3.22

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
# Compile CoreDNS with the out-of-tree plugins and the HTTPHandler patch.
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
        -ldflags="-s -w -X github.com/coredns/coredns/coremain.GitCommit=docker-${COREDNS_VERSION}" \
        -o /out/coredns .

# -----------------------------------------------------------------------------
# Slim runtime. Non-root (coredns:65532). Port 53 via file cap; if bind
# fails, add --sysctl net.ipv4.ip_unprivileged_port_start=0.
# -----------------------------------------------------------------------------
FROM ${ALPINE_IMAGE}
RUN apk add --no-cache ca-certificates tzdata libcap-utils su-exec \
    && addgroup -S -g 65532 coredns \
    && adduser -S -D -H -u 65532 -G coredns -s /sbin/nologin coredns \
    && mkdir -p /var/lib/coredns/zones /etc/coredns/tls

COPY --from=build /out/coredns /usr/bin/coredns
COPY docker/Corefile /etc/coredns/Corefile
COPY --chmod=0755 docker/entrypoint.sh /entrypoint.sh

RUN setcap cap_net_bind_service=+ep /usr/bin/coredns \
    && getcap /usr/bin/coredns | grep -q cap_net_bind_service \
    && chown -R coredns:coredns /var/lib/coredns /etc/coredns \
    && chmod 0750 /var/lib/coredns /etc/coredns

ENV TZ=UTC
EXPOSE 53/udp 53/tcp 8080 9153
VOLUME ["/var/lib/coredns"]
WORKDIR /var/lib/coredns
USER coredns
HEALTHCHECK --interval=10s --timeout=3s --start-period=8s --retries=5 \
    CMD wget -qO- http://127.0.0.1:8080/api/v1/health >/dev/null || exit 1

ENTRYPOINT ["/entrypoint.sh"]
CMD ["-conf", "/etc/coredns/Corefile"]
