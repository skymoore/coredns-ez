#!/bin/sh
# Install a skymoore/coredns-plugins release on Alpine Linux (OpenRC).
#
# Latest release:
#   curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-plugins/main/scripts/install-alpine.sh | sudo sh
# Pin a version and start:
#   curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-plugins/main/scripts/install-alpine.sh | sudo START=1 VERSION=v1.14.7 sh
#
# Does not overwrite an existing Corefile, unbound.conf (once seeded), or
# /etc/conf.d/coredns. Re-run after every binary upgrade so
# cap_net_bind_service is restored.
set -eu

REPO="${REPO:-skymoore/coredns-plugins}"
PREFIX="${PREFIX:-/usr/local}"
CONF_DIR="${CONF_DIR:-/etc/coredns}"
LIB_DIR="${LIB_DIR:-/var/lib/coredns}"
USER_NAME="${USER_NAME:-coredns}"
BIND_CAP="${BIND_CAP:-cap_net_bind_service=+ep}"
UNBOUND_PORT="${UNBOUND_PORT:-5353}"
INSTALLER="https://raw.githubusercontent.com/${REPO}/main/scripts/install-alpine.sh"

die() { printf '%s\n' "$*" >&2; exit 1; }

need_root() {
	[ "$(id -u)" -eq 0 ] || die "run as root"
}

need_alpine() {
	command -v apk >/dev/null 2>&1 || die "Alpine apk not found"
}

github_curl() {
	if [ -n "${GITHUB_TOKEN:-}" ]; then
		curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "$@"
	else
		curl -fsSL "$@"
	fi
}

