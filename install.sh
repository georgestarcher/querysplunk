#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Install querysplunk from an extracted release bundle.

Usage: ./install.sh [options]

Options:
  --agent auto|codex|claude|both|none  Assistant skill target (default: auto)
  --bin-dir DIR                        Binary directory (default: ~/.local/bin)
  --home-dir DIR                       Home used for skill installation (default: $HOME)
  --upgrade                            Replace a different installed version
  --allow-downgrade                    Permit an older target version with --upgrade
  -h, --help                           Show this help

The installer never reads or configures Splunk credentials.
EOF
}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source_binary="${script_dir}/splunkquery"
skill_source="${script_dir}/.agents/skills/querysplunk"
home_dir=${HOME:-}
bin_dir=""
agent="auto"
upgrade=false
allow_downgrade=false

while [ "$#" -gt 0 ]; do
  case "$1" in
    --agent)
      [ "$#" -ge 2 ] || fail "--agent requires a value"
      agent=$2
      shift 2
      ;;
    --bin-dir)
      [ "$#" -ge 2 ] || fail "--bin-dir requires a value"
      bin_dir=$2
      shift 2
      ;;
    --home-dir)
      [ "$#" -ge 2 ] || fail "--home-dir requires a value"
      home_dir=$2
      shift 2
      ;;
    --upgrade)
      upgrade=true
      shift
      ;;
    --allow-downgrade)
      allow_downgrade=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) fail "unknown option: $1" ;;
  esac
done

case "$agent" in
  auto|codex|claude|both|none) ;;
  *) fail "--agent must be auto, codex, claude, both, or none" ;;
esac
[ -n "$home_dir" ] || fail "HOME is not set; use --home-dir"
[ "$allow_downgrade" = false ] || [ "$upgrade" = true ] || fail "--allow-downgrade requires --upgrade"
[ -n "$bin_dir" ] || bin_dir="${home_dir}/.local/bin"
[ -f "$source_binary" ] || fail "release bundle is missing splunkquery"
[ -f "${skill_source}/SKILL.md" ] || fail "release bundle is missing the querysplunk skill"
grep -Eq '^name:[[:space:]]+querysplunk[[:space:]]*$' "${skill_source}/SKILL.md" || fail "querysplunk skill has invalid name frontmatter"
grep -Eq '^description:[[:space:]]+[^[:space:]].*$' "${skill_source}/SKILL.md" || fail "querysplunk skill has invalid description frontmatter"

binary_version() {
  output=$("$1" -version 2>/dev/null) || return 1
  printf '%s\n' "$output" | sed -n 's/.*version=\([^[:space:]]*\).*/\1/p' | sed -n '1p'
}

compare_versions() {
  awk -v left="$1" -v right="$2" '
    function prepare(value, side, dash, count, parts) {
      sub(/^v/, "", value)
      sub(/\+.*/, "", value)
      dash = index(value, "-")
      if (dash > 0) {
        pre[side] = substr(value, dash + 1)
        value = substr(value, 1, dash - 1)
      } else {
        pre[side] = ""
      }
      count = split(value, parts, ".")
      if (count != 3) return 0
      for (i = 1; i <= 3; i++) {
        if (parts[i] !~ /^[0-9]+$/) return 0
        core[side, i] = parts[i] + 0
      }
      return 1
    }
    BEGIN {
      if (!prepare(left, "l") || !prepare(right, "r")) { print 2; exit }
      for (i = 1; i <= 3; i++) {
        if (core["l", i] < core["r", i]) { print -1; exit }
        if (core["l", i] > core["r", i]) { print 1; exit }
      }
      if (pre["l"] == "" && pre["r"] != "") { print 1; exit }
      if (pre["l"] != "" && pre["r"] == "") { print -1; exit }
      if (pre["l"] == pre["r"]) { print 0; exit }
      ln = split(pre["l"], la, ".")
      rn = split(pre["r"], ra, ".")
      limit = ln > rn ? ln : rn
      for (i = 1; i <= limit; i++) {
        if (i > ln) { print -1; exit }
        if (i > rn) { print 1; exit }
        lnum = la[i] ~ /^[0-9]+$/
        rnum = ra[i] ~ /^[0-9]+$/
        if (lnum && rnum) {
          if ((la[i] + 0) < (ra[i] + 0)) { print -1; exit }
          if ((la[i] + 0) > (ra[i] + 0)) { print 1; exit }
        } else if (lnum != rnum) {
          print lnum ? -1 : 1
          exit
        } else {
          if (la[i] < ra[i]) { print -1; exit }
          if (la[i] > ra[i]) { print 1; exit }
        }
      }
      print 0
    }
  '
}

