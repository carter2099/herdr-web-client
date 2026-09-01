#!/usr/bin/env bash
# Herdr Web installer.
#
# Downloads a published herdr-web-client release from GitHub, verifies it
# against the published SHA256SUMS, and installs the binary and the per-user
# systemd unit. Linux amd64/arm64 only.
#
#   curl -fsSL https://raw.githubusercontent.com/carter2099/herdr-web-client/main/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/carter2099/herdr-web-client/main/install.sh | bash -s -- v1.2.3
set -Eeuo pipefail

repo='carter2099/herdr-web-client'
download_base="https://github.com/$repo/releases/download"
bin_dir="$HOME/.local/bin"
unit_dir="$HOME/.config/systemd/user"
config_dir="$HOME/.config/herdr-web-client"
service_name='herdr-web-client.service'

fail() {
  printf 'install.sh: %s\n' "$1" >&2
  exit 1
}

usage() {
  printf 'usage: install.sh [vVERSION]   (default: latest published release)\n' >&2
  exit 2
}

(( $# <= 1 )) || usage
(( EUID != 0 )) || fail 'run as your own user, not root: the service is a per-user systemd unit'
[[ -n ${HOME:-} ]] || fail 'HOME is not set'

[[ $(uname -s) == Linux ]] || fail "unsupported operating system: $(uname -s)"
case $(uname -m) in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac
for required_command in curl sha256sum tar install; do
  command -v "$required_command" >/dev/null 2>&1 ||
    fail "missing required command: $required_command"
done

requested=${1:-}
if [[ -n $requested ]]; then
  tag="v${requested#v}"
else
  if ! tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(v[^"]*\)".*/\1/p'); then
    fail 'could not query the latest release from GitHub'
  fi
fi
[[ $tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]] ||
  fail "no such release: ${tag:-<none>}"

release=${tag#v}
archive="herdr-web-client_${release}_linux_${arch}.tar.gz"

work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT

printf 'Downloading %s (linux %s)...\n' "$tag" "$arch"
curl -fSL --progress-bar -o "$work/$archive" "$download_base/$tag/$archive"
curl -fsSL -o "$work/SHA256SUMS" "$download_base/$tag/SHA256SUMS"

expected=$(awk -v file="$archive" '$2 == file { print $1 }' "$work/SHA256SUMS")
[[ -n $expected ]] || fail "SHA256SUMS has no entry for $archive"
actual=$(sha256sum "$work/$archive" | awk '{ print $1 }')
[[ $actual == "$expected" ]] || fail "checksum mismatch for $archive"

tar -xzf "$work/$archive" -C "$work"
archive_root="$work/herdr-web-client_${release}_linux_${arch}"

mkdir -p -- "$bin_dir" "$unit_dir"
install -m 0755 -- "$archive_root/herdr-web-client" "$bin_dir/herdr-web-client"
install -m 0644 -- "$archive_root/$service_name" "$unit_dir/$service_name"

# Prepare the private environment file for a first install. Existing
# configuration is never touched, so reinstalling keeps it.
if [[ ! -e $config_dir/env ]]; then
  install -d -m 0700 -- "$config_dir"
  install -m 0600 /dev/null "$config_dir/env"
fi

printf 'Installed '
"$bin_dir/herdr-web-client" --version
printf '  %s/herdr-web-client\n' "$bin_dir"
printf '  %s/%s\n' "$unit_dir" "$service_name"
printf 'Next:\n'
printf '  1. Fill in the required values in %s/env (see the README).\n' "$config_dir"
printf '  2. systemctl --user daemon-reload\n'
printf '  3. systemctl --user enable --now %s\n' "$service_name"
