#!/usr/bin/env bash
# Prepare a CoreDNS source tree with this repo's plugins, or package one
# GOOS/GOARCH binary the same way coredns Makefile.release does.
set -euo pipefail

PLUGIN_MODULE="github.com/skymoore/coredns-ez"
COREDNS_REPO="https://github.com/coredns/coredns.git"
GOTAGS="${GOTAGS:-grpcnotrace}"

usage() {
  cat <<'EOF'
Usage:
  build-release.sh prepare --tag vX.Y.Z --plugins-dir DIR --out-dir DIR
  build-release.sh package --src-dir DIR --goos OS --goarch ARCH --version VER --gitcommit SHA --out-dir DIR

prepare  Clone CoreDNS, inject plugins after file/secondary, go generate, write a
         portable src tree (coredns/ + plugins/) with relative replace directives.
package  Cross-compile one platform and write coredns_<ver>_<os>_<arch>.tgz
         (and .zip on windows) plus sha256 sidecars.
EOF
}

log() { printf '%s\n' "$*"; }
die() { printf '%s\n' "$*" >&2; exit 1; }

github_output() {
  [[ -n "${GITHUB_OUTPUT:-}" ]] || return 0
  printf '%s\n' "$1" >>"$GITHUB_OUTPUT"
}

# Insert LINE immediately after the first line matching PATTERN. Portable
# (GNU sed `a` is not BSD sed).
insert_after() {
  local file="$1" pattern="$2" line="$3"
  awk -v pat="$pattern" -v ins="$line" '
    { print }
    $0 ~ pat && !done { print ins; done=1 }
  ' "$file" >"${file}.tmp"
  mv "${file}.tmp" "$file"
}

insert_plugins() {
  local cfg="$1"
  grep -q '^file:file$' "$cfg" || die "plugin.cfg is missing file:file"
  grep -q '^secondary:secondary$' "$cfg" || die "plugin.cfg is missing secondary:secondary"
  if ! grep -q "^dns-update-persistent:${PLUGIN_MODULE}/dns-update-persistent\$" "$cfg"; then
    insert_after "$cfg" '^file:file$' "dns-update-persistent:${PLUGIN_MODULE}/dns-update-persistent"
    insert_after "$cfg" '^dns-update-persistent:' "ixfr:${PLUGIN_MODULE}/ixfr"
    insert_after "$cfg" '^ixfr:' "admin:${PLUGIN_MODULE}/admin"
  fi
  if ! grep -q "^admin:${PLUGIN_MODULE}/admin\$" "$cfg"; then
    if grep -q '^ixfr:' "$cfg"; then
      insert_after "$cfg" '^ixfr:' "admin:${PLUGIN_MODULE}/admin"
    else
      insert_after "$cfg" '^file:file$' "admin:${PLUGIN_MODULE}/admin"
    fi
  fi
  if ! grep -q "^secondary-persistent:${PLUGIN_MODULE}/secondary-persistent\$" "$cfg"; then
    insert_after "$cfg" '^secondary:secondary$' "secondary-persistent:${PLUGIN_MODULE}/secondary-persistent"
  fi
}

ensure_replace() {
  local gomod="$1" spec="$2"
  if ! grep -qF "replace ${spec}" "$gomod"; then
    printf '\nreplace %s\n' "$spec" >>"$gomod"
  fi
}

apply_coredns_patch() {
  local coredns_dir="$1" patch="$2"
  [[ -f "$patch" ]] || die "missing CoreDNS patch $patch"
  git -C "$coredns_dir" apply --check "$patch" || die "CoreDNS HTTPHandler patch does not apply to this tag"
  git -C "$coredns_dir" apply "$patch"
}

read_go_version() {
  local dir="$1"
  if [[ -f "${dir}/.go-version" ]]; then
    tr -d '[:space:]' <"${dir}/.go-version"
  elif [[ -f "${dir}/go-version" ]]; then
    tr -d '[:space:]' <"${dir}/go-version"
  else
    die "missing ${dir}/.go-version"
  fi
}

