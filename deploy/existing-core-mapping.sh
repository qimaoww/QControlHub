#!/bin/sh

mapped_engines=""
mapped_xray_binary=""
mapped_xray_config=""
mapped_xray_service=""
mapped_singbox_binary=""
mapped_singbox_config=""
mapped_singbox_config_directory=""
mapped_singbox_work_directory=""
mapped_singbox_service_binary=""
mapped_singbox_service=""

# These paths are part of the managed systemd unit contract. Keep them in the
# shared mapping library because both the remote installer and the bootstrap
# script source it independently. When a fresh node already runs Xray or
# sing-box, bootstrap installs the corresponding inactive QAgent unit and then
# immediately performs the full ownership check, so every expected path must
# already be initialized under `set -u`.
qagent_xray_binary=/usr/local/lib/qagent/cores/xray
qagent_xray_config=/etc/qagent/xray/config.json
qagent_singbox_binary=/usr/local/lib/qagent/cores/sing-box
qagent_singbox_config=/etc/qagent/sing-box/config.json

openrc_init_root=${OPENRC_INIT_ROOT:-/etc/init.d}
openrc_state_root=${OPENRC_STATE_ROOT:-/run/openrc}
openrc_run_root=${OPENRC_RUN_ROOT:-/run}
openrc_supervisor_executable=${OPENRC_SUPERVISOR_EXECUTABLE:-/sbin/supervise-daemon}
proc_root=${QCH_PROC_ROOT:-/proc}

case "${service_manager:-systemd}" in
  openrc)
    xray_service_candidates="xray"
    singbox_service_candidates="sing-box singbox"
    ;;
  *)
    xray_service_candidates="xray.service"
    singbox_service_candidates="sing-box.service singbox.service"
    ;;
esac
xray_binary_candidates="/usr/local/bin/xray /usr/bin/xray"
xray_config_candidates="/usr/local/etc/xray/config.json /etc/xray/config.json"
singbox_binary_candidates="/usr/local/bin/sing-box /usr/bin/sing-box /etc/sing-box/bin/sing-box"
singbox_direct_binary_candidates="/etc/sing-box/bin/sing-box"
singbox_config_candidates="/etc/sing-box/config.json /usr/local/etc/sing-box/config.json"

append_csv() {
  current=$1
  value=$2
  if [ -n "$current" ]; then
    printf '%s,%s' "$current" "$value"
  else
    printf '%s' "$value"
  fi
}

# Resolve the single child process that an OpenRC supervise-daemon provably owns
# for the named service. Prints "supervisor_pid child_pid" on success and fails
# closed for any layout that is not backed by protected supervise-daemon state.
# Read /proc/<pid>/stat with the leading "pid (comm) " stripped so the positional
# fields line up with the Go parser: $2 is ppid and ${20} is starttime. Prints
# "ppid starttime" and fails closed on malformed input. ${20} (not $20) is
# required because POSIX sh parses $20 as ${2}0.
proc_stat_identity() {
  pid_dir=$1
  proc_stat=$(sed 's/^.*) //' "$pid_dir/stat" 2>/dev/null) || return 1
  set -- $proc_stat
  [ $# -ge 20 ] || return 1
  [ -n "$2" ] && [ -n "${20}" ] || return 1
  case "$2" in *[!0-9]*) return 1 ;; esac
  case "${20}" in *[!0-9]*) return 1 ;; esac
  printf '%s %s\n' "$2" "${20}"
}

