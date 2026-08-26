#!/bin/sh
# Install a skymoore/coredns-ez release on Debian/Ubuntu (systemd).
#
#   curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-ez/main/scripts/install-systemd.sh | sudo sh
#   curl -fsSL … | sudo START=1 VERSION=v1.14.7 sh
# Recursion (Unbound :5353 + view lan): UNBOUND=1
#
# Does not overwrite an existing Corefile, unit, /etc/default/coredns, or
# unbound.conf (once seeded). Re-run after upgrades so cap_net_bind_service
# is restored.
set -eu

REPO="${REPO:-skymoore/coredns-ez}"
PREFIX="${PREFIX:-/usr/local}"
CONF_DIR="${CONF_DIR:-/etc/coredns}"
LIB_DIR="${LIB_DIR:-/var/lib/coredns}"
USER_NAME="${USER_NAME:-coredns}"
BIND_CAP="${BIND_CAP:-cap_net_bind_service=+ep}"
UNBOUND_PORT="${UNBOUND_PORT:-5353}"
INSTALLER="https://raw.githubusercontent.com/${REPO}/main/scripts/install-systemd.sh"

die() { printf '%s\n' "$*" >&2; exit 1; }

need_root() { [ "$(id -u)" -eq 0 ] || die "run as root"; }

need_debian() {
	[ -f /etc/os-release ] || die "not a Debian/Ubuntu system"
	# shellcheck disable=SC1091
	. /etc/os-release
	case "${ID:-}" in
	debian | ubuntu) ;;
	*) die "Debian or Ubuntu required (ID=${ID:-unknown})" ;;
	esac
	command -v apt-get >/dev/null || die "apt-get not found"
}

github_curl() {
	if [ -n "${GITHUB_TOKEN:-}" ]; then
		curl -fsSL -H "Authorization: Bearer ${GITHUB_TOKEN}" "$@"
	else
		curl -fsSL "$@"
	fi
}

resolve_version() {
	if [ -n "${VERSION:-}" ] && [ "$VERSION" != "latest" ]; then
		printf '%s' "$VERSION"
		return
	fi
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
	*) die "unsupported arch: $(uname -m)" ;;
	esac
}

ensure_pkgs() {
	pkgs="ca-certificates curl libcap2-bin"
	if [ "${UNBOUND:-}" = "1" ]; then
		pkgs="$pkgs unbound"
	fi
	# shellcheck disable=SC2086
	DEBIAN_FRONTEND=noninteractive apt-get update -qq
	# shellcheck disable=SC2086
	DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $pkgs
}

ensure_user() {
	if ! getent passwd "$USER_NAME" >/dev/null; then
		useradd --system --no-create-home --shell /usr/sbin/nologin "$USER_NAME"
	fi
}

ensure_dirs() {
	mkdir -p "$CONF_DIR/zones" "$CONF_DIR/tls" "$CONF_DIR/keys" "$LIB_DIR/zones"
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
		|| die "setcap failed; is libcap2-bin installed?"
	"$PREFIX/bin/coredns" -version
	trap - EXIT
	rm -rf "$tmp"
}

lan_view() {
	cat <<EOF
	view lan {
		expr incidr(client_ip(), '10.0.0.0/8') || incidr(client_ip(), '172.16.0.0/12') || incidr(client_ip(), '192.168.0.0/16') || incidr(client_ip(), '127.0.0.0/8') || incidr(client_ip(), '100.64.0.0/10') || incidr(client_ip(), '169.254.0.0/16') || incidr(client_ip(), '::1/128') || incidr(client_ip(), 'fc00::/7') || incidr(client_ip(), 'fe80::/10')
	}
EOF
}

write_unbound() {
	[ "${UNBOUND:-}" = "1" ] || return 0
	conf=/etc/unbound/unbound.conf
	if [ -f "$conf" ] && grep -qE 'coredns-ez|coredns-plugins' "$conf"; then
		printf 'keep existing %s\n' "$conf"
		return
	fi
	if [ -f "$conf" ]; then
		cp -a "$conf" "${conf}.dist"
	fi
	mkdir -p /etc/unbound/unbound.conf.d
	anchor=""
	if [ -f /usr/share/dns/root.key ]; then
		anchor='	auto-trust-anchor-file: "/usr/share/dns/root.key"'
	elif [ -f /var/lib/unbound/root.key ]; then
		anchor='	auto-trust-anchor-file: "/var/lib/unbound/root.key"'
	fi
	cat >"$conf" <<EOF
# Seeded by coredns-ez install-systemd.sh.
server:
	verbosity: 1
	interface: 127.0.0.1@${UNBOUND_PORT}
	interface: ::1@${UNBOUND_PORT}
	do-daemonize: no
	username: "unbound"
	directory: "/etc/unbound"
	chroot: ""
	access-control: 0.0.0.0/0 refuse
	access-control: ::0/0 refuse
	access-control: 127.0.0.0/8 allow
	access-control: ::1 allow
	access-control: 10.0.0.0/8 allow
	access-control: 172.16.0.0/12 allow
	access-control: 192.168.0.0/16 allow
	access-control: 169.254.0.0/16 allow
	access-control: 100.64.0.0/10 allow
	access-control: fc00::/7 allow
	access-control: fe80::/10 allow
	do-not-query-localhost: no
	hide-identity: yes
	hide-version: yes
${anchor}
include: "/etc/unbound/unbound.conf.d/*.conf"
EOF
	printf 'seeded %s\n' "$conf"
}

