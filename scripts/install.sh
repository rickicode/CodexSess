#!/usr/bin/env bash
set -euo pipefail

REPO="rickicode/CodexSess"
MODE="auto"          # auto|gui|server|update
VERSION="latest"     # latest or vX.Y.Z
BIN_DIR="/usr/local/bin"
NO_SUDO="0"
DETECTED_SERVER_BIN=""
RESOLVED_TAG=""
TMP_PATHS=()

if [[ -t 1 ]]; then
  COLOR_INFO=$'\033[1;36m'
  COLOR_STEP=$'\033[1;34m'
  COLOR_OK=$'\033[1;32m'
  COLOR_WARN=$'\033[1;33m'
  COLOR_ERR=$'\033[1;31m'
  COLOR_TITLE=$'\033[1;97m'
  COLOR_RESET=$'\033[0m'
else
  COLOR_INFO=""
  COLOR_STEP=""
  COLOR_OK=""
  COLOR_WARN=""
  COLOR_ERR=""
  COLOR_TITLE=""
  COLOR_RESET=""
fi

cleanup_tmp_paths() {
  local p
  for p in "${TMP_PATHS[@]:-}"; do
    if [[ -n "${p}" ]]; then
      rm -rf "${p}" || true
    fi
  done
}

new_tmp_dir() {
  local d
  d="$(mktemp -d)"
  TMP_PATHS+=("${d}")
  printf '%s' "${d}"
}

trap cleanup_tmp_paths EXIT

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

log() { printf '%s[codexsess-installer]%s %s\n' "${COLOR_INFO}" "${COLOR_RESET}" "$*"; }
step() { printf '%s[codexsess-installer][STEP]%s %s\n' "${COLOR_STEP}" "${COLOR_RESET}" "$*"; }
ok() { printf '%s[codexsess-installer][OK]%s %s\n' "${COLOR_OK}" "${COLOR_RESET}" "$*"; }
warn() { printf '%s[codexsess-installer][WARN]%s %s\n' "${COLOR_WARN}" "${COLOR_RESET}" "$*"; }
err() { printf '%s[codexsess-installer][ERROR]%s %s\n' "${COLOR_ERR}" "${COLOR_RESET}" "$*" >&2; }
title() { printf '\n%s== %s ==%s\n' "${COLOR_TITLE}" "$*" "${COLOR_RESET}"; }

normalize_version() {
  local v
  v="$(printf '%s' "${1:-}" | tr -d '\r' | tr -d '\n' | xargs)"
  v="${v#v}"
  if [[ -z "${v}" ]]; then
    printf 'unknown'
  else
    printf '%s' "${v}"
  fi
}

print_auth_help() {
  ok "default login: username=admin password=hijilabs"
  ok "change password: codexsess --changepassword"
}

run_root() {
  if [[ "${NO_SUDO}" == "1" || "${EUID:-$(id -u)}" -eq 0 ]]; then
    "$@"
  else
    if ! has_cmd sudo; then
      err "sudo not found while root privileges are required for: $*"
      err "rerun as root, or use --no-sudo with --bin-dir \"\$HOME/.local/bin\""
      exit 1
    fi
    sudo "$@"
  fi
}

can_escalate_root() {
  if [[ "${EUID:-$(id -u)}" -eq 0 ]]; then
    return 0
  fi
  if [[ "${NO_SUDO}" == "1" ]]; then
    return 1
  fi
  has_cmd sudo
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || { err "command not found: $1"; exit 1; }
}

has_cmd() {
  command -v "$1" >/dev/null 2>&1
}

installer_user() {
  if [[ "${EUID:-$(id -u)}" -eq 0 && -n "${SUDO_USER:-}" ]]; then
    printf '%s' "${SUDO_USER}"
    return
  fi
  id -un
}

installer_home() {
  local user
  user="$(installer_user)"
  if [[ "${user}" == "root" ]]; then
    printf '/root'
    return
  fi
  if has_cmd getent; then
    local home_dir
    home_dir="$(getent passwd "${user}" | awk -F: '{print $6}')"
    if [[ -n "${home_dir}" ]]; then
      printf '%s' "${home_dir}"
      return
    fi
  fi
  printf '/home/%s' "${user}"
}

ensure_npm_installed() {
  if has_cmd npm; then
    return
  fi
  log "npm not found, installing npm..."
  if has_cmd apt-get; then
    run_root apt-get update -y
    run_root apt-get install -y npm
  elif has_cmd dnf; then
    run_root dnf install -y npm
  elif has_cmd yum; then
    run_root yum install -y npm
  elif has_cmd pacman; then
    run_root pacman -Sy --noconfirm npm
  elif has_cmd zypper; then
    run_root zypper install -y npm
  else
    err "unable to install npm automatically. install npm manually, then rerun installer."
    exit 1
  fi
  if ! has_cmd npm; then
    err "npm installation failed or npm not in PATH"
    exit 1
  fi
  ok "npm installed"
}