source_version=$(binary_version "$source_binary") || fail "could not read bundled binary version"
case "$source_version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) fail "bundled binary does not contain a release version" ;;
esac

target_binary="${bin_dir}/querysplunk"
current_version=""
if [ -x "$target_binary" ]; then
  current_version=$(binary_version "$target_binary" || true)
fi

if [ -n "$current_version" ] && [ "$current_version" != "$source_version" ]; then
  comparison=$(compare_versions "$source_version" "$current_version")
  [ "$upgrade" = true ] || fail "querysplunk ${current_version} is installed; rerun with --upgrade for ${source_version}"
  if [ "$comparison" = "-1" ] && [ "$allow_downgrade" = false ]; then
    fail "refusing to downgrade querysplunk from ${current_version} to ${source_version}; use --upgrade --allow-downgrade to confirm"
  fi
fi

mkdir -p "$bin_dir"
binary_temp="${bin_dir}/.querysplunk.install.$$"
binary_backup="${bin_dir}/.querysplunk.backup.$$"
rm -f "$binary_temp" "$binary_backup"
cp "$source_binary" "$binary_temp"
chmod 0755 "$binary_temp"
if [ -e "$target_binary" ]; then
  mv "$target_binary" "$binary_backup"
fi
if mv "$binary_temp" "$target_binary" && installed_version=$(binary_version "$target_binary") && [ "$installed_version" = "$source_version" ]; then
  rm -f "$binary_backup"
else
  rm -f "$target_binary" "$binary_temp"
  if [ -e "$binary_backup" ]; then
    mv "$binary_backup" "$target_binary"
  fi
  fail "binary installation failed; the previous installation was restored"
fi

install_skill() {
  assistant=$1
  case "$assistant" in
    codex) target="${home_dir}/.codex/skills/querysplunk" ;;
    claude) target="${home_dir}/.claude/skills/querysplunk" ;;
    *) fail "unsupported assistant target ${assistant}" ;;
  esac
  parent=$(dirname "$target")
  temp="${parent}/.querysplunk.install.$$"
  backup="${parent}/.querysplunk.backup.$$"
  mkdir -p "$parent"
  rm -rf "$temp" "$backup"
  mkdir -p "$temp"
  cp -R "${skill_source}/." "$temp/"
  if [ -e "$target" ]; then
    mv "$target" "$backup"
  fi
  if mv "$temp" "$target" && [ -f "${target}/SKILL.md" ]; then
    rm -rf "$backup"
  else
    rm -rf "$target" "$temp"
    if [ -e "$backup" ]; then
      mv "$backup" "$target"
    fi
    fail "${assistant} skill installation failed; the previous skill was restored"
  fi
  echo "Installed ${assistant} skill: ${target}"
}

if [ "$agent" = "auto" ]; then
  codex_detected=false
  claude_detected=false
  if [ -d "${home_dir}/.codex" ] || command -v codex >/dev/null 2>&1; then codex_detected=true; fi
  if [ -d "${home_dir}/.claude" ] || command -v claude >/dev/null 2>&1; then claude_detected=true; fi
  if [ "$codex_detected" = true ] && [ "$claude_detected" = true ]; then
    agent=both
  elif [ "$codex_detected" = true ]; then
    agent=codex
  elif [ "$claude_detected" = true ]; then
    agent=claude
  else
    agent=none
  fi
fi

case "$agent" in
  codex) install_skill codex ;;
  claude) install_skill claude ;;
  both) install_skill codex; install_skill claude ;;
  none) echo "No assistant skill selected; use --agent codex, claude, or both to install one." ;;
esac

echo "Installed querysplunk ${source_version}: ${target_binary}"
case ":${PATH:-}:" in
  *":${bin_dir}:"*) ;;
  *)
    echo "Add querysplunk to this shell with:"
    printf '  export PATH="%s:$PATH"\n' "$bin_dir"
    ;;
esac
echo "Verify with: ${target_binary} -version"
echo "This installer did not read or configure Splunk credentials."