cmd_prepare() {
  local tag="" plugins_dir="" out_dir=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --tag)
        tag="${2:?}"
        shift 2
        ;;
      --plugins-dir)
        plugins_dir="${2:?}"
        shift 2
        ;;
      --out-dir)
        out_dir="${2:?}"
        shift 2
        ;;
      -h | --help)
        usage
        exit 0
        ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  [[ -n "$tag" && -n "$plugins_dir" && -n "$out_dir" ]] || die "prepare requires --tag, --plugins-dir, --out-dir"
  [[ "$tag" == v* ]] || tag="v${tag}"
  plugins_dir="$(cd "$plugins_dir" && pwd)"
  mkdir -p "$out_dir"
  out_dir="$(cd "$out_dir" && pwd)"

  local coredns_dir="${out_dir}/coredns"
  local plugins_copy="${out_dir}/plugins"

  if [[ ! -d "${coredns_dir}/.git" ]]; then
    git clone --depth 1 --branch "$tag" "$COREDNS_REPO" "$coredns_dir"
  else
    local current
    current="$(git -C "$coredns_dir" describe --tags --exact-match 2>/dev/null || true)"
    if [[ "$current" != "$tag" ]]; then
      git -C "$coredns_dir" fetch --depth 1 origin "refs/tags/${tag}:refs/tags/${tag}"
      git -C "$coredns_dir" checkout --force "$tag"
    fi
  fi

  mkdir -p "$plugins_copy"
  tar -C "$plugins_dir" \
    --exclude .git \
    --exclude coredns \
    --exclude node_modules \
    --exclude admin/ui/node_modules \
    -cf - . | tar -C "$plugins_copy" -xf -

  if [[ -f "${plugins_copy}/admin/ui/package.json" ]]; then
    log "building admin UI"
    (cd "${plugins_copy}/admin/ui" && npm ci && npm run build)
  fi

  insert_plugins "${coredns_dir}/plugin.cfg"
  apply_coredns_patch "$coredns_dir" "${plugins_dir}/patches/coredns-http-handler.patch"
  ensure_replace "${coredns_dir}/go.mod" "${PLUGIN_MODULE} => ../plugins"
  ensure_replace "${plugins_copy}/go.mod" "github.com/coredns/coredns => ../coredns"

  local plugins_commit coredns_commit gitcommit version go_version
  plugins_commit="$(git -C "$plugins_dir" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  coredns_commit="$(git -C "$coredns_dir" rev-parse --short HEAD)"
  gitcommit="${plugins_commit}-coredns-${tag}-${coredns_commit}"
  version="$(grep CoreVersion "${coredns_dir}/coremain/version.go" | awk '{ print $3 }' | tr -d '"')"
  go_version="$(read_go_version "$coredns_dir")"
  cp "${coredns_dir}/.go-version" "${coredns_dir}/go-version"

  log "plugins: $plugins_dir"
  log "coredns tag: $tag @ $coredns_commit"
  log "GITCOMMIT=$gitcommit"
  log "Go $go_version"
  grep -nE 'file:|dns-update-persistent:|ixfr:|admin:|secondary' "${coredns_dir}/plugin.cfg" || true

  (
    cd "$coredns_dir"
    export CGO_ENABLED=0
    export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
    export GOTOOLCHAIN="go${go_version}"
    make gen
  )

  # Drop VCS metadata so the artifact is a portable module tree.
  rm -rf "${coredns_dir}/.git"

  github_output "tag=v${version}"
  github_output "version=${version}"
  github_output "gitcommit=${gitcommit}"
  github_output "coredns_commit=${coredns_commit}"
  github_output "plugins_commit=${plugins_commit}"
  github_output "go_version=${go_version}"
  github_output "src_dir=${out_dir}"
}

host_goarch() {
  case "$(uname -m)" in
    x86_64 | amd64) printf '%s\n' amd64 ;;
    aarch64 | arm64) printf '%s\n' arm64 ;;
    *) printf '%s\n' unknown ;;
  esac
}