ensure_codex_cli() {
  if has_cmd codex; then
    ok "codex CLI detected: $(command -v codex)"
    return
  fi
  ensure_npm_installed
  log "codex CLI not found, installing @openai/codex globally..."
  run_root npm i -g @openai/codex
  if ! has_cmd codex; then
    err "codex CLI installation failed or codex not in PATH after npm install"
    exit 1
  fi
  ok "codex CLI installed: $(command -v codex)"
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

detect_installed_gui_version() {
  local v
  if has_cmd dpkg-query; then
    v="$(dpkg-query -W -f='${Version}\n' codexsess_GUI 2>/dev/null || true)"
    if [[ -n "${v}" ]]; then
      v="${v%%-*}"
      normalize_version "${v}"
      return
    fi
  fi
  if has_cmd rpm; then
    v="$(rpm -q --qf '%{VERSION}\n' codexsess_GUI 2>/dev/null || true)"
    if [[ -n "${v}" ]]; then
      normalize_version "${v}"
      return
    fi
  fi
  printf 'unknown'
}

detect_installed_server_version() {
  local bin out first
  bin="$(detect_existing_server_binary_path)"
  if [[ -z "${bin}" || ! -x "${bin}" ]]; then
    printf 'unknown'
    return
  fi
  out="$("${bin}" --version 2>/dev/null || true)"
  first="$(printf '%s' "${out}" | head -n1)"
  if [[ "${first}" =~ v([0-9]+\.[0-9]+\.[0-9]+) ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
    return
  fi
  normalize_version "${first}"
}

resolve_tag() {
  if [[ -n "${RESOLVED_TAG}" ]]; then
    echo "${RESOLVED_TAG}"
    return
  fi

  if [[ "${VERSION}" != "latest" ]]; then
    RESOLVED_TAG="${VERSION}"
    echo "${RESOLVED_TAG}"
    return
  fi

  require_cmd curl
  local latest_url tag
  latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")"
  tag="${latest_url##*/}"
  if [[ -z "${tag}" || "${tag}" == "latest" ]]; then
    err "failed to resolve latest release tag"
    exit 1
  fi
  RESOLVED_TAG="${tag}"
  echo "${RESOLVED_TAG}"
}

download_release_asset() {
  local tag="$1"
  local asset="$2"
  local out="$3"
  local url
  local ts
  ts="$(date +%s)"
  url="https://github.com/${REPO}/releases/download/${tag}/${asset}?ts=${ts}"
  log "force-downloading ${asset} (cache-bypass) from release ${tag}"
  curl -fL -H 'Cache-Control: no-cache' -H 'Pragma: no-cache' "${url}" -o "${out}"
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
  tmp="$(new_tmp_dir)"

  if has_cmd dpkg; then
    pkg="codexsess_GUI_${pkg_version}_${arch}.deb"
    download_release_asset "${tag}" "${pkg}" "${tmp}/${pkg}"
    run_root dpkg -i "${tmp}/${pkg}"
    ok "installed GUI package via dpkg: ${pkg}"
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
    download_release_asset "${tag}" "${pkg}" "${tmp}/${pkg}"
    if has_cmd dnf; then
      run_root dnf install -y "${tmp}/${pkg}"
    elif has_cmd yum; then
      run_root yum install -y "${tmp}/${pkg}"
    else
      run_root rpm -Uvh "${tmp}/${pkg}"
    fi
    ok "installed GUI package via rpm: ${pkg}"
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
  tmp="$(new_tmp_dir)"

  asset="codexsess-linux-${arch}"
  outbin="${BIN_DIR%/}/codexsess"

  download_release_asset "${tag}" "${asset}" "${tmp}/${asset}"

  run_root mkdir -p "${BIN_DIR}"
  install_binary_file "${tmp}/${asset}" "${outbin}"
  ok "installed server binary: ${outbin}"

  configure_server_systemd "${outbin}"
}

configure_server_systemd() {
  local outbin="$1"
  if ! can_escalate_root; then
    warn "root privileges unavailable; skipping systemd service setup."
    warn "run binary manually: ${outbin}"
    return
  fi
  if ! has_cmd systemctl; then
    warn "systemctl is not available. skipping systemd service setup."
    warn "run binary manually: ${outbin}"
    return
  fi
  local tmp unit_path svc_user svc_home svc_group
  unit_path="/etc/systemd/system/codexsess.service"
  svc_user="$(installer_user)"
  svc_home="$(installer_home)"
  svc_group="$(id -gn "${svc_user}" 2>/dev/null || printf '%s' "${svc_user}")"
  tmp="$(mktemp)"
  cat > "${tmp}" <<EOF
[Unit]
Description=CodexSess Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${svc_user}
Group=${svc_group}
ExecStart=${outbin}
Restart=always
RestartSec=2
Environment=HOME=${svc_home}
Environment=CODEX_HOME=${svc_home}/.codex
Environment=CODEXSESS_PUBLIC=true
Environment=CODEXSESS_NO_OPEN_BROWSER=1

[Install]
WantedBy=multi-user.target
EOF
  run_root install -m 0644 "${tmp}" "${unit_path}"
  rm -f "${tmp}"
  run_root systemctl daemon-reload
  run_root systemctl enable --now codexsess.service
  run_root systemctl restart codexsess.service
  if run_root systemctl is-active --quiet codexsess.service; then
    ok "systemd service active: codexsess.service"
    ok "service user: ${svc_user} (home: ${svc_home})"
    ok "check status with: systemctl status codexsess"
  else
    err "systemd service is not active: codexsess.service"
    log "check status with: systemctl status codexsess"
    return 1
  fi
}

finalize_service_restart() {
  if ! has_cmd systemctl; then
    return
  fi
  if ! service_exists_linux; then
    return
  fi
  if [[ "${NO_SUDO}" == "1" && "${EUID:-$(id -u)}" -ne 0 ]]; then
    err "cannot restart codexsess.service at installer end (no root permission)"
    log "run manually: sudo systemctl restart codexsess && sudo systemctl status codexsess"
    return
  fi
  log "finalizing install: restarting codexsess.service..."
  run_root systemctl restart codexsess.service
  if run_root systemctl is-active --quiet codexsess.service; then
    ok "final restart done: codexsess.service is active"
  else
    err "final restart failed: codexsess.service is not active"
    log "check status with: systemctl status codexsess"
    return 1
  fi
}

main() {
  detect_os >/dev/null
  title "CodexSess Installer"

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
    local installed_version target_version target_tag
    detected_mode="$(detect_existing_install_mode)"
    case "${detected_mode}" in
      gui|server)
        MODE="${detected_mode}"
        if [[ "${MODE}" == "server" && -n "${DETECTED_SERVER_BIN}" ]]; then
          BIN_DIR="$(dirname "${DETECTED_SERVER_BIN}")"
        fi
        target_tag="$(resolve_tag)"
        target_version="$(normalize_version "${target_tag#v}")"
        if [[ "${MODE}" == "gui" ]]; then
          installed_version="$(detect_installed_gui_version)"
        else
          installed_version="$(detect_installed_server_version)"
        fi

        title "Update Summary"
        step "Mode terdeteksi   : ${MODE}"
        step "Versi terpasang   : ${installed_version}"
        step "Versi target      : ${target_version} (${target_tag})"
        if [[ "${installed_version}" != "unknown" && "${installed_version}" == "${target_version}" ]]; then
          warn "Status            : sudah versi terbaru. installer akan reinstall untuk memastikan file sinkron."
        else
          ok "Status            : update tersedia, proses upgrade akan dilanjutkan."
        fi
        log "detected existing install mode: ${MODE}"
        ;;
      *)
        MODE="$(detect_mode_auto)"
        warn "tidak ditemukan instalasi sebelumnya; fallback mode: ${MODE}"
        ;;
    esac
  fi

  if [[ "${MODE}" == "server" && "${BIN_DIR}" == "/usr/local/bin" ]] && ! can_escalate_root; then
    BIN_DIR="${HOME}/.local/bin"
    title "Server Install Context"
    warn "non-root tanpa akses sudo terdeteksi."
    warn "otomatis ganti bin-dir ke: ${BIN_DIR}"
    warn "systemd service root akan dilewati; jalankan binary/manual service sendiri."
  fi

  step "Memastikan Codex CLI tersedia"
  ensure_codex_cli
  step "Menjalankan mode instalasi: ${MODE}"

  case "${MODE}" in
    gui)
      if [[ "$(uname -s)" != "Linux" ]]; then
        err "GUI package install currently supports Linux only"
        exit 1
      fi
      install_gui_linux
      print_auth_help
      ;;
    server)
      install_server_binary_release
      print_auth_help
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

  finalize_service_restart
}

main "$@"
