#!/usr/bin/env bash
set -euo pipefail

version="${1:?usage: package-release.sh VERSION [binary_name] [dist_dir]}"
binary_name="${2:-splunkquery}"
dist_dir="${3:-dist}"
build_dir="build"
commit="$(git rev-parse --short=12 HEAD 2>/dev/null || true)"
commit="${commit:-unknown}"
ldflags="-s -w -X main.version=${version} -X main.commit=${commit}"

case "${version}" in
  v*) ;;
  *) echo "version must start with v, got ${version}" >&2; exit 1 ;;
esac

rm -rf "${build_dir}" "${dist_dir}"
mkdir -p "${build_dir}" "${dist_dir}"
dist_abs="$(cd "${dist_dir}" && pwd)"

copy_bundle_files() {
  local package_dir="$1"
  local goos="$2"
  cp README.md INSTALL.md "${package_dir}/"
  mkdir -p "${package_dir}/examples" "${package_dir}/.agents/skills"
  mkdir -p "${package_dir}/examples/health"
  find examples/health -maxdepth 1 -type f \( -name '*.md' -o -name '*.yml' \) -exec cp {} "${package_dir}/examples/health/" \;
  cp -R .agents/skills/querysplunk "${package_dir}/.agents/skills/querysplunk"
  if [ "${goos}" = "windows" ]; then
    cp install.ps1 "${package_dir}/install.ps1"
  else
    cp install.sh "${package_dir}/install.sh"
    chmod 0755 "${package_dir}/install.sh"
  fi
}

build_package() {
  local goos="$1"
  local goarch="$2"
  local ext="${3:-}"
  local name="${binary_name}-${version}-${goos}-${goarch}"
  local package_dir="${build_dir}/${name}"
  local output="${package_dir}/${binary_name}${ext}"

  mkdir -p "${package_dir}"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build -trimpath -ldflags="${ldflags}" -o "${output}" .
  copy_bundle_files "${package_dir}" "${goos}"

  if [ "${goos}" = "windows" ]; then
    (cd "${build_dir}" && zip -qr "${dist_abs}/${name}.zip" "${name}")
  else
    tar -C "${build_dir}" -czf "${dist_abs}/${name}.tar.gz" "${name}"
  fi
}

build_package darwin amd64
build_package darwin arm64
build_package linux amd64
build_package linux arm64
build_package windows amd64 .exe

if command -v sha256sum >/dev/null 2>&1; then
  (cd "${dist_dir}" && sha256sum * > checksums.txt)
else
  (cd "${dist_dir}" && shasum -a 256 * > checksums.txt)
fi
