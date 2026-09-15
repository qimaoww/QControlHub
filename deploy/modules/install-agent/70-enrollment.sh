
agent_enrollment_completed() {
  [ -s "$final_state" ] || return 1
  grep -Fq "\"server\":\"$server_host\"" "$final_state" || return 1
  if [ "$reenroll_required" = true ] && [ "$previous_state_present" = true ]; then
    current_private_key=$(sed -n 's/.*"private_key":"\([^"]*\)".*/\1/p' "$final_state" | head -n 1)
    [ -n "$current_private_key" ] || return 1
    [ "$current_private_key" != "$previous_private_key" ] || return 1
  fi
}

waited_seconds=0
while ! agent_enrollment_completed; do
  if [ "$waited_seconds" -ge "$enrollment_wait_seconds" ]; then
    if [ "$service_manager" = openrc ]; then
      "$rc_service_cmd" qagent stop >/dev/null 2>&1 || true
    else
      "$systemctl_cmd" stop qagent.service >/dev/null 2>&1 || true
    fi
    printf '%s\n' "Agent did not confirm enrollment with $server_host within ${enrollment_wait_seconds}s; the service was stopped and enrollment credentials were retained, rerun this installer to retry" >&2
    exit 1
  fi
  sleep 1
  waited_seconds=$((waited_seconds + 1))
done

# The add-node credential is needed only until the state file proves that the
# Agent enrolled against this control plane. On a failed or interrupted
# migration the stopped service retains it so rerunning this installer can
# retry without destroying the prior identity or any managed core state.
sed -i '/^QCH_ENROLLMENT_TOKEN=/d' "$agent_env_file"
chmod 0600 "$agent_env_file"
if [ "$service_manager" = openrc ]; then
  sed -i '/^export QCH_ENROLLMENT_TOKEN=/d' "$openrc_conf"
  chmod 0600 "$openrc_conf"
fi
