
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
  openrc_state_directory_chain "$options_dir" || return 1
  protected_regular_file "$options_dir/child_pid" false || return 1
  child_pid=$(cat "$options_dir/child_pid") || return 1
  case "$child_pid" in *[!0-9]*) return 1 ;; esac
  [ "$child_pid" -gt 1 ] || return 1
  child_pid_stamp=$(stat -c '%d:%i' "$options_dir/child_pid" 2>/dev/null) || return 1
  protected_regular_file "$options_dir/pidfile" false || return 1
  pidfile_value=$(cat "$options_dir/pidfile") || return 1
  # An OpenRC init script chooses its own pidfile name, so the name is not a
  # trust anchor; the supervisor identity verified below is. The path is still
  # constrained to a direct child of the run directory with a plain .pid name.
  supervisor_pidfile_name=${pidfile_value##*/}
  case "$pidfile_value" in
    "$openrc_run_root/$supervisor_pidfile_name"|"/var/run/$supervisor_pidfile_name") ;;
    *) return 1 ;;
  esac
  case "$supervisor_pidfile_name" in
    .*|*[!A-Za-z0-9._-]*) return 1 ;;
    *.pid) ;;
    *) return 1 ;;
  esac
  [ "$supervisor_pidfile_name" != .pid ] || return 1
  pidfile_stamp=$(stat -c '%d:%i' "$options_dir/pidfile" 2>/dev/null) || return 1
  supervisor_pidfile="$openrc_run_root/$supervisor_pidfile_name"
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