PACKAGE_WORK=""
cleanup_package_work() {
  if [[ -n "${PACKAGE_WORK:-}" ]]; then
    rm -rf "${PACKAGE_WORK}"
    PACKAGE_WORK=""
  fi
}

cmd_package() {
  local src_dir="" goos="" goarch="" version="" gitcommit="" out_dir=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --src-dir)
        src_dir="${2:?}"
        shift 2
        ;;
      --goos)
        goos="${2:?}"
        shift 2
        ;;
      --goarch)
        goarch="${2:?}"
        shift 2
        ;;
      --version)
        version="${2:?}"
        shift 2
        ;;
      --gitcommit)
        gitcommit="${2:?}"
        shift 2
        ;;
      --out-dir)
        out_dir="${2:?}"
        shift 2
        ;;
      -h | --help)
        usage
        exit 0
        ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  [[ -n "$src_dir" && -n "$goos" && -n "$goarch" && -n "$version" && -n "$gitcommit" && -n "$out_dir" ]] ||
    die "package requires --src-dir, --goos, --goarch, --version, --gitcommit, --out-dir"

  src_dir="$(cd "$src_dir" && pwd)"
  local coredns_dir="${src_dir}/coredns"
  [[ -d "$coredns_dir" ]] || die "missing ${coredns_dir}"
  mkdir -p "$out_dir"
  out_dir="$(cd "$out_dir" && pwd)"

  local bin="coredns"
  if [[ "$goos" == windows ]]; then
    bin="coredns.exe"
  fi
  # Global, not `local`: an EXIT trap that names a function-local variable
  # runs after the function returns, and `set -u` then fails with
  # `work: unbound variable`.
  PACKAGE_WORK="$(mktemp -d "${TMPDIR:-/tmp}/coredns-pkg.XXXXXX")"
  trap cleanup_package_work EXIT

  local go_version
  go_version="$(read_go_version "$coredns_dir")"
  log "building ${goos}/${goarch} (Go ${go_version}, ${gitcommit})"

  (
    cd "$coredns_dir"
    export CGO_ENABLED=0
    export GOPROXY="${GOPROXY:-https://proxy.golang.org,direct}"
    export GOTOOLCHAIN="go${go_version}"
    export GOOS="$goos"
    export GOARCH="$goarch"
    go build -v -tags="${GOTAGS}" \
      -ldflags="-s -w -X github.com/coredns/coredns/coremain.GitCommit=${gitcommit}" \
      -o "${PACKAGE_WORK}/${bin}" .
  )

  if [[ "$goos" == linux && "$goarch" == "$(host_goarch)" ]]; then
    local plugins_out
    plugins_out="$("${PACKAGE_WORK}/${bin}" -plugins)"
    log "$plugins_out"
    local want
    for want in dns-update-persistent ixfr admin secondary-persistent file secondary kubernetes; do
      grep -F -q "$want" <<<"$plugins_out" || die "binary is missing plugin: $want"
    done
    "${PACKAGE_WORK}/${bin}" -version
  fi

  local stem="coredns_${version}_${goos}_${goarch}"
  tar -zcf "${out_dir}/${stem}.tgz" -C "$PACKAGE_WORK" "$bin"
  if [[ "$goos" == windows ]]; then
    (cd "$PACKAGE_WORK" && zip -q -j "${out_dir}/${stem}.zip" "$bin")
  fi
  (
    cd "$out_dir"
    for asset in "${stem}".tgz "${stem}".zip; do
      [[ -f "$asset" ]] || continue
      sha256sum "$asset" >"${asset}.sha256"
    done
  )
  ls -la "$out_dir"
  cleanup_package_work
  trap - EXIT
}

cmd="${1:-}"
case "$cmd" in
  prepare | package)
    shift
    "cmd_${cmd}" "$@"
    ;;
  -h | --help | "")
    usage
    [[ "$cmd" == "-h" || "$cmd" == "--help" ]]
    ;;
  *)
    usage >&2
    die "unknown command: $cmd"
    ;;
esac