write_corefile() {
	corefile="${CONF_DIR}/Corefile"
	if [ -f "$corefile" ]; then
		printf 'keep existing %s\n' "$corefile"
		return
	fi
	if [ "${UNBOUND:-}" = "1" ]; then
		cat >"$corefile" <<EOF
https://.:8080 {
	errors
	log
	admin {
		db ${LIB_DIR}/admin.sqlite
		data ${LIB_DIR}/zones
		role primary
		bootstrap_admin admin
	}
}
. {
$(lan_view)
	admin
	forward . 127.0.0.1:${UNBOUND_PORT}
	cache
	transfer { to 127.0.0.1 }
}
. {
	admin
	transfer { to 127.0.0.1 }
}
EOF
	else
		cat >"$corefile" <<EOF
https://.:8080 {
	errors
	log
	admin {
		db ${LIB_DIR}/admin.sqlite
		data ${LIB_DIR}/zones
		role primary
		bootstrap_admin admin
	}
}
. {
	errors
	log
	admin
	transfer { to 127.0.0.1 }
}
EOF
	fi
	chown "$USER_NAME:$USER_NAME" "$corefile"
	chmod 640 "$corefile"
	printf 'seeded %s\n' "$corefile"
}

write_unit() {
	unit=/etc/systemd/system/coredns.service
	if [ -f "$unit" ]; then
		printf 'keep existing %s\n' "$unit"
	else
		cat >"$unit" <<EOF
[Unit]
Description=CoreDNS (skymoore/coredns-ez)
Documentation=https://github.com/skymoore/coredns-ez
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
Group=${USER_NAME}
ExecStart=${PREFIX}/bin/coredns -conf ${CONF_DIR}/Corefile
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
Restart=on-failure
RestartSec=2
EnvironmentFile=-/etc/default/coredns

[Install]
WantedBy=multi-user.target
EOF
		printf 'seeded %s\n' "$unit"
	fi
	envf=/etc/default/coredns
	if [ ! -f "$envf" ]; then
		cat >"$envf" <<'EOF'
# COREDNS_ADMIN_BOOTSTRAP_PASSWORD=
# COREDNS_OIDC_CLIENT_SECRET=
EOF
		chown "$USER_NAME:$USER_NAME" "$envf"
		chmod 640 "$envf"
	fi
}

enable_service() {
	systemctl daemon-reload
	systemctl enable coredns.service
	if [ "${UNBOUND:-}" = "1" ]; then
		systemctl enable unbound.service 2>/dev/null || true
	fi
	if [ "${START:-}" = "1" ]; then
		if [ "${UNBOUND:-}" = "1" ]; then
			systemctl restart unbound.service || systemctl start unbound.service
		fi
		systemctl restart coredns.service || systemctl start coredns.service
	fi
}

need_root
need_debian
VERSION=$(resolve_version)
export VERSION
ensure_pkgs
ensure_user
ensure_dirs
write_unbound
write_corefile
install_binary
write_unit
enable_service
printf 'installed %s to %s/bin/coredns\n' "$VERSION" "$PREFIX"
printf 'Corefile: %s/Corefile  unit: coredns.service\n' "$CONF_DIR"
printf 'Admin UI: http://<host>:8080  user admin. Set COREDNS_ADMIN_BOOTSTRAP_PASSWORD in /etc/default/coredns before the first start.\n'
printf 'AXFR is localhost-only until you add secondary IPs in the UI.\n'
if [ "${UNBOUND:-}" = "1" ]; then
	printf 'Unbound recursion on :%s from private IPs; CoreDNS :53 recurses only for those clients.\n' "$UNBOUND_PORT"
else
	printf 'Recursion is off. UNBOUND=1 to seed Unbound and a private view.\n'
fi
printf 'Start: curl -fsSL %s | START=1 sh\n' "$INSTALLER"
