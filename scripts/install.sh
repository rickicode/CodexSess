#!/usr/bin/env bash
set -euo pipefail

REPO="rickicode/CodexSess"
MODE="auto"          # auto|gui|server|update
VERSION="latest"     # latest or vX.Y.Z
BIN_DIR="/usr/local/bin"
NO_SUDO="0"
DETECTED_SERVER_BIN=""

usage() {
  cat <<'EOF'
CodexSess installer

Usage:
  install.sh [options]

Options:
  --mode <auto|gui|server|update> Install mode (default: auto)
  --version <latest|vX.Y.Z>  Release version (default: latest)
  --repo <owner/repo>        GitHub repo (default: rickicode/CodexSess)
  --bin-dir <path>           Binary install dir for server mode (default: /usr/local/bin)
  --no-sudo                  Do not use sudo for install commands
  -h, --help                 Show this help

Examples:
  ./install.sh
  ./install.sh --mode gui --version v1.0.1
  ./install.sh --mode server --bin-dir "$HOME/.local/bin"
  ./install.sh --mode update

Note:
  This installer is Linux-only.
EOF
}

log() { printf '[codexsess-installer] %s\n' "$*"; }
err() { printf '[codexsess-installer] ERROR: %s\n' "$*" >&2; }

run_root() {
  if [[ "${NO_SUDO}" == "1" || "${EUID:-$(id -u)}" -eq 0 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { err "command not found: $1"; exit 1; }
}

has_cmd() {
  command -v "$1" >/dev/null 2>&1
}

detect_arch() {
  local raw
  raw="$(uname -m | tr '[:upper:]' '[:lower:]')"
  case "$raw" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      err "unsupported architecture: ${raw}"
      exit 1
      ;;
  esac
}

detect_os() {
  local raw
  raw="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$raw" in
    linux*) echo "linux" ;;
    *)
      err "unsupported operating system: ${raw}. install.sh is Linux-only"
      exit 1
      ;;
  esac
}

detect_mode_auto() {
  local os_name
  os_name="$(detect_os)"
  # Prefer GUI only when desktop indicators are present and package manager is available.
  if [[ "${os_name}" == "linux" ]] \
    && [[ -n "${DISPLAY:-}" || -n "${WAYLAND_DISPLAY:-}" || -n "${XDG_CURRENT_DESKTOP:-}" ]] \
    && (has_cmd dpkg || has_cmd dnf || has_cmd yum || has_cmd rpm); then
    echo "gui"
  else
    echo "server"
  fi
}

is_gui_package_installed_linux() {
  if has_cmd dpkg-query; then
    if dpkg-query -W -f='${Status}\n' codexsess_GUI 2>/dev/null | grep -q "install ok installed"; then
      return 0
    fi
  fi
  if has_cmd rpm; then
    if rpm -q codexsess_GUI >/dev/null 2>&1; then
      return 0
    fi
  fi
  return 1
}

service_exists_linux() {
  if ! has_cmd systemctl; then
    return 1
  fi
  systemctl list-unit-files codexsess.service --no-legend 2>/dev/null | grep -q "codexsess.service"
}

detect_existing_install_mode() {
  if is_gui_package_installed_linux; then
    echo "gui"
    return
  fi
  if service_exists_linux; then
    DETECTED_SERVER_BIN="$(detect_existing_server_binary_path)"
    echo "server"
    return
  fi
  if [[ -x "/usr/local/bin/codexsess" || -x "/usr/bin/codexsess" ]]; then
    DETECTED_SERVER_BIN="$(detect_existing_server_binary_path)"
    echo "server"
    return
  fi

  echo "none"
}

detect_existing_server_binary_path() {
  if has_cmd codexsess; then
    command -v codexsess
    return
  fi
  if [[ -x "/usr/local/bin/codexsess" ]]; then
    echo "/usr/local/bin/codexsess"
    return
  fi
  if [[ -x "/usr/bin/codexsess" ]]; then
    echo "/usr/bin/codexsess"
    return
  fi
  echo ""
}

resolve_tag() {
  if [[ "${VERSION}" != "latest" ]]; then
    echo "${VERSION}"
    return
  fi
  require_cmd curl
  local latest_url tag
  latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")"
  tag="${latest_url##*/}"
  if [[ -z "$tag" || "$tag" == "latest" ]]; then
    err "failed to resolve latest release tag"
    exit 1
  fi
  echo "$tag"
}

install_binary_file() {
  local src="$1"
  local dst="$2"
  if has_cmd install; then
    run_root install -m 0755 "${src}" "${dst}"
    return
  fi
  # Fallback for minimal environments without `install`.
  run_root cp -f "${src}" "${dst}"
  run_root chmod 0755 "${dst}"
}

