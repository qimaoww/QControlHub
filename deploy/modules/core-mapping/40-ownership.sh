
qagent_core_service_is_safe_owned() {
  engine=$1
  if [ "${service_manager:-systemd}" = openrc ]; then
    service="qagent-$engine"
    expected_fragment=${2:-$openrc_init_root/$service}
    [ -e "$expected_fragment" ] || return 0
    [ ! -L "$expected_fragment" ] && [ -f "$expected_fragment" ] || return 1
    protected_directory_chain "$openrc_init_root" || return 1
    protected_regular_file "$expected_fragment" true || return 1
    grep -q "^# QControlHub managed OpenRC service: $service$" "$expected_fragment" || return 1
    return 0
  fi
  service="qagent-$engine.service"
  expected_fragment=${2:-/etc/systemd/system/$service}
  load_state=$(systemctl show "$service" --property=LoadState --value 2>/dev/null) || return 1
  [ "$load_state" = not-found ] && return 0
  [ "$load_state" = loaded ] || return 1
  active_state=$(systemctl show "$service" --property=ActiveState --value 2>/dev/null) || return 1
  case "$active_state" in active|inactive|failed) ;; *) return 1 ;; esac
  fragment_path=$(systemctl show "$service" --property=FragmentPath --value 2>/dev/null) || return 1
  [ "$fragment_path" = "$expected_fragment" ] || return 1
  protected_regular_file "$fragment_path" false || return 1
  expected_environment=""
  case "$engine" in
    xray)
      expected_binary=$qagent_xray_binary
      expected_config=$qagent_xray_config
      expected_argv="$expected_binary run -config $expected_config"
      expected_description='Xray core managed by QAgent'
      ;;
    sing-box)
      expected_binary=$qagent_singbox_binary
      expected_config=$qagent_singbox_config
      expected_argv="$expected_binary run -c $expected_config"
      expected_description='sing-box core managed by QAgent'
      ;;
    shadowsocks-rust)
      expected_binary=$qagent_ssrust_binary
      expected_config=$qagent_ssrust_config
      expected_argv="$expected_binary -c $expected_config --acl $qagent_ssrust_acl"
      expected_description='Shadowsocks Rust core managed by QAgent'
      expected_environment='RUST_LOG=info,shadowsocks_service::server::tcprelay=debug,shadowsocks_service::server::udprelay=debug'
      # Match the same two exact QAgent commands accepted by Agent discovery.
      # Older managed units predate the dedicated --acl argument. Bootstrap
      # must recognize those units before replacing them with the new template,
      # including when a partial import left the old managed service active.
      legacy_argv="$expected_binary -c $expected_config"
      if [ "$(grep -c "^ExecStart=$legacy_argv$" "$fragment_path")" -eq 1 ]; then
        expected_argv=$legacy_argv
      fi
      ;;
    *) return 1 ;;
  esac
  [ "$(grep -c "^Description=$expected_description$" "$fragment_path")" -eq 1 ] || return 1
  [ "$(grep -c '^User=qcontrolhub-core$' "$fragment_path")" -eq 1 ] || return 1
  [ "$(grep -c '^Group=qcontrolhub-core$' "$fragment_path")" -eq 1 ] || return 1
  [ "$(grep -c "^ExecStart=$expected_argv$" "$fragment_path")" -eq 1 ] || return 1
  exec_start=$(systemctl show "$service" --property=ExecStart --value 2>/dev/null) || return 1
  parsed=$(single_exec_start_argv "$exec_start") || return 1
  parsed_executable=$(printf '%s\n' "$parsed" | sed -n '1p')
  parsed_argv=$(printf '%s\n' "$parsed" | sed -n '2p')
  [ "$parsed_executable" = "$expected_binary" ] || return 1
  [ "$parsed_argv" = "$expected_argv" ] || return 1
  [ "$(systemctl show "$service" --property=Description --value 2>/dev/null)" = "$expected_description" ] || return 1
  [ "$(systemctl show "$service" --property=User --value 2>/dev/null)" = qcontrolhub-core ] || return 1
  [ "$(systemctl show "$service" --property=Group --value 2>/dev/null)" = qcontrolhub-core ] || return 1
  [ "$(systemctl show "$service" --property=Type --value 2>/dev/null)" = simple ] || return 1
  [ "$(systemctl show "$service" --property=WorkingDirectory --value 2>/dev/null)" = "/var/lib/qcontrolhub-$engine" ] || return 1
  actual_environment=$(systemctl show "$service" --property=Environment --value 2>/dev/null) || return 1
  if [ "$engine" = shadowsocks-rust ] && [ "$actual_environment" = RUST_LOG=info ]; then
    : # Exact historical QAgent log policy; safe to upgrade.
  else
    [ "$actual_environment" = "$expected_environment" ] || return 1
  fi
  for property in RootDirectory RootImage BindPaths BindReadOnlyPaths EnvironmentFiles; do
    [ -z "$(systemctl show "$service" --property="$property" --value 2>/dev/null)" ] || return 1
  done
  for hook in ExecCondition ExecStartPre ExecStartPost ExecReload ExecStop ExecStopPost; do
    [ -z "$(systemctl show "$service" --property="$hook" --value 2>/dev/null)" ] || return 1
  done
  drop_in_paths=$(systemctl show "$service" --property=DropInPaths --value 2>/dev/null) || return 1
  for drop_in in $drop_in_paths; do
    case "$drop_in" in
      */10-qcontrolhub-bind-low-ports.conf)
        [ "$(cat "$drop_in" 2>/dev/null)" = "$(printf '%s\n' '[Service]' 'CapabilityBoundingSet=CAP_NET_BIND_SERVICE' 'AmbientCapabilities=CAP_NET_BIND_SERVICE')" ] || return 1
        ;;
      */20-qcontrolhub-volatile-logs.conf)
        [ "$(cat "$drop_in" 2>/dev/null)" = "$(printf '%s\n' '[Service]' 'LogNamespace=qagent-cores' 'StandardOutput=journal' 'StandardError=journal')" ] || return 1
        ;;
      */30-qcontrolhub-ss-rust-logs.conf)
        [ "$engine" = shadowsocks-rust ] || return 1
        [ "$drop_in" = "$fragment_path.d/30-qcontrolhub-ss-rust-logs.conf" ] || return 1
        protected_directory_chain "$fragment_path.d" || return 1
        protected_regular_file "$drop_in" false || return 1
        [ "$(cat "$drop_in" 2>/dev/null)" = "$(printf '%s\n' '[Service]' "Environment=$expected_environment")" ] || return 1
        ;;
      *) return 1 ;;
    esac
  done
}

