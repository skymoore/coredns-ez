#!/bin/sh
# Install or update a skymoore/coredns-ez release.
# Detects Alpine (OpenRC) vs Debian/Ubuntu (systemd).
#
#   curl -fsSL https://raw.githubusercontent.com/skymoore/coredns-ez/main/scripts/install.sh | sudo sh
#   curl -fsSL … | sudo START=1 VERSION=v1.14.7 sh
# Recursion: default on Alpine; on Debian/Ubuntu pass UNBOUND=1.
#
# Re-run to replace the binary in $LIB_DIR, refresh the OpenRC/systemd unit if
# it still points at $PREFIX/bin, restore cap_net_bind_service, and restart if
# CoreDNS is already running. Corefile and unbound.conf are left in place.
#
# The real binary lives in $LIB_DIR (owned by $USER_NAME) so Settings → Backup
# can read sqlite/zones/Corefile/tls and Settings → Update can swap the binary.
# systemd Restart=always / OpenRC supervise-daemon then start the new file with
# bind capability. $PREFIX/bin/coredns is a symlink for PATH.
set -eu

REPO="${REPO:-skymoore/coredns-ez}"
PREFIX="${PREFIX:-/usr/local}"
CONF_DIR="${CONF_DIR:-/etc/coredns}"
LIB_DIR="${LIB_DIR:-/var/lib/coredns}"
USER_NAME="${USER_NAME:-coredns}"
BIND_CAP="${BIND_CAP:-cap_net_bind_service=+ep}"
UNBOUND_PORT="${UNBOUND_PORT:-5353}"
INSTALLER="https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh"
OS=""
UPDATE=""
WAS_RUNNING=""
GENERATED_BOOTSTRAP=""
BOOTSTRAP_PW=""

die() { printf '%s\n' "$*" >&2; exit 1; }

need_root() { [ "$(id -u)" -eq 0 ] || die "run as root"; }

detect_os() {
	if command -v apk >/dev/null 2>&1; then
		OS=alpine
		return
	fi
	if [ -f /etc/os-release ]; then
		# shellcheck disable=SC1091
		. /etc/os-release
		case "${ID:-}" in
		debian | ubuntu)
			command -v apt-get >/dev/null || die "apt-get not found"
			OS=debian
			return
			;;
		esac
	fi
	die "Alpine (apk) or Debian/Ubuntu (apt) required"
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
	loongarch64) printf 'loong64' ;;
	*) die "unsupported arch: $(uname -m)" ;;
	esac
}

want_unbound() {
	if [ "${UNBOUND:-}" = "1" ]; then
		return 0
	fi
	if [ "${UNBOUND:-}" = "0" ]; then
		return 1
	fi
	[ "$OS" = alpine ]
}

already_installed() {
	[ -f "${CONF_DIR}/Corefile" ] || return 1
	[ -x "${LIB_DIR}/coredns" ] || [ -x "${PREFIX}/bin/coredns" ]
}

ensure_pkgs() {
	if [ "$OS" = alpine ]; then
		pkgs="ca-certificates curl libcap-utils"
		if want_unbound; then
			pkgs="$pkgs unbound"
		fi
		# shellcheck disable=SC2086
		apk add --no-cache $pkgs
		return
	fi
	pkgs="ca-certificates curl libcap2-bin"
	if want_unbound; then
		pkgs="$pkgs unbound"
	fi
	DEBIAN_FRONTEND=noninteractive apt-get update -qq
	# shellcheck disable=SC2086
	DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $pkgs
}

ensure_user() {
	if getent passwd "$USER_NAME" >/dev/null; then
		return
	fi
	if [ "$OS" = alpine ]; then
		adduser -D -H -s /sbin/nologin "$USER_NAME"
	else
		useradd --system --no-create-home --shell /usr/sbin/nologin "$USER_NAME"
	fi
}

