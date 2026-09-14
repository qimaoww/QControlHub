
resolve_fixed_singbox_binary() {
  service_binary=$1
  if [ ! -L "$service_binary" ]; then
    protected_directory_chain "$(dirname -- "$service_binary")" || return 1
    protected_existing_core_file "$service_binary" || return 1
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
  protected_existing_core_file "$real_binary" || return 1
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

config_path_from_argv() {
  case "$1" in
    xray) xray_config_path_from_argv "$2" "$3" ;;
    sing-box) singbox_config_path_from_argv "$2" "$3" ;;
    *) return 1 ;;
  esac
}

service_uses_paths() {
  service=$1
  binary=$2
  config=$3
  engine=$4
  real_binary=${5:-$binary}
  matched_config_path=""
  matched_config_directory=""
  matched_work_directory=""
  if [ "${service_manager:-systemd}" = openrc ]; then
    supervised=$(openrc_supervised_child_pid "$service") || return 1
    set -- $supervised
    supervisor_pid=$1
    child_pid=$2
    process_binary=$(readlink "$proc_root/$child_pid/exe" 2>/dev/null) || return 1
    [ "$process_binary" = "$real_binary" ] || return 1
    command_line=$(tr '\000' ' ' < "$proc_root/$child_pid/cmdline" 2>/dev/null) || return 1
    command_line=${command_line% }
    # A supervised process reports either the service executable or the resolved
    # real binary as argv0; accept exactly those two and nothing else.
    config_path_from_argv "$engine" "$binary" "$command_line" ||
      config_path_from_argv "$engine" "$real_binary" "$command_line" || return 1
    [ "$matched_config_path" = "$config" ] || return 1
    # The official sing-box working-directory form has no supervised OpenRC
    # binding this mapping could prove, so it stays rejected here.
    [ -z "$matched_work_directory" ] || return 1
    if [ -n "$matched_config_directory" ]; then
      protected_config_directory "$matched_config_directory" "$config" "$engine" || return 1
    fi
    return 0
  fi
  systemctl is-active --quiet "$service" 2>/dev/null || return 1
  exec_start=$(systemctl show "$service" --property=ExecStart --value 2>/dev/null) || return 1
  parsed=$(single_exec_start_argv "$exec_start") || return 1
  executable=$(printf '%s\n' "$parsed" | sed -n '1p')
  argv=$(printf '%s\n' "$parsed" | sed -n '2p')
  [ "$executable" = "$binary" ] || return 1
  config_path_from_argv "$engine" "$binary" "$argv" || return 1
  [ "$matched_config_path" = "$config" ] || return 1
  if [ -n "$matched_config_directory" ]; then
    protected_config_directory "$matched_config_directory" "$config" "$engine" || return 1
  fi
  if [ -n "$matched_work_directory" ]; then
    case "$matched_work_directory" in /*) ;; *) return 1 ;; esac
    case "$matched_work_directory" in *[[:space:]]*) return 1 ;; esac
  fi
  return 0
}

# xray_config_path_from_argv recognizes the exact Xray invocation shapes that
# can be mapped safely: a single configuration file, a file combined with a
# confdir, and the directory-authoritative form used by installers that ship no
# main file at all, which reports an empty configuration path.
xray_config_path_from_argv() {
  binary=$1
  argv=$2
  matched_config_path=""
  matched_config_directory=""
  matched_work_directory=""
  set -- $argv
  [ "$1" = "$binary" ] || return 1
  shift
  [ "${1:-}" = run ] || return 1
  shift
  config_path=""
  config_directory=""
  case "$#" in
    2)
      case "$1" in
        -confdir) config_directory=$2 ;;
        -config|-c) config_path=$2 ;;
        *) return 1 ;;
      esac
      ;;
    4)
      case "$1" in -config|-c) ;; *) return 1 ;; esac
      [ "$3" = -confdir ] || return 1
      config_path=$2
      config_directory=$4
      ;;
    *) return 1 ;;
  esac
  if [ -n "$config_path" ]; then
    case "$config_path" in /*) ;; *) return 1 ;; esac
    case "$config_path" in *[[:space:]]*) return 1 ;; esac
  fi
  if [ -n "$config_directory" ]; then
    case "$config_directory" in /*) ;; *) return 1 ;; esac
    case "$config_directory" in *[[:space:]]*) return 1 ;; esac
    matched_config_directory=$config_directory
  fi
  matched_config_path=$config_path
}

singbox_config_path_from_argv() {
  binary=$1
  argv=$2
  matched_config_path=""
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