qagent_core_service_is_safe_to_disable() {
  engine=$1
  if [ "${service_manager:-systemd}" = openrc ]; then
    service="qagent-$engine"
    expected_fragment=${2:-$openrc_init_root/$service}
    [ -e "$expected_fragment" ] || return 0
    [ ! -L "$expected_fragment" ] && [ -f "$expected_fragment" ] || return 1
    protected_directory_chain "$openrc_init_root" || return 1
    protected_regular_file "$expected_fragment" true || return 1
    grep -q "^# QControlHub managed OpenRC service: $service$" "$expected_fragment" || return 1
    if rc-service "$service" status >/dev/null 2>&1; then return 1; fi
    return
  fi
  service="qagent-$engine.service"
  expected_fragment=${2:-/etc/systemd/system/$service}
  qagent_core_service_is_safe_owned "$engine" "$expected_fragment" || return 1
  load_state=$(systemctl show "$service" --property=LoadState --value 2>/dev/null) || return 1
  [ "$load_state" = not-found ] && return 0
  [ "$load_state" = loaded ] || return 1
  active_state=$(systemctl show "$service" --property=ActiveState --value 2>/dev/null) || return 1
  case "$active_state" in inactive|failed) ;; *) return 1 ;; esac
  fragment_path=$(systemctl show "$service" --property=FragmentPath --value 2>/dev/null) || return 1
  [ "$fragment_path" = "$expected_fragment" ] || return 1
  protected_regular_file "$fragment_path" false || return 1
  grep -q '^Description=.* managed by QAgent$' "$fragment_path"
}

