#!/usr/bin/env bash
set -Eeuo pipefail

readonly RELEASE_API_URL="https://api.github.com/repos/zerohourcash/ZHC-Installer/releases/latest"
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
release_metadata_path="${tmp_dir}/release.json"

log "downloading the latest official Linux release"
curl --fail --silent --show-error --location \
  --retry 3 --retry-delay 2 --connect-timeout 20 \
  --header 'Accept: application/vnd.github+json' \
  --header 'User-Agent: ZHC-Installer-bootstrap' \
  "${RELEASE_API_URL}" \
  --output "${release_metadata_path}"

asset_download_url="$(
  tr ',' '\n' <"${release_metadata_path}" | awk -F'"' -v asset="${ASSET_NAME}" '
    index($0, "\"name\":\"" asset "\"") { wanted = 1 }
    wanted && $2 == "browser_download_url" {
      print $4
      exit
    }
  '
)"
expected_sha256="$(
  tr ',' '\n' <"${release_metadata_path}" | awk -F'"' -v asset="${ASSET_NAME}" '
    index($0, "\"name\":\"" asset "\"") { wanted = 1 }
    wanted && $2 == "digest" && $4 ~ /^sha256:[0-9a-fA-F]{64}$/ {
      sub(/^sha256:/, "", $4)
      print $4
      exit
    }
  '
)"
[[ "${asset_download_url}" =~ ^https://github\.com/zerohourcash/ZHC-Installer/releases/download/[^/]+/${ASSET_NAME}$ ]] || fail "GitHub release metadata does not contain a valid official download URL for ${ASSET_NAME}"
[[ "${expected_sha256}" =~ ^[0-9a-fA-F]{64}$ ]] || fail "GitHub release metadata does not contain a valid SHA256 digest for ${ASSET_NAME}"

curl --fail --silent --show-error --location \
  --retry 3 --retry-delay 2 --connect-timeout 20 \
  "${asset_download_url}" \
  --output "${binary_path}"

actual_sha256="$(sha256sum "${binary_path}" | awk '{ print $1 }')"
[[ "${actual_sha256}" == "${expected_sha256}" ]] || fail "downloaded binary checksum mismatch"
log "official GitHub asset SHA256 verified: ${actual_sha256}"

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
