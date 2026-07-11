#!/usr/bin/env bash
set -euo pipefail

dist_dir="${1:-dist}"
expected_version="${2:-}"

required_common=(
  "README.md"
  "examples/health/README.md"
  "examples/health/splunkd-health.yml"
  ".agents/skills/querysplunk/SKILL.md"
  ".agents/skills/querysplunk/references/yaml-config.md"
  ".agents/skills/querysplunk/references/live-integration.md"
  ".agents/skills/querysplunk/references/release.md"
)

example_output_files=()
while IFS= read -r output_file; do
  example_output_files+=("${output_file}")
done < <(awk -F: '/^[[:space:]]*output_file:/ {gsub(/^[[:space:]]+|[[:space:]]+$/, "", $2); gsub(/^"|"$/, "", $2); print $2}' examples/health/*.yml)

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

check_forbidden_names() {
  local archive="$1"
  local listing="$2"
  if grep -Eq '(^|/)(\.env($|\.)|splunkresults\.json|scheduler-health\.json|splunkd-health\.json)$' <<<"${listing}"; then
    echo "${listing}" >&2
    fail "${archive} contains local env or generated result artifacts"
  fi
  if grep -Eq '^[^/]+/examples/health/.*\.json$' <<<"${listing}"; then
    echo "${listing}" >&2
    fail "${archive} contains generated JSON under examples/health"
  fi
  for output_file in "${example_output_files[@]}"; do
    if grep -Fq "/${output_file}" <<<"${listing}"; then
      echo "${listing}" >&2
      fail "${archive} contains generated example output ${output_file}"
    fi
  done
}

check_listing() {
  local archive="$1"
  local listing="$2"
  local binary_pattern="$3"

  grep -Eq "^[^/]+/${binary_pattern}$" <<<"${listing}" || fail "${archive} is missing binary matching ${binary_pattern}"
  for required in "${required_common[@]}"; do
    grep -Eq "^[^/]+/${required}$" <<<"${listing}" || fail "${archive} is missing ${required}"
  done
  check_forbidden_names "${archive}" "${listing}"
}

shopt -s nullglob
archives=("${dist_dir}"/*.tar.gz "${dist_dir}"/*.zip)
[ "${#archives[@]}" -gt 0 ] || fail "no release archives found in ${dist_dir}"

if [ -n "${expected_version}" ]; then
  case "$(uname -s)-$(uname -m)" in
    Darwin-x86_64) host_archive="${dist_dir}/splunkquery-${expected_version}-darwin-amd64.tar.gz" ;;
    Darwin-arm64) host_archive="${dist_dir}/splunkquery-${expected_version}-darwin-arm64.tar.gz" ;;
    Linux-x86_64) host_archive="${dist_dir}/splunkquery-${expected_version}-linux-amd64.tar.gz" ;;
    Linux-aarch64|Linux-arm64) host_archive="${dist_dir}/splunkquery-${expected_version}-linux-arm64.tar.gz" ;;
    *) host_archive="" ;;
  esac

  if [ -n "${host_archive}" ]; then
    [ -f "${host_archive}" ] || fail "missing host archive ${host_archive}"
    verify_dir="$(mktemp -d)"
    trap 'rm -rf "${verify_dir}"' EXIT
    tar -xzf "${host_archive}" -C "${verify_dir}"
    host_binary="$(find "${verify_dir}" -type f -name splunkquery -print -quit)"
    [ -n "${host_binary}" ] || fail "${host_archive} does not contain splunkquery"
    actual_version="$("${host_binary}" -version)"
    expected_commit="$(git rev-parse --short=12 HEAD 2>/dev/null || true)"
    expected_commit="${expected_commit:-unknown}"
    expected_output="querysplunk version=${expected_version} commit=${expected_commit}"
    if [ "${actual_version}" != "${expected_output}" ]; then
      fail "${host_archive} version output was '${actual_version}'; expected '${expected_output}'"
    fi
    echo "verified ${host_archive} reports ${actual_version}"
  else
    echo "skipped executable version check for unsupported host $(uname -s)-$(uname -m)"
  fi
fi

for archive in "${archives[@]}"; do
  case "${archive}" in
    *.tar.gz)
      listing="$(tar -tzf "${archive}")"
      check_listing "${archive}" "${listing}" "splunkquery"
      ;;
    *.zip)
      command -v zipinfo >/dev/null 2>&1 || fail "zipinfo is required to verify ${archive}"
      listing="$(zipinfo -1 "${archive}")"
      check_listing "${archive}" "${listing}" "splunkquery\.exe"
      ;;
    *)
      fail "unsupported archive ${archive}"
      ;;
  esac
  echo "verified ${archive}"
done