openrc_supervised_child_pid() {
  service=$1
  [ -n "$service" ] || return 1
  case "$service" in *[!A-Za-z0-9_-]*) return 1 ;; esac
  protected_directory_chain "$openrc_init_root" || return 1
  protected_regular_file "$openrc_init_root/$service" true || return 1
  rc-service "$service" status >/dev/null 2>&1 || return 1
  options_dir="$openrc_state_root/options/$service"
  protected_directory_chain "$options_dir" || return 1
  protected_regular_file "$options_dir/child_pid" false || return 1
  child_pid=$(cat "$options_dir/child_pid") || return 1
  case "$child_pid" in *[!0-9]*) return 1 ;; esac
  [ "$child_pid" -gt 1 ] || return 1
  child_pid_stamp=$(stat -c '%d:%i' "$options_dir/child_pid" 2>/dev/null) || return 1
  protected_regular_file "$options_dir/pidfile" false || return 1
  pidfile_value=$(cat "$options_dir/pidfile") || return 1
  case "$pidfile_value" in
    "$openrc_run_root/supervise-$service.pid"|"/var/run/supervise-$service.pid") ;;
    *) return 1 ;;
  esac
  pidfile_stamp=$(stat -c '%d:%i' "$options_dir/pidfile" 2>/dev/null) || return 1
  supervisor_pidfile="$openrc_run_root/supervise-$service.pid"
  protected_directory_chain "$(dirname -- "$supervisor_pidfile")" || return 1
  protected_regular_file "$supervisor_pidfile" false || return 1
  supervisor_pid=$(cat "$supervisor_pidfile") || return 1
  case "$supervisor_pid" in *[!0-9]*) return 1 ;; esac
  [ "$supervisor_pid" -gt 1 ] || return 1
  supervisor_pid_stamp=$(stat -c '%d:%i' "$supervisor_pidfile" 2>/dev/null) || return 1
  supervisor_identity=$(proc_stat_identity "$proc_root/$supervisor_pid") || return 1
  set -- $supervisor_identity
  supervisor_ppid=$1
  supervisor_starttime=$2
  # The supervisor must itself be the protected OpenRC helper invoked for this
  # service, not merely a same-named binary that is executing elsewhere.
  supervisor_exe=$(readlink "$proc_root/$supervisor_pid/exe" 2>/dev/null) || return 1
  [ -n "$supervisor_exe" ] || return 1
  case "$openrc_supervisor_executable" in /*) ;; *) return 1 ;; esac
  protected_directory_chain "$(dirname -- "$openrc_supervisor_executable")" || return 1
  protected_regular_file "$openrc_supervisor_executable" true || return 1
  case "$supervisor_exe" in
    "$openrc_supervisor_executable") ;;
    *) return 1 ;;
  esac
  supervisor_cmdline=$(tr '\000' ' ' < "$proc_root/$supervisor_pid/cmdline" 2>/dev/null) || return 1
  case " $supervisor_cmdline " in *" $service "*) ;; *) return 1 ;; esac
  case " $supervisor_cmdline " in *" --start "*) ;; *) return 1 ;; esac
  supervisor_identity_again=$(proc_stat_identity "$proc_root/$supervisor_pid") || return 1
  set -- $supervisor_identity_again
  [ "$supervisor_ppid" = "$1" ] || return 1
  [ "$supervisor_starttime" = "$2" ] || return 1
  child_identity=$(proc_stat_identity "$proc_root/$child_pid") || return 1
  set -- $child_identity
  [ "$1" -eq "$supervisor_pid" ] || return 1
  child_starttime=$2
  child_identity_again=$(proc_stat_identity "$proc_root/$child_pid") || return 1
  set -- $child_identity_again
  [ "$1" -eq "$supervisor_pid" ] || return 1
  [ "$child_starttime" = "$2" ] || return 1
  # Re-read the metadata files and their identities to fail closed on a swap
  # between the first read and the process identity verification.
  child_pid_again=$(cat "$options_dir/child_pid") || return 1
  pidfile_value_again=$(cat "$options_dir/pidfile") || return 1
  supervisor_pid_again=$(cat "$supervisor_pidfile") || return 1
  [ "$child_pid_again" = "$child_pid" ] || return 1
  [ "$pidfile_value_again" = "$pidfile_value" ] || return 1
  [ "$supervisor_pid_again" = "$supervisor_pid" ] || return 1
  [ "$(stat -c '%d:%i' "$options_dir/child_pid" 2>/dev/null)" = "$child_pid_stamp" ] || return 1
  [ "$(stat -c '%d:%i' "$options_dir/pidfile" 2>/dev/null)" = "$pidfile_stamp" ] || return 1
  [ "$(stat -c '%d:%i' "$supervisor_pidfile" 2>/dev/null)" = "$supervisor_pid_stamp" ] || return 1
  printf '%s %s\n' "$supervisor_pid" "$child_pid"
}

protected_regular_file() {
  candidate=$1
  executable=${2:-false}
  [ -f "$candidate" ] && [ ! -L "$candidate" ] || return 1
  [ "$(stat -c '%u' "$candidate" 2>/dev/null)" = 0 ] || return 1
  permissions=$(stat -c '%a' "$candidate" 2>/dev/null) || return 1
  [ $((0$permissions & 022)) -eq 0 ] || return 1
  if [ "$executable" = true ]; then [ -x "$candidate" ] || return 1; fi
  parent=$(dirname -- "$candidate")
  [ -d "$parent" ] && [ ! -L "$parent" ] || return 1
  [ "$(stat -c '%u' "$parent" 2>/dev/null)" = 0 ] || return 1
  permissions=$(stat -c '%a' "$parent" 2>/dev/null) || return 1
  [ $((0$permissions & 022)) -eq 0 ]
}

protected_directory_chain() {
  chain_directory=$1
  case "$chain_directory" in /*) ;; *) return 1 ;; esac
  while :; do
    [ -d "$chain_directory" ] && [ ! -L "$chain_directory" ] || return 1
    chain_permissions=$(stat -c '%a' "$chain_directory" 2>/dev/null) || return 1
    chain_mode=$((0$chain_permissions))
    if [ $((chain_mode & 022)) -ne 0 ]; then
      [ $((chain_mode & 01000)) -ne 0 ] || return 1
      [ "$(stat -c '%u' "$chain_directory" 2>/dev/null)" = 0 ] || return 1
      return 0
    fi
    [ "$(stat -c '%u' "$chain_directory" 2>/dev/null)" = 0 ] || return 1
    [ "$chain_directory" != / ] || return 0
    chain_directory=$(dirname -- "$chain_directory")
  done
}

protected_config_directory() {
  config_directory=$1
  primary=$2
  protected_directory_chain "$config_directory" || return 1
  total=$(wc -c < "$primary") || return 1
  for config_candidate in "$config_directory"/*.json; do
    [ -e "$config_candidate" ] || continue
    protected_regular_file "$config_candidate" false || return 1
    size=$(wc -c < "$config_candidate") || return 1
    total=$((total + size))
    [ "$total" -le 2097152 ] || return 1
  done
}

resolve_fixed_singbox_binary() {
  service_binary=$1
  if [ ! -L "$service_binary" ]; then
    protected_directory_chain "$(dirname -- "$service_binary")" || return 1
    protected_regular_file "$service_binary" true || return 1
    [ "$(head -c 2 "$service_binary" 2>/dev/null)" != '#!' ] || return 1
    printf '%s\n' "$service_binary"
    return 0
  fi
  protected_directory_chain "$(dirname -- "$service_binary")" || return 1
  wrapper=$(readlink -- "$service_binary" 2>/dev/null) || return 1
  case "$wrapper" in /*) ;; *) wrapper=$(dirname -- "$service_binary")/$wrapper ;; esac
  wrapper_parent=$(cd -P -- "$(dirname -- "$wrapper")" 2>/dev/null && pwd -P) || return 1
  wrapper=$wrapper_parent/$(basename -- "$wrapper")
  [ ! -L "$wrapper" ] || return 1
  protected_directory_chain "$(dirname -- "$wrapper")" || return 1
  protected_regular_file "$wrapper" true || return 1
  if [ "$(head -c 2 "$wrapper" 2>/dev/null)" != '#!' ]; then
    printf '%s\n' "$wrapper"
    return 0
  fi
  first=$(sed -n '1p' "$wrapper")
  second=$(sed -n '2p' "$wrapper")
  third=$(sed -n '3p' "$wrapper")
  [ "$first" = '#!/bin/sh' ] && [ -z "$third" ] || return 1
  prefix='exec '
  suffix=' "$@"'
  case "$second" in "$prefix"*"$suffix") ;; *) return 1 ;; esac
  real_binary=${second#"$prefix"}
  real_binary=${real_binary%"$suffix"}
  case "$real_binary" in /*) ;; *) return 1 ;; esac
  case "$real_binary" in *[[:space:]]*) return 1 ;; esac
  protected_directory_chain "$(dirname -- "$real_binary")" || return 1
  protected_regular_file "$real_binary" true || return 1
  [ "$(head -c 2 "$real_binary" 2>/dev/null)" != '#!' ] || return 1
  printf '%s\n' "$real_binary"
}

single_exec_start_argv() {
  exec_start=$1
  case "$exec_start" in
    *'
'*|*'} {'*|*'; path='*) return 1 ;;
  esac
  prefix='{ path='
  case "$exec_start" in "$prefix"*) ;; *) return 1 ;; esac
  remainder=${exec_start#"$prefix"}
  executable=${remainder%%' ; argv[]='*}
  [ "$remainder" != "$executable" ] || return 1
  remainder=${remainder#"$executable ; argv[]="}
  argv=${remainder%%' ; ignore_errors='*}
  [ "$remainder" != "$argv" ] || return 1
  metadata=${remainder#"$argv"}
  case "$metadata" in ' ; ignore_errors='*' }') ;; *) return 1 ;; esac
  metadata_without_closing=${metadata%\}}
  [ "$metadata_without_closing" != "$metadata" ] || return 1
  case "$metadata_without_closing" in *'{'*|*'}'*) return 1 ;; esac
  [ -n "$executable" ] && [ -n "$argv" ] || return 1
  printf '%s\n%s\n' "$executable" "$argv"
}

service_uses_paths() {
  service=$1
  binary=$2
  config=$3
  engine=$4
  real_binary=${5:-$binary}
  if [ "${service_manager:-systemd}" = openrc ]; then
    supervised=$(openrc_supervised_child_pid "$service") || return 1
    set -- $supervised
    supervisor_pid=$1
    child_pid=$2
    process_binary=$(readlink "$proc_root/$child_pid/exe" 2>/dev/null) || return 1
    [ "$process_binary" = "$real_binary" ] || return 1
    command_line=$(tr '\000' ' ' < "$proc_root/$child_pid/cmdline" 2>/dev/null) || return 1
    matched_config_directory=""
    case "$engine:$command_line" in
      "xray:$binary run -config $config "|"xray:$binary run -c $config "|\
      "xray:$real_binary run -config $config "|"xray:$real_binary run -c $config "|\
      "sing-box:$binary run -c $config "|"sing-box:$binary run --config $config "|\
      "sing-box:$real_binary run -c $config "|"sing-box:$real_binary run --config $config ") ;;
      *)
        [ "$engine" = sing-box ] || return 1
        for process_prefix in \
          "$binary run -c $config -C " \
          "$real_binary run -c $config -C "
        do
          case "$command_line" in
            "$process_prefix"*' ')
              process_config_directory=${command_line#"$process_prefix"}
              process_config_directory=${process_config_directory% }
              ;;
            *) continue ;;
          esac
          case "$process_config_directory" in /*) ;; *) return 1 ;; esac
          case "$process_config_directory" in *[[:space:]]*) return 1 ;; esac
          protected_config_directory "$process_config_directory" "$config" || return 1
          matched_config_directory=$process_config_directory
          return 0
        done
        return 1
        ;;
    esac
    return
  fi
  systemctl is-active --quiet "$service" 2>/dev/null || return 1
  exec_start=$(systemctl show "$service" --property=ExecStart --value 2>/dev/null) || return 1
  parsed=$(single_exec_start_argv "$exec_start") || return 1
  executable=$(printf '%s\n' "$parsed" | sed -n '1p')
  argv=$(printf '%s\n' "$parsed" | sed -n '2p')
  [ "$executable" = "$binary" ] || return 1
  matched_config_directory=""
  matched_work_directory=""
  case "$engine" in
    xray)
      case "$argv" in
        "$binary run -config $config"|"$binary run -c $config") return 0 ;;
      esac
      return 1
      ;;
    sing-box)
      matched_config_path=""
      matched_config_directory=""
      singbox_config_path_from_argv "$binary" "$argv" || return 1
      [ "$matched_config_path" = "$config" ] || return 1
      if [ -n "$matched_config_directory" ]; then
        protected_config_directory "$matched_config_directory" "$config" || return 1
      fi
      if [ -n "$matched_work_directory" ]; then
        case "$matched_work_directory" in /*) ;; *) return 1 ;; esac
        case "$matched_work_directory" in *[[:space:]]*) return 1 ;; esac
      fi
      return 0
      ;;
  esac
  return 1
}

singbox_config_path_from_argv() {
  binary=$1
  argv=$2
  matched_config_directory=""
  matched_work_directory=""
  set -- $argv
  [ "$1" = "$binary" ] || return 1
  shift
  config_path=""
  config_directory=""
  case "$#" in
    3)
      [ "$1" = run ] || return 1
      case "$2" in -c|--config) ;; *) return 1 ;; esac
      config_path=$3
      ;;
    5)
      if [ "$1" = run ] && { [ "$2" = "-c" ] || [ "$2" = "--config" ]; } && [ "$4" = "-C" ]; then
        config_path=$3
        config_directory=$5
      elif [ "$1" = "-D" ] && [ "$3" = "-C" ] && [ "$5" = run ]; then
        workdir=$2
        config_directory=$4
        case "$workdir" in /*) ;; *) return 1 ;; esac
        config_path="$config_directory/config.json"
        matched_work_directory=$workdir
      else
        return 1
      fi
      ;;
    *) return 1 ;;
  esac
  case "$config_path" in /*) ;; *) return 1 ;; esac
  case "$config_path" in *[[:space:]]*) return 1 ;; esac
  if [ -n "$config_directory" ]; then
    case "$config_directory" in /*) ;; *) return 1 ;; esac
    case "$config_directory" in *[[:space:]]*) return 1 ;; esac
    matched_config_directory=$config_directory
  fi
  matched_config_path=$config_path
}

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
  for property in RootDirectory RootImage BindPaths BindReadOnlyPaths Environment EnvironmentFiles; do
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
  found_service=""
  for service in "$active_service_candidate"; do
    for binary in $xray_binary_candidates; do
      protected_regular_file "$binary" true || continue
      for config in $xray_config_candidates; do
        protected_regular_file "$config" false || continue
        [ "$(wc -c < "$config")" -le 2097152 ] || continue
        service_uses_paths "$service" "$binary" "$config" xray || continue
        inspect_existing_candidate xray "$binary" "$config" || continue
        match_count=$((match_count + 1))
        found_binary=$binary
        found_config=$config
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
  mapped_xray_service=$found_service
  mapped_engines=$(append_csv "$mapped_engines" xray)
  printf '%s\n' "detected existing Xray service: $found_service ($found_config)"
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