install_gui_linux() {
  require_cmd curl
  local tag arch pkg_version tmp pkg
  tag="$(resolve_tag)"
  pkg_version="${tag#v}"
  arch="$(detect_arch)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  if has_cmd dpkg; then
    pkg="codexsess_GUI_${pkg_version}_${arch}.deb"
    log "downloading ${pkg}"
    curl -fL "https://github.com/${REPO}/releases/download/${tag}/${pkg}" -o "${tmp}/${pkg}"
    run_root dpkg -i "${tmp}/${pkg}"
    log "installed GUI package via dpkg: ${pkg}"
    return
  fi

  if has_cmd dnf || has_cmd yum || has_cmd rpm; then
    local rpm_arch
    if [[ "${arch}" == "amd64" ]]; then
      rpm_arch="x86_64"
    else
      rpm_arch="aarch64"
    fi
    pkg="codexsess_GUI-${pkg_version}-1.${rpm_arch}.rpm"
    log "downloading ${pkg}"
    curl -fL "https://github.com/${REPO}/releases/download/${tag}/${pkg}" -o "${tmp}/${pkg}"
    if has_cmd dnf; then
      run_root dnf install -y "${tmp}/${pkg}"
    elif has_cmd yum; then
      run_root yum install -y "${tmp}/${pkg}"
    else
      run_root rpm -Uvh "${tmp}/${pkg}"
    fi
    log "installed GUI package via rpm: ${pkg}"
    return
  fi

  err "unsupported Linux package manager for GUI mode (need dpkg or rpm/dnf/yum)"
  exit 1
}

install_server_binary_release() {
  require_cmd curl

  local tag tmp outbin arch asset
  tag="$(resolve_tag)"
  arch="$(detect_arch)"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  asset="codexsess-linux-${arch}"
  outbin="${BIN_DIR%/}/codexsess"

  log "downloading ${asset} from release ${tag}"
  curl -fL "https://github.com/${REPO}/releases/download/${tag}/${asset}" -o "${tmp}/${asset}"

  run_root mkdir -p "${BIN_DIR}"
  install_binary_file "${tmp}/${asset}" "${outbin}"
  log "installed server binary: ${outbin}"

  configure_server_systemd "${outbin}"
}

configure_server_systemd() {
  local outbin="$1"
  if ! has_cmd systemctl; then
    err "systemctl is not available on this Linux host. server mode requires systemd service setup"
    return
  fi
  if [[ "${NO_SUDO}" == "1" && "${EUID:-$(id -u)}" -ne 0 ]]; then
    log "no root permission; skipping systemd service setup"
    return
  fi
  local tmp unit_path
  unit_path="/etc/systemd/system/codexsess.service"
  tmp="$(mktemp)"
  cat > "${tmp}" <<EOF
[Unit]
Description=CodexSess Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${outbin}
Restart=always
RestartSec=2
Environment=CODEXSESS_NO_OPEN_BROWSER=1

[Install]
WantedBy=multi-user.target
EOF
  run_root install -m 0644 "${tmp}" "${unit_path}"
  rm -f "${tmp}"
  run_root systemctl daemon-reload
  run_root systemctl enable --now codexsess.service
  run_root systemctl restart codexsess.service
  log "systemd service active: codexsess.service"
}

main() {
  detect_os >/dev/null

  while [[ $# -gt 0 ]]; do
    if [[ "$1" == "--mode" || "$1" == "--version" || "$1" == "--repo" || "$1" == "--bin-dir" ]]; then
      if [[ $# -lt 2 || -z "${2:-}" ]]; then
        err "missing value for $1"
        usage
        exit 1
      fi
    fi
    case "$1" in
      --mode) MODE="${2:-}"; shift 2 ;;
      --version) VERSION="${2:-}"; shift 2 ;;
      --repo) REPO="${2:-}"; shift 2 ;;
      --bin-dir) BIN_DIR="${2:-}"; shift 2 ;;
      --no-sudo) NO_SUDO="1"; shift ;;
      -h|--help) usage; exit 0 ;;
      *)
        err "unknown argument: $1"
        usage
        exit 1
        ;;
    esac
  done

  if [[ "${MODE}" == "auto" ]]; then
    MODE="$(detect_mode_auto)"
  fi

  if [[ "${MODE}" == "update" ]]; then
    local detected_mode
    detected_mode="$(detect_existing_install_mode)"
    case "${detected_mode}" in
      gui|server)
        MODE="${detected_mode}"
        if [[ "${MODE}" == "server" && -n "${DETECTED_SERVER_BIN}" ]]; then
          BIN_DIR="$(dirname "${DETECTED_SERVER_BIN}")"
        fi
        log "detected existing install mode: ${MODE}"
        ;;
      *)
        MODE="$(detect_mode_auto)"
        log "no existing install detected; fallback mode: ${MODE}"
        ;;
    esac
  fi

  case "${MODE}" in
    gui)
      if [[ "$(uname -s)" != "Linux" ]]; then
        err "GUI package install currently supports Linux only"
        exit 1
      fi
      install_gui_linux
      ;;
    server)
      install_server_binary_release
      ;;
    update)
      err "invalid mode transition for update"
      exit 1
      ;;
    *)
      err "invalid mode: ${MODE} (expected: auto|gui|server|update)"
      exit 1
      ;;
  esac
}

main "$@"
