
protected_regular_file() {
  # POSIX sh has no locals, so these names are prefixed: an earlier revision
  # assigned a bare `candidate`, which silently overwrote the loop variable of
  # find_single_active_service and made every OpenRC service look inactive.
  protected_file_path=$1
  protected_file_executable=${2:-false}
  [ -f "$protected_file_path" ] && [ ! -L "$protected_file_path" ] || return 1
  [ "$(stat -c '%u' "$protected_file_path" 2>/dev/null)" = 0 ] || return 1
  protected_file_permissions=$(stat -c '%a' "$protected_file_path" 2>/dev/null) || return 1
  [ $((0$protected_file_permissions & 022)) -eq 0 ] || return 1
  if [ "$protected_file_executable" = true ]; then [ -x "$protected_file_path" ] || return 1; fi
  protected_file_parent=$(dirname -- "$protected_file_path")
  [ -d "$protected_file_parent" ] && [ ! -L "$protected_file_parent" ] || return 1
  [ "$(stat -c '%u' "$protected_file_parent" 2>/dev/null)" = 0 ] || return 1
  protected_file_permissions=$(stat -c '%a' "$protected_file_parent" 2>/dev/null) || return 1
  [ $((0$protected_file_permissions & 022)) -eq 0 ]
}

file_owner_is_inactive_orphan() {
  orphan_owner_uid=$1
  case "$orphan_owner_uid" in ''|*[!0-9]*) return 1 ;; esac
  [ "$orphan_owner_uid" -gt 0 ] || return 1

  # Use only the fixed, protected system helper to consult the host's complete
  # NSS view. Missing helpers and lookup errors fail closed; 2 means no account.
  orphan_getent_path=/usr/bin/getent
  protected_directory_chain "$(dirname -- "$orphan_getent_path")" || return 1
  if [ -L "$orphan_getent_path" ]; then
    orphan_getent_real=$(readlink -f -- "$orphan_getent_path" 2>/dev/null) || return 1
    case "$orphan_getent_real" in /*) ;; *) return 1 ;; esac
    protected_directory_chain "$(dirname -- "$orphan_getent_real")" || return 1
    protected_regular_file "$orphan_getent_real" true || return 1
    [ "$(head -c 2 "$orphan_getent_real" 2>/dev/null)" != '#!' ] || return 1
  else
    protected_regular_file "$orphan_getent_path" true || return 1
    [ "$(head -c 2 "$orphan_getent_path" 2>/dev/null)" != '#!' ] || return 1
  fi
  "$orphan_getent_path" passwd "$orphan_owner_uid" >/dev/null 2>&1
  orphan_getent_status=$?
  [ "$orphan_getent_status" -eq 2 ] || return 1

  orphan_status_count=0
  for orphan_status_path in "$proc_root"/[0-9]*/task/[0-9]*/status; do
    [ -e "$orphan_status_path" ] || continue
    orphan_status_count=$((orphan_status_count + 1))
    orphan_status_uids=$(awk '
      $1 == "Uid:" {
        seen = 1
        if (NF != 5) exit 2
        print $2, $3, $4, $5
      }
      END { if (!seen) exit 2 }
    ' "$orphan_status_path" 2>/dev/null) || {
      # A process may disappear between the glob and the read. Every other
      # unreadable or malformed status file fails closed.
      [ ! -e "$orphan_status_path" ] || return 1
      continue
    }
    case " $orphan_status_uids " in
      *" $orphan_owner_uid "*) return 1 ;;
    esac
  done
  [ "$orphan_status_count" -gt 0 ]
}

protected_existing_core_file() {
  existing_core_file_path=$1
  if protected_regular_file "$existing_core_file_path" true; then
    return 0
  fi
  case " $installer_orphan_owner_binary_candidates " in
    *" $existing_core_file_path "*) ;;
    *) return 1 ;;
  esac
  [ -f "$existing_core_file_path" ] && [ ! -L "$existing_core_file_path" ] && [ -x "$existing_core_file_path" ] || return 1
  existing_core_file_permissions=$(stat -c '%a' "$existing_core_file_path" 2>/dev/null) || return 1
  [ $((0$existing_core_file_permissions & 022)) -eq 0 ] || return 1
  protected_directory_chain "$(dirname -- "$existing_core_file_path")" || return 1
  existing_core_file_uid=$(stat -c '%u' "$existing_core_file_path" 2>/dev/null) || return 1
  file_owner_is_inactive_orphan "$existing_core_file_uid"
}

protected_directory_chain() {
  validate_directory_chain "$1" false
}

# OpenRC creates its own service directory (/run/openrc) as mode 0775 owned by
# root:root. Tolerate exactly that policy shape — root owner, root group, no
# world-write — while reading OpenRC state. This is a real but narrow relaxation
# because a non-root account could be a member of gid 0; every other protected
# path keeps the stricter rule.
openrc_state_directory_chain() {
  case "$1" in
    "$openrc_state_root"|"$openrc_state_root"/*) ;;
    *) return 1 ;;
  esac
  validate_directory_chain "$1" true || return 1
  # End the gid-0 exception at OpenRC's state root. Its parent (/run on a
  # stock system) remains subject to the general protected-path policy.
  protected_directory_chain "$(dirname -- "$openrc_state_root")"
}

validate_directory_chain() {
  chain_directory=$1
  allow_root_group_write=${2:-false}
  case "$chain_directory" in /*) ;; *) return 1 ;; esac
  while :; do
    [ -d "$chain_directory" ] && [ ! -L "$chain_directory" ] || return 1
    chain_permissions=$(stat -c '%a' "$chain_directory" 2>/dev/null) || return 1
    chain_mode=$((0$chain_permissions))
    if [ $((chain_mode & 022)) -ne 0 ]; then
      if [ $((chain_mode & 01000)) -ne 0 ]; then
        [ "$(stat -c '%u' "$chain_directory" 2>/dev/null)" = 0 ] || return 1
        return 0
      fi
      [ "$allow_root_group_write" = true ] || return 1
      [ $((chain_mode & 002)) -eq 0 ] || return 1
      [ "$(stat -c '%g' "$chain_directory" 2>/dev/null)" = 0 ] || return 1
    fi
    [ "$(stat -c '%u' "$chain_directory" 2>/dev/null)" = 0 ] || return 1
    [ "$chain_directory" != / ] || return 0
    chain_directory=$(dirname -- "$chain_directory")
  done
}

protected_config_directory() {
  config_directory=$1
  primary=$2
  config_engine=$3
  protected_directory_chain "$config_directory" || return 1
  # A directory-authoritative mapping has no main configuration file, so the
  # size budget starts empty and the directory alone has to stay within it.
  case "$config_engine" in
    xray) config_patterns="$config_directory/*.json $config_directory/*.jsonc $config_directory/*.toml $config_directory/*.yaml $config_directory/*.yml" ;;
    sing-box) config_patterns="$config_directory/*.json" ;;
    *) return 1 ;;
  esac
  if [ -n "$primary" ] && { [ "$config_engine" = xray ] || [ "$primary" != "$config_directory/config.json" ]; }; then
    total=$(wc -c < "$primary") || return 1
  else
    total=0
  fi
  for config_candidate in $config_patterns; do
    [ -e "$config_candidate" ] || continue
    protected_regular_file "$config_candidate" false || return 1
    size=$(wc -c < "$config_candidate") || return 1
    total=$((total + size))
    [ "$total" -le 2097152 ] || return 1
  done
}
