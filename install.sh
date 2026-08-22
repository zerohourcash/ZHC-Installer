#!/usr/bin/env bash
set -Eeuo pipefail

readonly RELEASE_BASE_URL="https://github.com/zerohourcash/ZHC-Installer/releases/latest/download"
readonly ASSET_NAME="zhc-installer-linux"

log() {
  printf '[ZHC-Installer] %s\n' "$*"
}

fail() {
  printf '[ZHC-Installer] ERROR: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

[[ "$(uname -s)" == "Linux" ]] || fail "this bootstrap script supports Linux only"

case "$(uname -m)" in
  x86_64 | amd64) ;;
  *) fail "unsupported architecture: $(uname -m); the current release provides Linux x86_64 only" ;;
esac

require_command curl
require_command sha256sum
require_command awk
require_command install
require_command mktemp

if [[ "${EUID}" -eq 0 ]]; then
  install_dir="${ZHC_INSTALL_PREFIX:-/usr/local/bin}"
else
  if [[ -z "${XDG_CURRENT_DESKTOP:-}${DESKTOP_SESSION:-}${GDMSESSION:-}${WAYLAND_DISPLAY:-}" ]]; then
    fail "a headless/server installation must run as root; use: curl -fsSL https://raw.githubusercontent.com/zerohourcash/ZHC-Installer/main/install.sh | sudo bash"
  fi
  install_dir="${ZHC_INSTALL_PREFIX:-${HOME}/.local/bin}"
fi

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT HUP INT TERM

binary_path="${tmp_dir}/${ASSET_NAME}"
checksums_path="${tmp_dir}/SHA256SUMS"

log "downloading the latest official Linux release"
curl --fail --silent --show-error --location \
  --retry 3 --retry-delay 2 --connect-timeout 20 \
  "${RELEASE_BASE_URL}/${ASSET_NAME}" \
  --output "${binary_path}"
curl --fail --silent --show-error --location \
  --retry 3 --retry-delay 2 --connect-timeout 20 \
  "${RELEASE_BASE_URL}/SHA256SUMS" \
  --output "${checksums_path}"

expected_sha256="$(awk -v asset="${ASSET_NAME}" '$2 == asset { print $1; exit }' "${checksums_path}")"
[[ "${expected_sha256}" =~ ^[0-9a-fA-F]{64}$ ]] || fail "SHA256SUMS does not contain a valid checksum for ${ASSET_NAME}"

actual_sha256="$(sha256sum "${binary_path}" | awk '{ print $1 }')"
[[ "${actual_sha256}" == "${expected_sha256}" ]] || fail "downloaded binary checksum mismatch"
log "SHA256 verified: ${actual_sha256}"

mkdir -p -- "${install_dir}"
installed_path="${install_dir}/zhc-installer"
install -m 0755 "${binary_path}" "${installed_path}"
log "installed executable: ${installed_path}"

if [[ "${ZHC_INSTALL_ONLY:-0}" == "1" ]]; then
  log "installation-only mode complete"
  exit 0
fi

log "starting ZHCASH installation"
cleanup
trap - EXIT HUP INT TERM
exec "${installed_path}" --no-wait-on-exit "$@"