# Prefer VERSION= (or VERSION=latest). Otherwise the newest GitHub Release.
resolve_version() {
	if [ -n "${VERSION:-}" ] && [ "$VERSION" != "latest" ]; then
		printf '%s' "$VERSION"
		return
	fi
	tag=""
	final=$(github_curl -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest" || true)
	tag=${final##*/}
	case "$tag" in
	v[0-9]*)
		printf '%s' "$tag"
		return
		;;
	esac
	body=$(github_curl "https://api.github.com/repos/${REPO}/releases/latest") \
		|| die "could not fetch latest release for ${REPO}"
	tag=$(printf '%s\n' "$body" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$tag" ] || die "no GitHub Releases on ${REPO} (set VERSION=vX.Y.Z)"
	printf '%s' "$tag"
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
	apk add --no-cache ca-certificates curl libcap-utils unbound
}

ensure_user() {
	if ! getent passwd "$USER_NAME" >/dev/null; then
		adduser -D -H -s /sbin/nologin "$USER_NAME"
	fi
}

ensure_dirs() {
	mkdir -p "$CONF_DIR/zones" "$CONF_DIR/tls" "$CONF_DIR/keys" \
		"$LIB_DIR/admin-zones" /etc/unbound/unbound.conf.d
	chown -R "$USER_NAME:$USER_NAME" "$CONF_DIR" "$LIB_DIR"
}

install_binary() {
	goarch="$(arch)"
	base="coredns_${VERSION#v}_linux_${goarch}"
	url="https://github.com/${REPO}/releases/download/${VERSION}/${base}.tgz"
	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	github_curl "$url" -o "$tmp/${base}.tgz"
	if github_curl "$url.sha256" -o "$tmp/${base}.tgz.sha256"; then
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

# Recursion for RFC1918 / ULA / loopback / CGNAT only. Listens on UNBOUND_PORT
# (default 5353) so CoreDNS can keep :53. Private clients may query this port
# directly; CoreDNS forwards "." here for recursion on :53.
write_unbound() {
	conf=/etc/unbound/unbound.conf
	if [ -f "$conf" ] && grep -q 'coredns-plugins' "$conf"; then
		printf 'keep existing %s\n' "$conf"
		return
	fi
	if [ -f "$conf" ]; then
		cp -a "$conf" "${conf}.apk-dist"
		printf 'backed up %s to %s.apk-dist\n' "$conf" "$conf"
	fi

	anchor=""
	if [ -f /usr/share/dnssec-root/trusted-key.key ]; then
		anchor='	trust-anchor-file: "/usr/share/dnssec-root/trusted-key.key"'
	elif [ -f /etc/unbound/root.key ]; then
		anchor='	auto-trust-anchor-file: "/etc/unbound/root.key"'
	fi
	hints=""
	if [ -f /etc/unbound/root.hints ]; then
		hints='	root-hints: "/etc/unbound/root.hints"'
	fi

	cat >"$conf" <<EOF
# Seeded by coredns-plugins install-alpine.sh.
# Validating recursive resolver. Not an open resolver: only private,
# loopback, link-local, and CGNAT clients are allowed.
# Port ${UNBOUND_PORT} so CoreDNS can bind :53 and forward "." here.
server:
	verbosity: 1
	username: "unbound"
	directory: "/etc/unbound"
	chroot: ""
	pidfile: "/run/unbound.pid"
	num-threads: 1
	do-daemonize: no
	use-syslog: yes

	interface: 127.0.0.1@${UNBOUND_PORT}
	interface: ::1@${UNBOUND_PORT}
	interface: 0.0.0.0@${UNBOUND_PORT}
	interface: ::0@${UNBOUND_PORT}

	do-ip4: yes
	do-ip6: yes
	do-udp: yes
	do-tcp: yes

	access-control: 0.0.0.0/0 refuse
	access-control: ::0/0 refuse
	access-control: 127.0.0.0/8 allow
	access-control: ::1 allow
	access-control: ::ffff:127.0.0.1 allow
	access-control: 10.0.0.0/8 allow
	access-control: 172.16.0.0/12 allow
	access-control: 192.168.0.0/16 allow
	access-control: 169.254.0.0/16 allow
	access-control: 100.64.0.0/10 allow
	access-control: fc00::/7 allow
	access-control: fe80::/10 allow

	hide-identity: yes
	hide-version: yes
	harden-glue: yes
	harden-dnssec-stripped: yes
	prefetch: yes
	prefetch-key: yes
	qname-minimisation: yes
	do-not-query-localhost: no
${hints}
${anchor}

remote-control:
	control-enable: no

include-toplevel: "/etc/unbound/unbound.conf.d/*.conf"
EOF
	if ! unbound-checkconf "$conf" >/dev/null; then
		if [ -f "${conf}.apk-dist" ]; then
			mv "${conf}.apk-dist" "$conf"
		fi
		die "unbound-checkconf failed for $conf"
	fi
	printf 'seeded %s (recursion on :%s from private IPs)\n' "$conf" "$UNBOUND_PORT"
}

write_corefile() {
	corefile="${CONF_DIR}/Corefile"
	if [ -f "$corefile" ]; then
		printf 'keep existing %s\n' "$corefile"
		return
	fi
	cat >"$corefile" <<EOF
. {
	errors
	log
	admin {
		db ${LIB_DIR}/admin.sqlite
		data ${LIB_DIR}/admin-zones
		role primary
		bootstrap_admin admin
	}
	forward . 127.0.0.1:${UNBOUND_PORT}
	cache
}

# Add an https://.:443 block (and tls) to serve the admin UI on DoH.
EOF
	chown "$USER_NAME:$USER_NAME" "$corefile"
	chmod 640 "$corefile"
	printf 'seeded %s (forwards recursion to unbound :%s)\n' "$corefile" "$UNBOUND_PORT"
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
# export COREDNS_ADMIN_BOOTSTRAP_PASSWORD=''
# export COREDNS_OIDC_CLIENT_SECRET=''
EOF
		chown "$USER_NAME:$USER_NAME" "$confd"
		chmod 640 "$confd"
	fi
}

enable_service() {
	rc-update add unbound default 2>/dev/null || true
	rc-update add coredns default 2>/dev/null || true
	if [ "${START:-}" = "1" ]; then
		rc-service unbound restart || rc-service unbound start
		rc-service coredns restart || rc-service coredns start
	fi
}

need_root
need_alpine
VERSION=$(resolve_version)
export VERSION
ensure_pkgs
ensure_user
ensure_dirs
write_unbound
write_corefile
install_binary
write_openrc
enable_service
printf 'installed %s to %s/bin/coredns\n' "$VERSION" "$PREFIX"
printf 'Corefile: %s/Corefile  unbound: /etc/unbound/unbound.conf\n' "$CONF_DIR"
printf 'unbound recursion: UDP/TCP :%s from private IPs (RFC1918, ULA, loopback, CGNAT)\n' "$UNBOUND_PORT"
printf 'Set COREDNS_ADMIN_BOOTSTRAP_PASSWORD in /etc/conf.d/coredns before the first start.\n'
printf 'To start now: curl -fsSL %s | START=1 sh\n' "$INSTALLER"