skip_core_service() {
  engine=$1
  case ",${QCH_SKIP_CORE_SERVICES:-}," in
    *",$engine,"*) return 0 ;;
    *) return 1 ;;
  esac
}

# Import has already validated this installed unit. Recheck ownership, but
# leave its exact command and enablement for the durable migration transaction.
require_preserved_core_service_safe() {
  engine=$1
  skip_core_service "$engine" || return 1
  if [ "${service_manager:-systemd}" = openrc ]; then
    expected_fragment=${2:-$openrc_init_root/qagent-$engine}
  else
    expected_fragment=${2:-/etc/systemd/system/qagent-$engine.service}
    [ "$(systemctl show "qagent-$engine.service" --property=LoadState --value 2>/dev/null)" = loaded ] || return 1
  fi
  [ -f "$expected_fragment" ] && [ ! -L "$expected_fragment" ] || return 1
  qagent_core_service_is_safe_owned "$engine" "$expected_fragment"
}

require_skipped_core_service_inactive() {
  engine=$1
  skip_core_service "$engine" || return 0
  if [ "${service_manager:-systemd}" = openrc ]; then service="qagent-$engine"; else service="qagent-$engine.service"; fi
  if service_is_active "$service"; then
    if [ -n "${2:-}" ]; then
      qagent_core_service_is_safe_owned "$engine" "$2" || {
        printf '%s\n' "refusing to alter active unrecognized $service while mapping another service" >&2
        return 1
      }
    else
      qagent_core_service_is_safe_owned "$engine" || {
        printf '%s\n' "refusing to alter active unrecognized $service while mapping another service" >&2
        return 1
      }
    fi
  fi
}

disable_skipped_core_service() {
  engine=$1
  if [ "${service_manager:-systemd}" = openrc ]; then
    expected_fragment=${2:-$openrc_init_root/qagent-$engine}
    service="qagent-$engine"
    require_skipped_core_service_inactive "$engine" "$expected_fragment" || return 1
    qagent_core_service_is_safe_to_disable "$engine" "$expected_fragment" || {
      printf '%s\n' "refusing to disable an unrecognized $service" >&2
      return 1
    }
    for runlevel_link in /etc/runlevels/*/"$service"; do
      [ -e "$runlevel_link" ] || [ -L "$runlevel_link" ] || continue
      [ -L "$runlevel_link" ] || {
        printf '%s\n' "refusing non-symlinked OpenRC runlevel entry: $runlevel_link" >&2
        return 1
      }
      runlevel=$(basename -- "$(dirname -- "$runlevel_link")")
      rc-update del "$service" "$runlevel" >/dev/null 2>&1 || return 1
    done
    for runlevel_link in /etc/runlevels/*/"$service"; do
      [ ! -e "$runlevel_link" ] && [ ! -L "$runlevel_link" ] || {
        printf '%s\n' "$service remains enabled after rc-update del" >&2
        return 1
      }
    done
    require_skipped_core_service_inactive "$engine" "$expected_fragment"
    return
  fi
  expected_fragment=${2:-/etc/systemd/system/qagent-$engine.service}
  service="qagent-$engine.service"
  require_skipped_core_service_inactive "$engine" "$expected_fragment" || return 1
  if systemctl is-active --quiet "$service" 2>/dev/null; then
    printf '%s\n' "kept safely owned active $service pending explicit import" >&2
    return 0
  fi
  qagent_core_service_is_safe_to_disable "$engine" "$expected_fragment" || {
    printf '%s\n' "refusing to disable an unrecognized $service" >&2
    return 1
  }
  systemctl disable "$service" >/dev/null 2>&1 || {
    printf '%s\n' "failed to remove persistent enablement for $service" >&2
    return 1
  }
  systemctl disable --runtime "$service" >/dev/null 2>&1 || {
    printf '%s\n' "failed to remove runtime enablement for $service" >&2
    return 1
  }
  enable_state=$(systemctl is-enabled "$service" 2>/dev/null || true)
  [ "$enable_state" = disabled ] || {
    printf '%s\n' "$service remains enabled after disable (state: ${enable_state:-unknown})" >&2
    return 1
  }
  require_skipped_core_service_inactive "$engine" "$expected_fragment"
}
