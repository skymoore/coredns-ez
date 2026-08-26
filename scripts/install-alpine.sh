#!/bin/sh
# Install a skymoore/coredns-plugins release on Alpine Linux (OpenRC).
# Does not overwrite an existing Corefile. Re-run after every binary upgrade
# so cap_net_bind_service is restored.
set -eu

VERSION="${VERSION:-v1.14.7}"
REPO="${REPO:-skymoore/coredns-plugins}"
PREFIX="${PREFIX:-/usr/local}"
CONF_DIR="${CONF_DIR:-/etc/coredns}"
LIB_DIR="${LIB_DIR:-/var/lib/coredns}"
USER_NAME="${USER_NAME:-coredns}"
BIND_CAP="${BIND_CAP:-cap_net_bind_service=+ep}"

die() { printf '%s\n' "$*" >&2; exit 1; }

need_root() {
	[ "$(id -u)" -eq 0 ] || die "run as root"
}

arch() {
	case "$(uname -m)" in
	x86_64 | amd64) printf 'amd64' ;;
	aarch64 | arm64) printf 'arm64' ;;
	armv7l | armv7) printf 'arm' ;;
	ppc64le) printf 'ppc64le' ;;
	s390x) printf 's390x' ;;
	riscv64) printf 'riscv64' ;;
	loongarch64) printf 'loong64' ;;
	*) die "unsupported arch: $(uname -m)" ;;
	esac
}

ensure_pkgs() {
	apk add --no-cache ca-certificates curl libcap-utils
}

ensure_user() {
	if ! getent passwd "$USER_NAME" >/dev/null; then
		adduser -D -H -s /sbin/nologin "$USER_NAME"
	fi
}

ensure_dirs() {
	mkdir -p "$CONF_DIR/zones" "$CONF_DIR/tls" "$CONF_DIR/keys" \
		"$LIB_DIR/admin-zones"
	chown -R "$USER_NAME:$USER_NAME" "$CONF_DIR" "$LIB_DIR"
}

install_binary() {
	goarch="$(arch)"
	base="coredns_${VERSION#v}_linux_${goarch}"
	url="https://github.com/${REPO}/releases/download/${VERSION}/${base}.tgz"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	curl -fsSL "$url" -o "$tmp/${base}.tgz"
	if curl -fsSL "$url.sha256" -o "$tmp/${base}.tgz.sha256"; then
		(cd "$tmp" && sha256sum -c "${base}.tgz.sha256")
	fi
	tar -xzf "$tmp/${base}.tgz" -C "$tmp"
	[ -x "$tmp/coredns" ] || die "archive missing coredns binary"
	install -m 0755 "$tmp/coredns" "$PREFIX/bin/coredns"
	setcap "$BIND_CAP" "$PREFIX/bin/coredns"
	getcap "$PREFIX/bin/coredns" | grep -q cap_net_bind_service \
		|| die "setcap failed; is libcap-utils installed?"
	"$PREFIX/bin/coredns" -version
}

write_openrc() {
	initd="/etc/init.d/coredns"
	if [ -f "$initd" ]; then
		printf 'keep existing %s\n' "$initd"
	else
		cat >"$initd" <<EOF
#!/sbin/openrc-run

name="CoreDNS"
description="CoreDNS DNS server"
command="${PREFIX}/bin/coredns"
command_args="-conf ${CONF_DIR}/Corefile"
command_user="${USER_NAME}:${USER_NAME}"
command_background=yes
pidfile="/run/coredns.pid"
capabilities="^cap_net_bind_service"

depend() {
	need net
	use logger unbound
	after unbound
	provide dns
}
EOF
		chmod 755 "$initd"
	fi

	confd="/etc/conf.d/coredns"
	if [ -f "$confd" ]; then
		printf 'keep existing %s\n' "$confd"
	else
		cat >"$confd" <<'EOF'
# OpenRC sources this file but does not export it. Prefix every secret with export.
# export TSIG_NODES_K8S=''
# export COREDNS_OIDC_CLIENT_SECRET=''
EOF
		chown "$USER_NAME:$USER_NAME" "$confd"
		chmod 640 "$confd"
	fi
}

enable_service() {
	rc-update add coredns default 2>/dev/null || true
	if [ "${START:-}" = "1" ]; then
		rc-service coredns restart || rc-service coredns start
	fi
}

need_root
ensure_pkgs
ensure_user
ensure_dirs
install_binary
write_openrc
enable_service
printf 'installed %s to %s/bin/coredns\n' "$VERSION" "$PREFIX"
printf 'Corefile: %s/Corefile  conf: /etc/conf.d/coredns\n' "$CONF_DIR"
printf 'START=1 %s to also start the service\n' "$0"
