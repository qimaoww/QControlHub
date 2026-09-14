
find_single_active_service() {
  candidates=$1
  label=$2
  active_service_candidate=""
  active_service_count=0
  for candidate in $candidates; do
    if [ "${service_manager:-systemd}" = openrc ] && [ -e "$openrc_init_root/$candidate" ]; then
      protected_directory_chain "$openrc_init_root" && protected_regular_file "$openrc_init_root/$candidate" true || {
        printf '%s\n' "unsafe OpenRC service script: /etc/init.d/$candidate" >&2
        return 2
      }
    fi
    if service_is_active "$candidate"; then
      active_service_count=$((active_service_count + 1))
      active_service_candidate=$candidate
    fi
  done
  [ "$active_service_count" -ne 0 ] || return 1
  if [ "$active_service_count" -ne 1 ]; then
    printf '%s\n' "multiple active $label service candidates cannot be mapped safely" >&2
    return 2
  fi
}

service_is_active() {
  service=$1
  if [ "${service_manager:-systemd}" = openrc ]; then
    protected_directory_chain "$openrc_init_root" || return 1
    protected_regular_file "$openrc_init_root/$service" true || return 1
    rc-service "$service" status >/dev/null 2>&1
  else
    systemctl is-active --quiet "$service" 2>/dev/null
  fi
}

inspect_existing_candidate() {
  engine=$1
  binary=$2
  config=$3
  case "$engine" in
    xray)
      QCH_SERVICE_MANAGER=${service_manager:-systemd} QCH_XRAY_BINARY=$binary QCH_XRAY_CONFIG=$config \
        QCH_XRAY_CONFIG_DIRECTORY=${4:-} \
        "$work_dir/qagent" inspect-existing xray >/dev/null 2>&1
      ;;
    sing-box)
      QCH_SERVICE_MANAGER=${service_manager:-systemd} QCH_SING_BOX_BINARY=$binary QCH_SING_BOX_CONFIG=$config \
        QCH_SING_BOX_CONFIG_DIRECTORY=${4:-} QCH_SING_BOX_WORK_DIRECTORY=${6:-} \
        QCH_SING_BOX_SERVICE_BINARY=${5:-$binary} \
        "$work_dir/qagent" inspect-existing sing-box >/dev/null 2>&1
      ;;
    *) return 1 ;;
  esac
}

discover_existing_xray() {
  find_single_active_service "$xray_service_candidates" Xray || {
    result=$?
    [ "$result" -eq 1 ] && return 1
    return 2
  }
  match_count=0
  found_binary=""
  found_config=""
  found_config_directory=""
  found_service=""
  for service in "$active_service_candidate"; do
    for binary in $xray_binary_candidates; do
      protected_existing_core_file "$binary" || continue
      # The empty candidate is the directory-authoritative form: an installer
      # that runs Xray with only a confdir ships no main file to whitelist.
      for config in "" $xray_config_candidates; do
        if [ -n "$config" ]; then
          protected_regular_file "$config" false || continue
          [ "$(wc -c < "$config")" -le 2097152 ] || continue
        fi
        service_uses_paths "$service" "$binary" "$config" xray || continue
        config_directory=$matched_config_directory
        # Exactly one of the two sources must be present, never neither.
        [ -n "$config" ] || [ -n "$config_directory" ] || continue
        inspect_existing_candidate xray "$binary" "$config" "$config_directory" || continue
        match_count=$((match_count + 1))
        found_binary=$binary
        found_config=$config
        found_config_directory=$config_directory
        found_service=$service
      done
    done
  done
  [ "$match_count" -eq 1 ] || {
    printf '%s\n' 'active Xray service could not be mapped to one validated executable and configuration' >&2
    return 2
  }
  if ! qagent_core_service_is_safe_to_disable xray; then
    printf '%s\n' 'refusing installation while the managed qagent-xray service is active or ambiguous' >&2
    return 2
  fi
  mapped_xray_binary=$found_binary
  mapped_xray_config=$found_config
  mapped_xray_config_directory=$found_config_directory
  mapped_xray_service=$found_service
  mapped_engines=$(append_csv "$mapped_engines" xray)
  printf '%s\n' "detected existing Xray service: $found_service (${found_config:-$found_config_directory})"
}

discover_existing_singbox() {
  find_single_active_service "$singbox_service_candidates" sing-box || {
    result=$?
    [ "$result" -eq 1 ] && return 1
    return 2
  }
  match_count=0
  found_binary=""
  found_config=""
  found_config_directory=""
  found_work_directory=""
  found_service_binary=""
  found_service=""
  for service in "$active_service_candidate"; do
    for service_binary in $singbox_binary_candidates; do
      case " $singbox_direct_binary_candidates " in
        *" $service_binary "*) [ ! -L "$service_binary" ] || continue ;;
      esac
      resolved_binary=$(resolve_fixed_singbox_binary "$service_binary") || continue
      for config in $singbox_config_candidates; do
        protected_regular_file "$config" false || continue
        [ "$(wc -c < "$config")" -le 2097152 ] || continue
        service_uses_paths "$service" "$service_binary" "$config" sing-box "$resolved_binary" || continue
        config_directory=$matched_config_directory
        work_directory=$matched_work_directory
        inspect_existing_candidate sing-box "$resolved_binary" "$config" "$config_directory" "$service_binary" "$work_directory" || continue
        match_count=$((match_count + 1))
        found_binary=$resolved_binary
        found_config=$config
        found_config_directory=$config_directory
        found_work_directory=$work_directory
        found_service_binary=$service_binary
        found_service=$service
      done
    done
  done
  [ "$match_count" -eq 1 ] || {
    printf '%s\n' 'active sing-box service could not be mapped to one validated executable and configuration' >&2
    return 2
  }
  if ! qagent_core_service_is_safe_to_disable sing-box; then
    printf '%s\n' 'refusing installation while the managed qagent-sing-box service is active or ambiguous' >&2
    return 2
  fi
  mapped_singbox_binary=$found_binary
  mapped_singbox_config=$found_config
  mapped_singbox_config_directory=$found_config_directory
  mapped_singbox_work_directory=$found_work_directory
  mapped_singbox_service_binary=$found_service_binary
  mapped_singbox_service=$found_service
  mapped_engines=$(append_csv "$mapped_engines" sing-box)
  printf '%s\n' "detected existing sing-box service: $found_service ($found_config)"
}
