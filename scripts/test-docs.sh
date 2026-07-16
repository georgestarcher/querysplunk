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
grep -F 'Run `querysplunk -h` for the full CLI reference.' README.md >/dev/null || fail "README does not point users to the complete CLI help"
for required_flag in -validate-config -json-events -allow-old-earliest -allow-index-wildcard -job-sid; do
  grep -F -- "$required_flag" README.md >/dev/null || fail "README is missing key workflow flag ${required_flag}"
done

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
metadata_ids="${tmp_dir}/oob-metadata-ids"
: >"$metadata_ids"
for config in examples/health/*.yml examples/rest/*.yml examples/detections/*.yml examples/detections/ai-agent/*.yml examples/pentest/*.yml \
  .agents/skills/querysplunk/templates/long-running-successful-searches.yml \
  .agents/skills/querysplunk/templates/recent-search-job-failures.yml; do
  "$binary" -validate-config "$config" >"${tmp_dir}/$(basename "$config").plan.yml"

  grep -Fx 'schema_version: "1"' "$config" >/dev/null || fail "$config is missing schema_version 1"
  for block in metadata requirements provenance interpretation result_handling result_contract; do
    grep -Eq "^${block}:$" "$config" || fail "$config is missing its $block block"
  done

  metadata_id=$(sed -n 's/^  id: //p' "$config")
  [ -n "$metadata_id" ] || fail "$config is missing metadata.id"
  printf '%s\n' "$metadata_id" >>"$metadata_ids"

  case "$config" in
    examples/detections/ai-agent/*.yml)
      grep -Fx '  source: Agent Threat Rules' "$config" >/dev/null || fail "$config is missing ATR provenance"
      grep -Fx '  source_url: https://github.com/Agent-Threat-Rule/agent-threat-rules' "$config" >/dev/null || fail "$config is missing its ATR source_url"
      grep -Fx '  source_revision: 0c7a1f133fc176a732767363db65102aa0aae710' "$config" >/dev/null || fail "$config is missing its pinned ATR revision"
      grep -Eq '^  adaptation_notes: [^[:space:]].*$' "$config" || fail "$config is missing adaptation notes"
      ;;
    *)
      grep -Fx '  source_url: https://github.com/georgestarcher/querysplunk' "$config" >/dev/null || fail "$config is missing its canonical provenance source_url"
      ;;
  esac
  grep -Fx '  license: MIT' "$config" >/dev/null || fail "$config is missing its provenance license"
  grep -Fx '  recommended_file_mode: "0600"' "$config" >/dev/null || fail "$config does not recommend owner-only result files"
  grep -Eq '^  maximum_rows: [1-9][0-9]*$' "$config" || fail "$config is missing a positive result row contract"
done
duplicate_metadata_ids=$(sort "$metadata_ids" | uniq -d)
[ -z "$duplicate_metadata_ids" ] || fail "duplicate bundled metadata IDs: $duplicate_metadata_ids"

skill_dir=".agents/skills/querysplunk"
[ "$(sed -n '1p' "${skill_dir}/SKILL.md")" = "---" ] || fail "skill frontmatter is missing"
grep -Fx 'name: querysplunk' "${skill_dir}/SKILL.md" >/dev/null || fail "skill name frontmatter is invalid"
grep -Eq '^description:[[:space:]]+[^[:space:]].*$' "${skill_dir}/SKILL.md" || fail "skill description frontmatter is invalid"
for required in \
  references/health-diagnostics.md \
  references/ai-agent-detections.md \
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

grep -F '[Project wiki](https://github.com/georgestarcher/querysplunk/wiki)' README.md >/dev/null || fail "README does not link to the project wiki for deeper workflows"
grep -F '[Current release notes](RELEASE_NOTES.md)' README.md >/dev/null || fail "README does not link to the current release notes"
grep -F 'schema_version: "1"' README.md >/dev/null || fail "README does not document schema version 1"
grep -F '## Bundled Search Library' README.md >/dev/null || fail "README does not explain the bundled search library"
grep -F '# querysplunk v2.3.0' RELEASE_NOTES.md >/dev/null || fail "release notes do not identify v2.3.0"
grep -F 'result_handling' RELEASE_NOTES.md >/dev/null || fail "release notes do not describe result handling"
grep -F -- '--notes-file RELEASE_NOTES.md' .github/workflows/release.yml >/dev/null || fail "release workflow does not publish curated release notes"
grep -F 'Never use direct token-bearing `curl`' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference is missing the direct-call safety boundary"
grep -F 'Resolve at most five levels' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference is missing its recursion limit"
grep -F 'complete stanza title including arity' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference does not distinguish macro arity"
grep -F 'Execution time that regularly meets or exceeds that' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference is missing saved-search schedule-overlap guidance"
grep -F 'examples/health/scheduler-health.yml' "${skill_dir}/references/rest-inspection.md" >/dev/null || fail "REST inspection reference does not connect overlap analysis to scheduler health"
grep -F '| table title, search, disabled, is_scheduled, cron_schedule, alert_type, actions, dispatch.earliest_time, dispatch.latest_time, eai:acl.app, eai:acl.owner, eai:acl.sharing' examples/rest/saved-search-definition.yml >/dev/null || fail "saved-search inspection is missing its bounded SPL, schedule, action, or namespace fields"
grep -F 'add_orphan_field=true' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not request Splunk orphan status"
grep -F 'search="orphan=1" search="is_scheduled=1" count=100' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not filter before its finite result bound"
grep -F '| where orphan=1 AND is_scheduled=1' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not select scheduled orphaned searches"
grep -F '| rename eai:acl.app AS app eai:acl.owner AS owner' examples/health/orphaned-scheduled-searches.yml >/dev/null || fail "orphaned-search inspection does not expose simple app and owner fields"
grep -F 'scheduler_message=coalesce(errmsg, reason, "No scheduler message reported")' .agents/skills/querysplunk/templates/recent-search-job-failures.yml >/dev/null || fail "failed-job analysis does not normalize scheduler messages before deduplication"
grep -F 'dedup app savedsearch_name scheduler_message' .agents/skills/querysplunk/templates/recent-search-job-failures.yml >/dev/null || fail "failed-job analysis does not preserve distinct normalized scheduler messages"
grep -F '(authorization:[ ]*bearer|bearer|token|password|secret)[=: ]+' .agents/skills/querysplunk/templates/recent-search-job-failures.yml >/dev/null || fail "failed-job analysis is missing broad credential redaction before AI processing"
grep -F '/configs/conf-macros count=2' examples/rest/macro-definitions.yml >/dev/null || fail "macro inspection does not use the filterable endpoint with ambiguity detection"
grep -F '| inputlookup max=100 example_lookup' examples/rest/lookup-preview.yml >/dev/null || fail "lookup preview does not bound rows at inputlookup"
grep -F '/services/storage/passwords count=0 splunk_server=local strict=true' examples/pentest/stored-credentials.yml >/dev/null || fail "pentest credential-store example does not use the documented local endpoint"
grep -F '| table app, owner, sharing, realm, username, clear_password, encr_password' examples/pentest/stored-credentials.yml >/dev/null || fail "pentest credential-store example does not expose the documented credential fields"
grep -F 'AUTHORIZED SECURITY TESTING ONLY' examples/pentest/stored-credentials.yml >/dev/null || fail "pentest credential-store example is missing its authorization warning"
grep -F 'datamodel=Splunk_Audit.Search_Activity' examples/detections/sensitive-search-activity.yml >/dev/null || fail "sensitive-search detection does not use the audit Search_Activity dataset"
grep -F 'datamodel=Splunk_Audit.Search_Activity' examples/detections/failed-search-activity.yml >/dev/null || fail "failed-search detection does not use the audit Search_Activity dataset"
grep -F 'datamodel=Splunk_Audit.Web_Service_Errors' examples/health/audit-web-service-errors.yml >/dev/null || fail "web-service health search does not use the audit Web_Service_Errors dataset"
grep -F 'datamodel=Splunk_Audit.Modular_Actions' examples/health/audit-failed-modular-actions.yml >/dev/null || fail "modular-action health search does not use the audit Modular_Actions dataset"
grep -F 'BY src, app' examples/pentest/possible-password-paste-by-app.yml >/dev/null || fail "pentest password-paste app example does not correlate by source and app"
grep -F 'BY src, dest' examples/pentest/possible-password-paste-by-dest.yml >/dev/null || fail "pentest password-paste destination example does not correlate by source and destination"
for config in examples/pentest/possible-password-paste-by-app.yml examples/pentest/possible-password-paste-by-dest.yml; do
  grep -F 'last(user) AS failed_user' "${config}" >/dev/null || fail "${config} does not preserve the failed username"
  grep -F 'match(failed_user, "[^A-Za-z0-9]")' "${config}" >/dev/null || fail "${config} does not require a symbol in the failed username"
  grep -F 'match(user, "^[A-Za-z0-9]+$")' "${config}" >/dev/null || fail "${config} does not require an alphanumeric successful username"
  grep -F 'seconds_to_success<=300' "${config}" >/dev/null || fail "${config} does not bound the failure-to-success sequence"
done
for config in examples/pentest/*.yml; do
  grep -Fx '  classification: secret' "$config" >/dev/null || fail "$config does not classify credential-bearing pentest output as secret"
  grep -Fx '  contains_credentials: true' "$config" >/dev/null || fail "$config does not declare credential-bearing output"
  grep -Fx '  agent_display: do_not_display' "$config" >/dev/null || fail "$config does not prohibit raw agent display"
done
for config in examples/detections/*.yml examples/detections/ai-agent/*.yml; do
  grep -Fx '  classification: sensitive' "$config" >/dev/null || fail "$config does not classify detection output as sensitive"
  grep -Fx '  agent_display: summary_only' "$config" >/dev/null || fail "$config does not restrict agent display to a summary"
done
grep -F 'Copyright (c) 2026 ATR Contributors' THIRD_PARTY_NOTICES.md >/dev/null || fail "ATR copyright notice is missing"
grep -F 'MIT License' THIRD_PARTY_NOTICES.md >/dev/null || fail "ATR MIT license notice is missing"
grep -F 'does not independently email' examples/detections/ai-agent/README.md >/dev/null || fail "AI-agent guide does not preserve the ai command boundary"
grep -F 'AI-derived dynamic execution only when' "${skill_dir}/references/ai-agent-detections.md" >/dev/null || fail "AI-agent skill reference does not preserve the action evidence boundary"
if grep -REn '(first|last|earliest|latest)\((_time|[^)]*_epoch)\)' examples --include='*.yml' .agents/skills/querysplunk/templates --include='*.yml'; then
  fail "YAML searches must use numeric min/max aggregation for epoch timestamps"
fi
for config in examples/health/system-messages.yml examples/health/orphaned-scheduled-searches.yml examples/rest/saved-search-definition.yml examples/rest/macro-definitions.yml examples/rest/lookup-definitions.yml; do
  grep -Eq '^[[:space:]]+\| (fields|table) ' "$config" || fail "$config does not project a bounded field set"
done

if rg -n 'v1\.1\.0|## Quick setup|does not have standard YAML frontmatter' README.md INSTALL.md "$skill_dir"; then
  fail "documentation contains stale release or skill language"
fi

echo "documentation, help, examples, and skill QA passed"
