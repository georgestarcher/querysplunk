#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

make_bundle() {
  destination=$1
  version=$2
  mkdir -p "${destination}/.agents/skills"
  cp "${repo_dir}/install.sh" "${destination}/install.sh"
  cp -R "${repo_dir}/.agents/skills/querysplunk" "${destination}/.agents/skills/querysplunk"
  cat >"${destination}/splunkquery" <<EOF
#!/bin/sh
echo 'querysplunk version=${version} commit=installer-test'
EOF
  chmod 0755 "${destination}/install.sh" "${destination}/splunkquery"
}

home_dir="${work_dir}/home with spaces"
bin_dir="${work_dir}/bin with spaces"
bundle_v1="${work_dir}/bundle-v1"
bundle_v2="${work_dir}/bundle-v2"
mkdir -p "${home_dir}/.codex/skills/other" "${home_dir}/.claude" "${home_dir}/saved"
echo keep >"${home_dir}/.codex/skills/other/KEEP"
echo 'search: keep' >"${home_dir}/saved/user.yml"
echo 'SPLUNKTOKEN=placeholder-not-a-secret' >"${home_dir}/saved/.env.test"
make_bundle "$bundle_v1" "v1.0.0"
make_bundle "$bundle_v2" "v2.0.0"

blocked_bin="${work_dir}/blocked bin"
mkdir -p "$blocked_bin"
echo keep >"${blocked_bin}/querysplunk"
if HOME="$home_dir" "$bundle_v1/install.sh" --agent none --home-dir "$home_dir" --bin-dir "$blocked_bin" >/dev/null 2>&1; then
  fail "unrecognized existing binary was overwritten"
fi
[ "$(cat "$blocked_bin/querysplunk")" = "keep" ] || fail "unrecognized existing binary was not preserved"

HOME="$home_dir" "$bundle_v1/install.sh" --agent both --home-dir "$home_dir" --bin-dir "$bin_dir" >/dev/null
[ "$("$bin_dir/querysplunk" -version)" = "querysplunk version=v1.0.0 commit=installer-test" ] || fail "fresh binary installation failed"
[ -f "$home_dir/.codex/skills/querysplunk/SKILL.md" ] || fail "Codex skill was not installed"
[ -f "$home_dir/.claude/skills/querysplunk/SKILL.md" ] || fail "Claude skill was not installed"

HOME="$home_dir" "$bundle_v1/install.sh" --agent both --home-dir "$home_dir" --bin-dir "$bin_dir" >/dev/null
if HOME="$home_dir" "$bundle_v2/install.sh" --agent both --home-dir "$home_dir" --bin-dir "$bin_dir" >/dev/null 2>&1; then
  fail "newer version installed without --upgrade"
fi
HOME="$home_dir" "$bundle_v2/install.sh" --upgrade --agent both --home-dir "$home_dir" --bin-dir "$bin_dir" >/dev/null
[ "$("$bin_dir/querysplunk" -version)" = "querysplunk version=v2.0.0 commit=installer-test" ] || fail "upgrade failed"
[ -f "$home_dir/.codex/skills/other/KEEP" ] || fail "upgrade removed an unrelated skill"
[ -f "$home_dir/saved/user.yml" ] && [ -f "$home_dir/saved/.env.test" ] || fail "upgrade removed user files"

if HOME="$home_dir" "$bundle_v1/install.sh" --upgrade --agent both --home-dir "$home_dir" --bin-dir "$bin_dir" >/dev/null 2>&1; then
  fail "downgrade was not blocked"
fi
HOME="$home_dir" "$bundle_v1/install.sh" --upgrade --allow-downgrade --agent none --home-dir "$home_dir" --bin-dir "$bin_dir" >/dev/null
[ "$("$bin_dir/querysplunk" -version)" = "querysplunk version=v1.0.0 commit=installer-test" ] || fail "authorized downgrade failed"

custom_home="${work_dir}/custom home"
custom_bin="${work_dir}/custom destination/bin"
HOME="$custom_home" "$bundle_v2/install.sh" --agent none --home-dir "$custom_home" --bin-dir "$custom_bin" >/dev/null
[ -x "$custom_bin/querysplunk" ] || fail "custom binary destination failed"
[ ! -e "$custom_home/.codex" ] && [ ! -e "$custom_home/.claude" ] || fail "agent none created skill directories"

echo "POSIX installer tests passed"
