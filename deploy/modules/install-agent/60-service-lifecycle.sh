write_openrc_environment() {
  : > "$work_dir/qagent.openrc.conf"
  while IFS='=' read -r environment_key environment_value; do
    case "$environment_key" in
      QCH_*) case "$environment_key" in *[!A-Z0-9_]*) printf '%s\n' "refusing invalid Agent environment key: $environment_key" >&2; exit 1 ;; esac ;;
      *) printf '%s\n' "refusing invalid Agent environment key: $environment_key" >&2; exit 1 ;;
    esac
    escaped_value=$(printf '%s' "$environment_value" | sed "s/'/'\\\\''/g")
    printf "export %s='%s'\n" "$environment_key" "$escaped_value" >> "$work_dir/qagent.openrc.conf"
  done < "$agent_env_file"
  install -d -o root -g root -m 0755 "$openrc_conf_dir"
  install -o root -g root -m 0600 "$work_dir/qagent.openrc.conf" "$openrc_conf"
}

install_managed_service() {
  source_file=$1
  destination=$2
  mode=$3
  marker=$4
  [ ! -L "$destination" ] || { printf '%s\n' "refusing symlinked managed service: $destination" >&2; exit 1; }
  if [ -e "$destination" ]; then
    [ -f "$destination" ] || { printf '%s\n' "refusing non-regular managed service: $destination" >&2; exit 1; }
    if ! grep -q "$marker" "$destination"; then
      printf '%s\n' "refusing to overwrite non-QAgent service: $destination" >&2
      exit 1
    fi
    if cmp -s "$source_file" "$destination"; then
      printf '%s\n' "managed service already current: $destination"
      return
    fi
  fi
  install -o root -g root -m "$mode" "$source_file" "$destination"
  printf '%s\n' "installed managed service: $destination"
}

ensure_openrc_enabled() {
  service_name=$1
  if [ -e "$openrc_runlevels_root/default/$service_name" ]; then
    printf '%s\n' "openrc service already enabled: $service_name"
    return
  fi
  "$rc_update_cmd" add "$service_name" default >/dev/null
}

echo "== 5/6 安装 $service_manager 服务 =="
if [ "$service_manager" = openrc ]; then
  install_managed_service "$repository_dir/deploy/openrc/qagent" "$openrc_init_dir/qagent" 0755 '^# QControlHub managed OpenRC service:'
  write_openrc_environment
  ensure_openrc_enabled qagent
else
  install_managed_service "$repository_dir/deploy/systemd/qagent.service" "$service_unit_dir/qagent.service" 0644 '^Description=QControlHub remote engine agent$'
  "$systemctl_cmd" daemon-reload
fi

echo '== 6/6 启动 agent =='
if [ "$service_manager" = openrc ]; then
  if "$rc_service_cmd" qagent status >/dev/null 2>&1; then "$rc_service_cmd" qagent restart; else "$rc_service_cmd" qagent start; fi
else
  "$systemctl_cmd" enable qagent.service >/dev/null
  # restart also starts an inactive unit and guarantees repeated installation
  # replaces the running process with the freshly downloaded binary.
  "$systemctl_cmd" restart qagent.service
fi
sleep 3
if [ "$service_manager" = openrc ]; then
  "$rc_service_cmd" qagent status
else
  "$systemctl_cmd" --no-pager status qagent.service | head -n 10
fi