ensure_dirs() {
	mkdir -p "$CONF_DIR/zones" "$CONF_DIR/tls" "$CONF_DIR/keys" "$LIB_DIR/zones"
	chown -R "$USER_NAME:$USER_NAME" "$CONF_DIR" "$LIB_DIR"
	chmod 0750 "$CONF_DIR" "$LIB_DIR"
}

install_binary() {
	goarch="$(arch)"
	base="coredns_${VERSION#v}_linux_${goarch}"
	url="https://github.com/${REPO}/releases/download/${VERSION}/${base}.tgz"
	tmp="$(mktemp -d)"
	github_curl "$url" -o "$tmp/${base}.tgz"
	if github_curl "$url.sha256" -o "$tmp/${base}.tgz.sha256"; then
		(cd "$tmp" && sha256sum -c "${base}.tgz.sha256")
	fi
	tar -xzf "$tmp/${base}.tgz" -C "$tmp"
	[ -x "$tmp/coredns" ] || die "archive missing coredns binary"
	install -o "$USER_NAME" -g "$USER_NAME" -m 0755 "$tmp/coredns" "${LIB_DIR}/coredns"
	rm -rf "$tmp"
	setcap "$BIND_CAP" "${LIB_DIR}/coredns"
	getcap "${LIB_DIR}/coredns" | grep -q cap_net_bind_service \
		|| die "setcap failed; is libcap installed?"
	mkdir -p "${PREFIX}/bin"
	if [ -e "${PREFIX}/bin/coredns" ] && [ ! -L "${PREFIX}/bin/coredns" ]; then
		rm -f "${PREFIX}/bin/coredns"
	fi
	ln -sfn "${LIB_DIR}/coredns" "${PREFIX}/bin/coredns"
	"${LIB_DIR}/coredns" -version
}

lan_view() {
	cat <<EOF
	view lan {
		expr incidr(client_ip(), '10.0.0.0/8') || incidr(client_ip(), '172.16.0.0/12') || incidr(client_ip(), '192.168.0.0/16') || incidr(client_ip(), '127.0.0.0/8') || incidr(client_ip(), '100.64.0.0/10') || incidr(client_ip(), '169.254.0.0/16') || incidr(client_ip(), '::1/128') || incidr(client_ip(), 'fc00::/7') || incidr(client_ip(), 'fe80::/10')
	}
EOF
}

write_unbound() {
	want_unbound || return 0
	conf=/etc/unbound/unbound.conf
	if [ -f "$conf" ] && grep -qE 'coredns-ez|coredns-plugins' "$conf"; then
		printf 'keep existing %s\n' "$conf"
		return
	fi
	mkdir -p /etc/unbound/unbound.conf.d
	if [ -f "$conf" ]; then
		cp -a "$conf" "${conf}.dist"
	fi
	anchor=""
	if [ -f /usr/share/dnssec-root/trusted-key.key ]; then
		anchor='	trust-anchor-file: "/usr/share/dnssec-root/trusted-key.key"'
	elif [ -f /etc/unbound/root.key ]; then
		anchor='	auto-trust-anchor-file: "/etc/unbound/root.key"'
	elif [ -f /usr/share/dns/root.key ]; then
		anchor='	auto-trust-anchor-file: "/usr/share/dns/root.key"'
	elif [ -f /var/lib/unbound/root.key ]; then
		anchor='	auto-trust-anchor-file: "/var/lib/unbound/root.key"'
	fi
	cat >"$conf" <<EOF
# Seeded by coredns-ez install.sh.
server:
	verbosity: 1
	username: "unbound"
	directory: "/etc/unbound"
	chroot: ""
	interface: 127.0.0.1@${UNBOUND_PORT}
	interface: ::1@${UNBOUND_PORT}
	interface: 0.0.0.0@${UNBOUND_PORT}
	interface: ::0@${UNBOUND_PORT}
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
EOF
	if [ "$OS" = alpine ]; then
		printf '\ninclude-toplevel: "/etc/unbound/unbound.conf.d/*.conf"\n' >>"$conf"
		if command -v unbound-checkconf >/dev/null && ! unbound-checkconf "$conf" >/dev/null; then
			[ -f "${conf}.dist" ] && mv "${conf}.dist" "$conf"
			die "unbound-checkconf failed for $conf"
		fi
	else
		printf '\ninclude: "/etc/unbound/unbound.conf.d/*.conf"\n' >>"$conf"
	fi
	printf 'seeded %s (recursion on :%s from private IPs)\n' "$conf" "$UNBOUND_PORT"
}

