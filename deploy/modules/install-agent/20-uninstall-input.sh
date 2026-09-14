
run_uninstall() {
  case "$service_manager" in
    systemd)
      if [ -e "$service_unit_dir/qagent.service" ]; then
        "$systemctl_cmd" disable qagent.service >/dev/null 2>&1 || true
        "$systemctl_cmd" stop qagent.service >/dev/null 2>&1 || true
        rm -f "$service_unit_dir/qagent.service"
        "$systemctl_cmd" daemon-reload
      fi
      for unit in "$service_unit_dir"/qagent-*.service; do
        [ -e "$unit" ] || continue
        if grep -q '^Description=.* managed by QAgent$' "$unit"; then
          "$systemctl_cmd" disable "${unit##*/}" >/dev/null 2>&1 || true
          "$systemctl_cmd" stop "${unit##*/}" >/dev/null 2>&1 || true
          rm -f "$unit"
        else
          printf '%s\n' "preserved non-QAgent unit: $unit"
        fi
      done
      "$systemctl_cmd" daemon-reload
      ;;
    openrc)
      if [ -e "$openrc_init_dir/qagent" ]; then
        "$rc_service_cmd" qagent stop >/dev/null 2>&1 || true
        rm -f "$openrc_runlevels_root/default/qagent"
        rm -f "$openrc_init_dir/qagent"
      fi
      for service in "$openrc_init_dir"/qagent-*; do
        [ -e "$service" ] || continue
        if grep -q '^# QControlHub managed OpenRC service:' "$service"; then
          "$rc_service_cmd" "${service##*/}" stop >/dev/null 2>&1 || true
          rm -f "$openrc_runlevels_root/default/${service##*/}"
          rm -f "$service"
        else
          printf '%s\n' "preserved non-QAgent OpenRC service: $service"
        fi
      done
      ;;
  esac
  rm -f "$agent_env_file" "$openrc_conf_dir/qagent" "$qagent_bin_link"
  rm -rf "$qagent_bin_dir" "$qagent_etc_dir" "$core_asset_root"
  printf '%s\n' "已卸载 QControlHub Agent；保留节点状态目录 $agent_state_dir，如需彻底清理请手动删除。"
}

if [ "$action" = uninstall ]; then
  run_uninstall
  exit 0
fi

install_nftables
install_iproute2

control="${1:?usage: install-agent.sh install|update <control-plane-url|ip[:port]> <add-node-credential> [agent-name]}"
token="${2:?usage: install-agent.sh install|update <control-plane-url|ip[:port]> <add-node-credential> [agent-name]}"
name_arg="${3:-}"
default_name=$(hostname)
name=${name_arg:-$default_name}
ca_file="${QCH_TLS_CA_FILE:-}"
allow_insecure_live="${QCH_ALLOW_INSECURE_LIVE:-false}"

validate_environment_value QCH_SERVER_URL "$control"
validate_environment_value QCH_ENROLLMENT_TOKEN "$token"
validate_environment_value QCH_AGENT_NAME "$name"
validate_environment_value QCH_TLS_CA_FILE "$ca_file"
validate_environment_value QCH_ALLOW_INSECURE_LIVE "$allow_insecure_live"

case "$control" in
  http://*|https://*|ws://*|wss://*) server_url="$control" ;;
  *) server_url="http://$control" ;;
esac
case "$server_url" in */) server_url=${server_url%/} ;; esac
case "$server_url" in
  ws://*) http_origin="http://${server_url#ws://}" ;;
  wss://*) http_origin="https://${server_url#wss://}" ;;
  http://*|https://*) http_origin="$server_url" ;;
  *) printf '%s\n' 'invalid control-plane URL' >&2; exit 1 ;;
esac
server_host=${server_url#*://}
case "$server_host" in
  ""|*/*|*'?'*|*'#'*|*@*|*'"'*|*\\*) printf '%s\n' 'control-plane URL must be a bare origin' >&2; exit 1 ;;
esac
case "$server_host" in
  *[[:space:]]*) printf '%s\n' 'control-plane URL must not contain whitespace' >&2; exit 1 ;;
esac

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/qcontrolhub-agent.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
repository_dir="$work_dir/qcontrolhub"
mkdir -p "$repository_dir/deploy/$service_manager" "$repository_dir/examples/configs"
