# TLS frontend preview and migration

Set the following identifiers from the authorized workspace. Use an empty string for the main instance. Use the assigned share ID for a shared instance. Preview runs without `secrets.read`.

```sh
python3 scripts/qch.py POST "/agents/$AGENT/configs/$ENGINE/server-inbounds/$TAG/tls-frontend/preview" --stdin <<'JSON'
{"expected_version":1,"expected_instance_id":"","target_mode":"caddy"}
JSON
```

Use a URL-encoded tag and the current saved version. Review each member's source and target mode, blockers and remaining Agent checks. The preview creates no revision or task. The existing server-inbounds intents both persist a revision and queue work. Validate checks the saved candidate. Deploy attempts to activate it. Query the task ID and applied revision before reporting completion. Keep credentials, configuration bodies and diagnostic logs out of output.

For a reviewed plan with no blockers, bind the exact same selection and its plan ID. Set `AGENT`, `ENGINE`, `TAG` (URL-encoded), `VERSION` and `INSTANCE` explicitly. Keep the operation ID so a disconnected client can query the same operation. The following commands use the downloaded helper and jq.

```sh
set -eu
endpoint="/agents/$AGENT/configs/$ENGINE/server-inbounds/$TAG/tls-frontend"
selection=$(jq -nc --arg i "$INSTANCE" --argjson v "$VERSION" '{expected_instance_id:$i,expected_version:$v,target_mode:"caddy"}')
plan=$(printf '%s' "$selection" | python3 scripts/qch.py POST "$endpoint/preview" --stdin)
printf '%s\n' "$plan"
plan_id=$(printf '%s' "$plan" | jq -er 'select(.blockers == []) | .plan_id')
task_id=$(python3 -c 'import secrets; print("tsk_" + secrets.token_hex(8))')
printf 'Operation %s\n' "$task_id"
op=$(printf '%s' "$selection" | jq -c --arg p "$plan_id" --arg t "$task_id" '. + {plan_id:$p,task_id:$t}' | python3 scripts/qch.py POST "$endpoint/migrate" --stdin)
printf '%s\n' "$op"
if [ "$(printf '%s' "$op" | jq -r .state)" != unchanged ]; then
  task_id=$(printf '%s' "$op" | jq -er .task_id)
  python3 scripts/qch.py GET "/agents/$AGENT/tls-migrations/$task_id" --wait --budget 180
fi
```

Each result row identifies the configuration, instance, engine, inbound and path alongside its candidate and recovery evidence. The helper emits SHA-256 digests of the UTF-8 tag and path. On older panels, match result rows to plan members by array order. A 202 response includes the original task ID. Current panels also return its lookup URL in Location. A recovered original deployment reports `rolled_back`. For `recovery_incomplete`, report the step and affected members to the operator. Query the same task ID after repair. The panel refreshes recovery evidence from the node on reconnect and subsequent heartbeats. Existing sessions can disconnect during listener handoff. An older Agent has to advertise `tls-port-migration-v1` before execution. The legacy save/validate and save/deploy endpoints keep their previous behavior.
