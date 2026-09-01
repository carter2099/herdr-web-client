#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd -- "$ROOT_DIR"
version="${HERDR_WEB_CLIENT_VERSION:-${VERSION:-}}"


if [[ -n "$(git status --porcelain=v1 --untracked-files=all)" ]]; then
  printf '%s\n' 'Refusing to release from a dirty git tree.' >&2
  git status --short >&2
  exit 1
fi
source_sha="$(git rev-parse --verify 'HEAD^{commit}')"
if [[ ! "$source_sha" =~ ^[0-9a-f]{40}$ ]]; then
  printf '%s\n' 'Could not resolve a full commit SHA for the release source.' >&2
  exit 1
fi
if [[ -z "$version" ]]; then
  version="$source_sha"
fi


release_tmp="$(mktemp -d "${TMPDIR:-/tmp}/herdr-web-client-release.XXXXXX")"
rollback_tmp="$(mktemp -d "${TMPDIR:-/tmp}/herdr-web-client-rollback.XXXXXX")"
source_dir="${release_tmp}/source"
built_binary="${release_tmp}/herdr-web-client"
binary_stage=''
unit_stage=''
artifacts_installed=0
had_binary=0
had_unit=0
was_enabled=0
was_active=0

binary_dir="${HOME}/.local/bin"
unit_dir="${HOME}/.config/systemd/user"
binary_target="${binary_dir}/herdr-web-client"
unit_target="${unit_dir}/herdr-web-client.service"
service_name='herdr-web-client.service'
old_binary="${rollback_tmp}/herdr-web-client"
old_unit="${rollback_tmp}/herdr-web-client.service"
env_file="${HOME}/.config/herdr-web-client/env"

# Read one simple KEY=value assignment without evaluating the environment
# file. Shell expansions, command substitutions, and other shell syntax are
# deliberately treated as literal text.
config_value() {
  local wanted=$1
  local line key value
  local found=0
  local result=''
  [[ -f "$env_file" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    if [[ "$line" =~ ^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]]; then
      key="${BASH_REMATCH[1]}"
      [[ "$key" == "$wanted" ]] || continue
      value="${BASH_REMATCH[2]}"
      if [[ ${#value} -ge 2 && ${value:0:1} == '"' && ${value: -1} == '"' ]]; then
        value="${value:1:${#value}-2}"
      elif [[ ${#value} -ge 2 && ${value:0:1} == "'" && ${value: -1} == "'" ]]; then
        value="${value:1:${#value}-2}"
      fi
      result="$value"
      found=1
    fi
  done < "$env_file"
  if (( found )); then
    printf '%s\n' "$result"
    return 0
  fi
  return 1
}

rollback_and_cleanup() {
  local status=$?
  local restore_stage

  if (( status != 0 && artifacts_installed )); then
    set +e

    if (( had_binary )); then
      restore_stage="$(mktemp "${binary_dir}/.herdr-web-client-restore.XXXXXX")"
      cp -a --remove-destination -- "$old_binary" "$restore_stage" && mv -f -- "$restore_stage" "$binary_target"
      rm -f -- "$restore_stage"
    else
      rm -f -- "$binary_target"
    fi

    if (( had_unit )); then
      restore_stage="$(mktemp "${unit_dir}/.herdr-web-client-restore.XXXXXX")"
      cp -a --remove-destination -- "$old_unit" "$restore_stage" && mv -f -- "$restore_stage" "$unit_target"
      rm -f -- "$restore_stage"
    else
      rm -f -- "$unit_target"
    fi

    systemctl --user daemon-reload >/dev/null 2>&1 || true
    if (( had_unit )); then
      if (( was_enabled )); then
        systemctl --user enable "$service_name" >/dev/null 2>&1 || true
      else
        systemctl --user disable "$service_name" >/dev/null 2>&1 || true
      fi
      if (( was_active )); then
        systemctl --user restart "$service_name" >/dev/null 2>&1 || true
      else
        systemctl --user stop "$service_name" >/dev/null 2>&1 || true
      fi
    else
      systemctl --user disable --now "$service_name" >/dev/null 2>&1 || true
      systemctl --user reset-failed "$service_name" >/dev/null 2>&1 || true
    fi
  fi

  rm -f -- "$binary_stage" "$unit_stage" 2>/dev/null || true
  rm -rf -- "$release_tmp" "$rollback_tmp"
  exit "$status"
}
trap rollback_and_cleanup EXIT

install -d -m 0755 -- "$binary_dir" "$unit_dir" "$source_dir"

if [[ -e "$binary_target" ]]; then
  cp -a -- "$binary_target" "$old_binary"
  had_binary=1
fi
if [[ -e "$unit_target" ]]; then
  cp -a -- "$unit_target" "$old_unit"
  had_unit=1
fi
if systemctl --user is-enabled --quiet "$service_name" 2>/dev/null; then
  was_enabled=1
fi
if systemctl --user is-active --quiet "$service_name" 2>/dev/null; then
  was_active=1
fi

# Build the exact clean commit in scratch space. A failed dependency install or
# asset build cannot modify the development checkout.
git archive --format=tar "$source_sha" | tar -xf - -C "$source_dir"
(
  cd -- "$source_dir"
  bun install --frozen-lockfile --ignore-scripts
  HERDR_WEB_CLIENT_VERSION="$version" scripts/build "$built_binary"
)

# Stage in the destination directories so each rename is an atomic replacement
# on the same filesystem as its final path.
binary_stage="$(mktemp "${binary_dir}/.herdr-web-client.XXXXXX")"
unit_stage="$(mktemp "${unit_dir}/.herdr-web-client.service.XXXXXX")"
install -m 0755 -- "$built_binary" "$binary_stage"
install -m 0644 -- "$source_dir/deploy/herdr-web-client.service" "$unit_stage"

artifacts_installed=1
mv -f -- "$binary_stage" "$binary_target"
binary_stage=''
mv -f -- "$unit_stage" "$unit_target"
unit_stage=''

if listen_addr="$(config_value HERDR_WEB_CLIENT_LISTEN_ADDR)"; then
  :
else
  listen_addr='127.0.0.1:8080'
fi
if public_origin="$(config_value HERDR_WEB_CLIENT_PUBLIC_ORIGIN)"; then
  :
else
  public_origin=''
fi
if [[ "$public_origin" != https://* ]]; then
  printf '%s\n' "Configured public origin is missing or is not HTTPS in $env_file." >&2
  exit 1
fi
public_host="${public_origin#https://}"
if [[ -z "$public_host" || "$public_host" == */* || "$public_host" == *'?'* || "$public_host" == *'#'* ]]; then
  printf '%s\n' "Configured public origin is not an exact origin in $env_file." >&2
  exit 1
fi

systemctl --user daemon-reload
systemctl --user enable "$service_name"
systemctl --user restart "$service_name"

ready=0
for _ in {1..40}; do
  status_code="$(
    curl --silent --show-error --noproxy '*' --max-time 1 --output /dev/null --write-out '%{http_code}' \
      --header "Host: $public_host" "http://$listen_addr/" 2>/dev/null || true
  )"
  if [[ "$status_code" == '200' ]] && systemctl --user is-active --quiet "$service_name"; then
    ready=1
    break
  fi
  sleep 0.25
done
if (( ! ready )); then
  systemctl --user status "$service_name" --no-pager >&2 || true
  printf '%s\n' 'herdr-web-client did not become ready; restoring the prior release.' >&2
  exit 1
fi

# Readiness makes the new pair authoritative; the EXIT trap now only removes
# temporary files.
artifacts_installed=0
printf '%s\n' 'herdr-web-client release installed, restarted, and ready.'