# CoreDNS transfer treats every token after `to` as an address, including a
# closing `}` on the same line. Always write a block.
transfer_block() {
	indent=$1
	printf '%stransfer {\n%s\tto 127.0.0.1\n%s}\n' "$indent" "$indent" "$indent"
}

# Rewrite `transfer { to ... }` one-liners in an already-seeded Corefile.
fix_oneline_transfer() {
	corefile="${CONF_DIR}/Corefile"
	[ -f "$corefile" ] || return 0
	grep -q 'transfer { to ' "$corefile" || return 0
	tmp="$(mktemp)"
	awk '
		{
			if ($0 ~ /transfer \{ to .*\}/) {
				indent = $0
				sub(/transfer.*/, "", indent)
				line = $0
				sub(/^[[:space:]]*transfer \{ to /, "", line)
				sub(/\}[[:space:]]*$/, "", line)
				gsub(/[[:space:]]+$/, "", line)
				print indent "transfer {"
				print indent "\tto " line
				print indent "}"
			} else {
				print
			}
		}
	' "$corefile" >"$tmp"
	cat "$tmp" >"$corefile"
	rm -f "$tmp"
	chown "$USER_NAME:$USER_NAME" "$corefile"
	chmod 640 "$corefile"
	printf 'rewrote one-line transfer blocks in %s (} was parsed as an AXFR address)\n' "$corefile"
}

write_corefile() {
	corefile="${CONF_DIR}/Corefile"
	if [ -f "$corefile" ]; then
		printf 'keep existing %s\n' "$corefile"
		fix_oneline_transfer
		return
	fi
	if want_unbound; then
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
	errors
	log
	admin
	forward . 127.0.0.1:${UNBOUND_PORT}
	cache
$(transfer_block "	")
}
. {
	errors
	log
	admin
$(transfer_block "	")
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
$(transfer_block "	")
}
EOF
	fi
	chown "$USER_NAME:$USER_NAME" "$corefile"
	chmod 640 "$corefile"
	printf 'seeded %s\n' "$corefile"
}

service_layout_ok() {
	if [ "$OS" = alpine ]; then
		[ -f /etc/init.d/coredns ] || return 1
		grep -q 'supervisor=supervise-daemon' /etc/init.d/coredns || return 1
		grep -q 'supervise_daemon_args' /etc/init.d/coredns || return 1
		grep -Fq "${LIB_DIR}/coredns" /etc/init.d/coredns
		return
	fi
	[ -f /etc/systemd/system/coredns.service ] || return 1
	grep -q '^Restart=always' /etc/systemd/system/coredns.service || return 1
	grep -Fq "${LIB_DIR}/coredns" /etc/systemd/system/coredns.service
}

stop_service() {
	if [ "$OS" = alpine ]; then
		rc-service coredns stop || true
	else
		systemctl stop coredns.service || true
	fi
}

ensure_logs() {
	touch /var/log/coredns.log /var/log/coredns.err
	chown "$USER_NAME:$USER_NAME" /var/log/coredns.log /var/log/coredns.err
	chmod 640 /var/log/coredns.log /var/log/coredns.err
}

