
echo '== 4/6 安装 agent 二进制并写入环境文件 =='
if [ -x "$qagent_bin_dir/qagent" ] || [ -f "$agent_env_file" ] || \
   [ -e "$service_unit_dir/qagent.service" ] || [ -e "$openrc_init_dir/qagent" ]; then
  if [ "$action" = update ]; then
    echo '更新已有 QControlHub Agent（保留可复用的自定义配置）。'
  elif [ "$action" = migrate ]; then
    echo '迁移到新的控制面板（保留已装内核与配置，仅重新注册身份）。'
  else
    echo '检测到已有 QControlHub Agent；本次按覆盖升级处理，保留可复用的自定义配置。'
  fi
fi

# 本次安装由脚本显式管理的配置键：这些始终以本次为准，不继承上次的值。
managed_env_keys="QCH_SERVER_URL QCH_ENROLLMENT_TOKEN QCH_REENROLL QCH_TLS_CA_FILE QCH_ALLOW_INSECURE_LIVE QCH_ALLOW_HTTP QCH_SERVICE_MANAGER QCH_EXISTING_XRAY_BINARY QCH_EXISTING_XRAY_CONFIG QCH_EXISTING_XRAY_CONFIG_DIRECTORY QCH_EXISTING_XRAY_SERVICE QCH_EXISTING_SING_BOX_BINARY QCH_EXISTING_SING_BOX_CONFIG QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY QCH_EXISTING_SING_BOX_WORK_DIRECTORY QCH_EXISTING_SING_BOX_SERVICE_BINARY QCH_EXISTING_SING_BOX_SERVICE"

install -d -o root -g root -m 0700 "$agent_conf_dir"
install -d -o root -g root -m 0755 "$qagent_bin_dir"
[ ! -L "$qagent_bin_dir/qagent" ] || { printf '%s\n' "refusing symlinked Agent binary: $qagent_bin_dir/qagent" >&2; exit 1; }
[ ! -e "$qagent_bin_dir/qagent" ] || [ -f "$qagent_bin_dir/qagent" ] || {
  printf '%s\n' "refusing non-regular Agent binary: $qagent_bin_dir/qagent" >&2
  exit 1
}
install -m 0755 "$work_dir/qagent" "$qagent_bin_dir/qagent"
[ ! -e "$qagent_bin_link" ] || [ -L "$qagent_bin_link" ] || {
  printf '%s\n' "refusing to overwrite non-symlink compatibility link: $qagent_bin_link" >&2
  exit 1
}
ln -sfn "$qagent_bin_dir/qagent" "$qagent_bin_link"

# 读取上次安装留下的可复用配置。只继承非本次管理的 QCH_* 键，便于在重复
# 安装时保留运维自行追加的探测/标签/心跳等设置，而不是把整份环境重置。
existing_name=""
existing_labels=""
existing_engines=""
existing_state=""
existing_server=""
inherited_env="$work_dir/qagent.inherited.env"
: > "$inherited_env"
read_existing_agent_env() {
  [ -r "$agent_env_file" ] || return 0
  while IFS='=' read -r environment_key environment_value; do
    case "$environment_key" in
      QCH_SERVER_URL) existing_server=$environment_value; continue ;;
      QCH_AGENT_NAME) existing_name=$environment_value; continue ;;
      QCH_AGENT_LABELS) existing_labels=$environment_value; continue ;;
      QCH_AGENT_ENGINES) existing_engines=$environment_value; continue ;;
      QCH_AGENT_STATE) existing_state=$environment_value; continue ;;
    esac
    case "$environment_key" in QCH_*) ;; *) continue ;; esac
    case " $managed_env_keys " in *" $environment_key "*) continue ;; esac
    printf '%s=%s\n' "$environment_key" "$environment_value" >> "$inherited_env"
  done < "$agent_env_file"
}
read_existing_agent_env

final_name=${name_arg:-${existing_name:-$default_name}}
final_labels=${existing_labels:-region=cn-east}
final_engines=${existing_engines:-mihomo,xray,sing-box,ss-rust}
final_state=${existing_state:-$agent_state_file}
validate_environment_value QCH_AGENT_NAME "$final_name"
validate_environment_value QCH_AGENT_LABELS "$final_labels"
validate_environment_value QCH_AGENT_ENGINES "$final_engines"
validate_environment_value QCH_AGENT_STATE "$final_state"

install -d -o root -g root -m 0755 "$(dirname "$final_state")"
reenroll_required=false
if [ "$action" = migrate ] || { [ -n "$existing_server" ] && [ "$existing_server" != "$server_url" ]; }; then
  reenroll_required=true
fi
previous_state_present=false
previous_private_key=""
if [ -s "$final_state" ]; then
  previous_state_present=true
  previous_private_key=$(sed -n 's/.*"private_key":"\([^"]*\)".*/\1/p' "$final_state" | head -n 1)
fi
umask 077
{
  printf '%s\n' "QCH_SERVER_URL=$server_url"
  if [ -n "$ca_file" ]; then printf '%s\n' "QCH_TLS_CA_FILE=$ca_file"; fi
  case "$server_url" in
    http://*|ws://*) printf '%s\n' 'QCH_ALLOW_HTTP=true' ;;
  esac
  printf '%s\n' "QCH_ALLOW_INSECURE_LIVE=$allow_insecure_live"
  printf '%s\n' "QCH_ENROLLMENT_TOKEN=$token"
  printf '%s\n' "QCH_AGENT_NAME=$final_name"
  printf '%s\n' "QCH_AGENT_LABELS=$final_labels"
  printf '%s\n' "QCH_AGENT_STATE=$final_state"
  printf '%s\n' "QCH_AGENT_ENGINES=$final_engines"
  printf '%s\n' "QCH_SERVICE_MANAGER=$service_manager"
  if [ -n "$mapped_xray_config" ] || [ -n "$mapped_xray_config_directory" ]; then
    printf '%s\n' \
      "QCH_EXISTING_XRAY_BINARY=$mapped_xray_binary" \
      "QCH_EXISTING_XRAY_CONFIG=$mapped_xray_config" \
      "QCH_EXISTING_XRAY_CONFIG_DIRECTORY=$mapped_xray_config_directory" \
      "QCH_EXISTING_XRAY_SERVICE=$mapped_xray_service"
  fi
  if [ -n "$mapped_singbox_config" ]; then
    printf '%s\n' \
      "QCH_EXISTING_SING_BOX_BINARY=$mapped_singbox_binary" \
      "QCH_EXISTING_SING_BOX_CONFIG=$mapped_singbox_config" \
      "QCH_EXISTING_SING_BOX_CONFIG_DIRECTORY=$mapped_singbox_config_directory" \
      "QCH_EXISTING_SING_BOX_SERVICE_BINARY=$mapped_singbox_service_binary" \
      "QCH_EXISTING_SING_BOX_SERVICE=$mapped_singbox_service"
    if [ -n "$mapped_singbox_work_directory" ]; then
      printf '%s\n' "QCH_EXISTING_SING_BOX_WORK_DIRECTORY=$mapped_singbox_work_directory"
    fi
  fi
  if [ -s "$inherited_env" ]; then cat "$inherited_env"; fi
} > "$agent_env_file"
chmod 0600 "$agent_env_file"

openrc_conf="$openrc_conf_dir/qagent"
