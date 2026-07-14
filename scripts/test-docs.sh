#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM
cd "$repo_dir"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

binary="${tmp_dir}/querysplunk"
version="v0.0.0-qa"
commit="docsqa"
go build -trimpath -ldflags="-X main.version=${version} -X main.commit=${commit}" -o "$binary" .

actual_version=$("$binary" -version)
[ "$actual_version" = "querysplunk version=${version} commit=${commit}" ] || fail "release metadata output drifted"

"$binary" -h >"${tmp_dir}/help.txt" 2>&1
awk '/^  -[a-z]/ {print $1}' "${tmp_dir}/help.txt" | sort -u >"${tmp_dir}/help-flags"
awk '/^  -[a-z]/ {print $1}' README.md | sort -u >"${tmp_dir}/readme-flags"
comm -23 "${tmp_dir}/help-flags" "${tmp_dir}/readme-flags" >"${tmp_dir}/missing-readme-flags"
comm -13 "${tmp_dir}/help-flags" "${tmp_dir}/readme-flags" >"${tmp_dir}/stale-readme-flags"
if [ -s "${tmp_dir}/missing-readme-flags" ]; then
  cat "${tmp_dir}/missing-readme-flags" >&2
  fail "CLI flags are missing from the README help snapshot"
fi
if [ -s "${tmp_dir}/stale-readme-flags" ]; then
  cat "${tmp_dir}/stale-readme-flags" >&2
  fail "README help snapshot contains stale CLI flags"
fi

./install.sh --help >"${tmp_dir}/install-help.txt"
check_installer_option() {
  posix_option=$1
  powershell_option=$2
  grep -F -- "$posix_option" "${tmp_dir}/install-help.txt" >/dev/null || fail "POSIX installer help is missing ${posix_option}"
  grep -F -- "$posix_option" INSTALL.md >/dev/null || fail "INSTALL.md is missing ${posix_option}"
  grep -F -- "$powershell_option" INSTALL.md >/dev/null || fail "INSTALL.md is missing ${powershell_option}"
}
check_installer_option --agent -Agent
check_installer_option --bin-dir -BinDir
check_installer_option --home-dir -HomeDir
check_installer_option --upgrade -Upgrade
check_installer_option --allow-downgrade -AllowDowngrade

rg -n -o '\]\([^)]+\)' --glob '*.md' . | while IFS=: read -r file line token; do
  link=$(printf '%s' "$token" | sed 's/^](//; s/)$//')
  case "$link" in
    http://*|https://*|mailto:*|\#*) continue ;;
  esac
  link=${link%%#*}
  link=${link%%\?*}
  [ -n "$link" ] || continue
  target="$(dirname "$file")/$link"
  [ -e "$target" ] || fail "$file:$line has broken local link $link"
done

unset SPLUNKBASEURL SPLUNKTOKEN SPLUNKUSERNAME SPLUNKPASSWORD SPLUNKAPP || true
"$binary" -write-config "${tmp_dir}/generated.yml" >/dev/null 2>&1
"$binary" -validate-config "${tmp_dir}/generated.yml" >"${tmp_dir}/generated-plan.yml"
for config in examples/health/*.yml examples/rest/*.yml; do
  "$binary" -validate-config "$config" >"${tmp_dir}/$(basename "$config").plan.yml"
done

skill_dir=".agents/skills/querysplunk"
[ "$(sed -n '1p' "${skill_dir}/SKILL.md")" = "---" ] || fail "skill frontmatter is missing"
grep -Fx 'name: querysplunk' "${skill_dir}/SKILL.md" >/dev/null || fail "skill name frontmatter is invalid"
grep -Eq '^description:[[:space:]]+[^[:space:]].*$' "${skill_dir}/SKILL.md" || fail "skill description frontmatter is invalid"
for required in \
  references/health-diagnostics.md \
  references/installation.md \
  references/live-integration.md \
  references/preflight-and-recovery.md \
  references/release.md \
  references/rest-inspection.md \
  references/result-analysis.md \
  references/spl-authoring.md \
  references/yaml-config.md \
  templates/handoff.yml; do
  [ -f "${skill_dir}/${required}" ] || fail "skill is missing ${required}"
done

grep -F '| savedsearch "' README.md >/dev/null || fail "README savedsearch example is missing the generating pipe"
grep -F 'Never use direct token-bearing `curl`' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference is missing the direct-call safety boundary"
grep -F 'Resolve at most five levels' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference is missing its recursion limit"
grep -F 'complete stanza title including arity' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference does not distinguish macro arity"
grep -F 'Execution time that regularly meets or exceeds that' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference is missing saved-search schedule-overlap guidance"
grep -F 'examples/health/scheduler-health.yml' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference does not connect overlap analysis to scheduler health"
grep -F '| table title search disabled is_scheduled cron_schedule alert_type actions dispatch.earliest_time dispatch.latest_time eai:acl.app eai:acl.owner eai:acl.sharing' examples/rest/saved-search-definition.yml >/dev/null || fail "saved-search inspection is missing its bounded SPL, schedule, action, or namespace fields"
grep -F 'add_orphan_field=true' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not request Splunk orphan status"
grep -F 'search="orphan=1" search="is_scheduled=1" count=100' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not filter before its finite result bound"
grep -F '| where orphan=1 AND is_scheduled=1' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not select scheduled orphaned searches"
grep -F '| rename eai:acl.app AS app eai:acl.owner AS owner' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not expose simple app and owner fields"
grep -F '/configs/conf-macros count=2' examples/rest/macro-definitions.yml >/dev/null || fail "macro inspection does not use the filterable endpoint with ambiguity detection"
grep -F '| inputlookup max=100 example_lookup' examples/rest/lookup-preview.yml >/dev/null || fail "lookup preview does not bound rows at inputlookup"
for config in examples/health/system-messages.yml examples/health/orphaned-scheduled-searches.yml examples/rest/saved-search-definition.yml examples/rest/macro-definitions.yml examples/rest/lookup-definitions.yml; do
  grep -Eq '^[[:space:]]+\| (fields|table) ' "$config" || fail "$config does not project a bounded field set"
done

if rg -n 'v1\.1\.0|## Quick setup|does not have standard YAML frontmatter' README.md INSTALL.md "$skill_dir"; then
  fail "documentation contains stale release or skill language"
fi

echo "documentation, help, examples, and skill QA passed"