write_openrc() {
	ensure_logs
	initd=/etc/init.d/coredns
	if [ -f "$initd" ] && grep -q 'supervisor=supervise-daemon' "$initd" && grep -Fq "${LIB_DIR}/coredns" "$initd" && grep -q 'supervise_daemon_args' "$initd"; then
		printf 'keep existing %s\n' "$initd"
	else
		if [ -f "$initd" ]; then
			cp -a "$initd" "${initd}.bak"
			printf 'refreshing %s so Settings → Update can replace %s/coredns\n' "$initd" "$LIB_DIR"
		fi
		cat >"$initd" <<EOF
#!/sbin/openrc-run

name="CoreDNS"
description="CoreDNS (skymoore/coredns-ez)"
supervisor=supervise-daemon
command="${LIB_DIR}/coredns"
command_args="-conf ${CONF_DIR}/Corefile"
command_user="${USER_NAME}:${USER_NAME}"
directory="${LIB_DIR}"
pidfile="/run/coredns.pid"
output_log="/var/log/coredns.log"
error_log="/var/log/coredns.err"
capabilities="^cap_net_bind_service"
respawn_delay=2
# supervise-daemon sanitizes the environment; conf.d values must be passed with --env.
supervise_daemon_args=""
[ -n "\${COREDNS_ADMIN_BOOTSTRAP_PASSWORD:-}" ] && supervise_daemon_args="\${supervise_daemon_args} --env COREDNS_ADMIN_BOOTSTRAP_PASSWORD=\${COREDNS_ADMIN_BOOTSTRAP_PASSWORD}"
[ -n "\${COREDNS_OIDC_CLIENT_SECRET:-}" ] && supervise_daemon_args="\${supervise_daemon_args} --env COREDNS_OIDC_CLIENT_SECRET=\${COREDNS_OIDC_CLIENT_SECRET}"
[ -n "\${GITHUB_TOKEN:-}" ] && supervise_daemon_args="\${supervise_daemon_args} --env GITHUB_TOKEN=\${GITHUB_TOKEN}"
[ -n "\${COREDNS_UPDATE_REPO:-}" ] && supervise_daemon_args="\${supervise_daemon_args} --env COREDNS_UPDATE_REPO=\${COREDNS_UPDATE_REPO}"

depend() {
	need net
	use logger unbound
	after unbound
	provide dns
}

start_pre() {
	if [ ! -f ${LIB_DIR}/admin.sqlite ] && [ -z "\${COREDNS_ADMIN_BOOTSTRAP_PASSWORD:-}" ]; then
		eerror "empty sqlite needs COREDNS_ADMIN_BOOTSTRAP_PASSWORD in /etc/conf.d/coredns"
		return 1
	fi
}
EOF
		chmod 755 "$initd"
	fi
	confd=/etc/conf.d/coredns
	if [ ! -f "$confd" ]; then
		cat >"$confd" <<'EOF'
# Sourced by OpenRC. supervise-daemon does not inherit these unless the
# init script passes --env (install.sh does). Still export so a manual
# `su coredns` run works.
# export COREDNS_ADMIN_BOOTSTRAP_PASSWORD=''
# export COREDNS_OIDC_CLIENT_SECRET=''
EOF
		chown "$USER_NAME:$USER_NAME" "$confd"
		chmod 640 "$confd"
	fi
}

write_systemd() {
	unit=/etc/systemd/system/coredns.service
	if [ -f "$unit" ] && grep -q '^Restart=always' "$unit" && grep -Fq "${LIB_DIR}/coredns" "$unit"; then
		printf 'keep existing %s\n' "$unit"
	else
		if [ -f "$unit" ]; then
			cp -a "$unit" "${unit}.bak"
			printf 'refreshing %s so Settings → Update can replace %s/coredns\n' "$unit" "$LIB_DIR"
		fi
		cat >"$unit" <<EOF
[Unit]
Description=CoreDNS (skymoore/coredns-ez)
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${USER_NAME}
Group=${USER_NAME}
WorkingDirectory=${LIB_DIR}
ExecStart=${LIB_DIR}/coredns -conf ${CONF_DIR}/Corefile
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
Restart=always
RestartSec=2
EnvironmentFile=-/etc/default/coredns

[Install]
WantedBy=multi-user.target
EOF
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

write_service() {
	if [ "$OS" = alpine ]; then
		write_openrc
		return
	fi
	write_systemd
}

service_active() {
	if [ "$OS" = alpine ]; then
		rc-service coredns status >/dev/null 2>&1
	else
		systemctl is-active --quiet coredns.service
	fi
}

enable_service() {
	if [ "$OS" = alpine ]; then
		if want_unbound; then
			rc-update add unbound default 2>/dev/null || true
		fi
		rc-update add coredns default 2>/dev/null || true
	else
		systemctl daemon-reload
		systemctl enable coredns.service
		if want_unbound; then
			systemctl enable unbound.service 2>/dev/null || true
		fi
	fi
}

restart_or_start() {
	run=0
	if [ "${START:-}" = "1" ]; then
		run=1
	fi
	if [ -n "$WAS_RUNNING" ]; then
		run=1
	fi
	[ "$run" = 1 ] || return 0
	if [ "$OS" = alpine ]; then
		if want_unbound; then
			rc-service unbound restart || rc-service unbound start
		fi
		rc-service coredns restart || rc-service coredns start
	else
		if want_unbound; then
			systemctl restart unbound.service || systemctl start unbound.service
		fi
		systemctl restart coredns.service || systemctl start coredns.service
	fi
	verify_running
}

# supervise-daemon returns before the child has proven it can stay up.
# BusyBox pgrep -x matches the full path (/var/lib/coredns/coredns), so it
# never sees a process named "coredns". pidof and the health URL do.
verify_running() {
	i=0
	while [ "$i" -lt 8 ]; do
		if curl -fsS -m 2 http://127.0.0.1:8080/api/v1/health >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 1
	done
	if pidof coredns >/dev/null 2>&1; then
		return 0
	fi
	printf 'CoreDNS did not stay running.\n' >&2
	if [ -s /var/log/coredns.err ]; then
		printf '--- /var/log/coredns.err ---\n' >&2
		tail -n 40 /var/log/coredns.err >&2
	fi
	if [ -s /var/log/coredns.log ]; then
		printf '--- /var/log/coredns.log ---\n' >&2
		tail -n 40 /var/log/coredns.log >&2
	fi
	die "check ${CONF_DIR}/Corefile and $(secret_hint); then rc-service coredns start / systemctl start coredns"
}

rand_password() {
	dd if=/dev/urandom bs=32 count=1 2>/dev/null | tr -dc 'A-Za-z0-9' | cut -c1-24
}

# Empty sqlite refuses to start without this. OpenRC only passes exported conf.d vars.
ensure_bootstrap_password() {
	if [ -f "${LIB_DIR}/admin.sqlite" ]; then
		return 0
	fi
	pw="${COREDNS_ADMIN_BOOTSTRAP_PASSWORD:-}"
	if [ "$OS" = alpine ]; then
		envf=/etc/conf.d/coredns
		if [ -z "$pw" ] && [ -f "$envf" ]; then
			pw=$(sed -n "s/^export COREDNS_ADMIN_BOOTSTRAP_PASSWORD=//p" "$envf" | tail -n 1)
			pw=$(printf '%s' "$pw" | sed "s/^['\"]//; s/['\"]$//")
		fi
		if [ -z "$pw" ]; then
			pw=$(rand_password)
			[ -n "$pw" ] || die "could not generate COREDNS_ADMIN_BOOTSTRAP_PASSWORD"
			GENERATED_BOOTSTRAP=1
		fi
		touch "$envf"
		if ! grep -q '^export COREDNS_ADMIN_BOOTSTRAP_PASSWORD=' "$envf"; then
			printf "export COREDNS_ADMIN_BOOTSTRAP_PASSWORD='%s'\n" "$pw" >>"$envf"
			chown "$USER_NAME:$USER_NAME" "$envf"
			chmod 640 "$envf"
		fi
	else
		envf=/etc/default/coredns
		if [ -z "$pw" ] && [ -f "$envf" ]; then
			pw=$(sed -n "s/^COREDNS_ADMIN_BOOTSTRAP_PASSWORD=//p" "$envf" | tail -n 1)
			pw=$(printf '%s' "$pw" | sed "s/^['\"]//; s/['\"]$//")
		fi
		if [ -z "$pw" ]; then
			pw=$(rand_password)
			[ -n "$pw" ] || die "could not generate COREDNS_ADMIN_BOOTSTRAP_PASSWORD"
			GENERATED_BOOTSTRAP=1
		fi
		touch "$envf"
		if ! grep -q '^COREDNS_ADMIN_BOOTSTRAP_PASSWORD=' "$envf"; then
			printf 'COREDNS_ADMIN_BOOTSTRAP_PASSWORD=%s\n' "$pw" >>"$envf"
			chown "$USER_NAME:$USER_NAME" "$envf"
			chmod 640 "$envf"
		fi
	fi
	BOOTSTRAP_PW=$pw
}

secret_hint() {
	if [ "$OS" = alpine ]; then
		printf '/etc/conf.d/coredns (use export VAR=)'
	else
		printf '/etc/default/coredns'
	fi
}

need_root
detect_os
if already_installed; then
	UPDATE=1
	if [ -x "${LIB_DIR}/coredns" ]; then
		printf 'existing install at %s/coredns\n' "$LIB_DIR"
		"${LIB_DIR}/coredns" -version 2>/dev/null || true
	else
		printf 'existing install at %s/bin/coredns\n' "$PREFIX"
		"${PREFIX}/bin/coredns" -version 2>/dev/null || true
	fi
fi
VERSION=$(resolve_version)
export VERSION
if [ -n "$UPDATE" ]; then
	printf 'updating to %s (config left in place)\n' "$VERSION"
else
	printf 'installing %s on %s\n' "$VERSION" "$OS"
fi
ensure_pkgs
ensure_user
ensure_dirs
if service_active; then
	WAS_RUNNING=1
fi
write_unbound
write_corefile
if [ -n "$WAS_RUNNING" ] && ! service_layout_ok; then
	printf 'stopping CoreDNS to migrate the service onto %s/coredns (required for Settings → Update)\n' "$LIB_DIR"
	stop_service
fi
write_service
install_binary
ensure_bootstrap_password
enable_service
restart_or_start
printf 'binary %s at %s/coredns (symlink %s/bin/coredns)\n' "$VERSION" "$LIB_DIR" "$PREFIX"
printf 'Corefile: %s/Corefile\n' "$CONF_DIR"
if [ -n "${GENERATED_BOOTSTRAP:-}" ] && [ -n "${BOOTSTRAP_PW:-}" ]; then
	printf 'Admin UI: http://<host>:8080  user admin  password %s (saved in %s)\n' "$BOOTSTRAP_PW" "$(secret_hint)"
else
	printf 'Admin UI: http://<host>:8080  user admin. Bootstrap password: %s\n' "$(secret_hint)"
fi
printf 'Settings → Backup and Settings → Update work against this layout.\n'
printf 'AXFR is localhost-only until you add secondary IPs in the UI.\n'
if want_unbound; then
	printf 'Unbound recursion on :%s from private IPs; CoreDNS :53 recurses only for those clients.\n' "$UNBOUND_PORT"
else
	printf 'Recursion is off. UNBOUND=1 to seed Unbound and a private view.\n'
fi
if [ -n "$UPDATE" ]; then
	printf 'Re-run this script to replace the binary. Running services were restarted if they were already up.\n'
else
	printf 'Start: curl -fsSL %s | START=1 sh\n' "$INSTALLER"
fi
