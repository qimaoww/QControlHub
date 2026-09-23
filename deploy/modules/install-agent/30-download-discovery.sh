
download() {
  source_path=$1
  destination=$2
  if [ -n "$ca_file" ]; then
    "$download_cmd" --fail --silent --show-error --compressed --cacert "$ca_file" -H "X-QControlHub-Enrollment: $token" "$http_origin$source_path" -o "$destination"
  else
    "$download_cmd" --fail --silent --show-error --compressed -H "X-QControlHub-Enrollment: $token" "$http_origin$source_path" -o "$destination"
  fi
}

echo '== 1/6 下载安装资源 =='
for asset in \
  deploy/bootstrap-core-services.sh \
  deploy/existing-core-mapping.sh \
  examples/configs/mihomo-minimal.yaml \
  examples/configs/xray-minimal.json \
  examples/configs/sing-box-minimal.json \
  examples/configs/shadowsocks-rust-minimal.json
do
  download "/install-assets/$asset" "$repository_dir/$asset"
done
if [ "$service_manager" = openrc ]; then
  service_assets="qagent qagent-mihomo qagent-xray qagent-sing-box qagent-shadowsocks-rust"
  for service_asset in $service_assets; do
    download "/install-assets/deploy/openrc/$service_asset" "$repository_dir/deploy/openrc/$service_asset"
  done
else
  service_assets="qagent.service qagent-core-journal.conf qagent-mihomo.service qagent-xray.service qagent-sing-box.service qagent-shadowsocks-rust.service qagent-mihomo@.service qagent-xray@.service qagent-sing-box@.service qagent-shadowsocks-rust@.service"
  for service_asset in $service_assets; do
    download "/install-assets/deploy/systemd/$service_asset" "$repository_dir/deploy/systemd/$service_asset"
  done
fi
. "$repository_dir/deploy/existing-core-mapping.sh"

echo "== 2/6 下载 agent 二进制（控制面 GET /api/v1/agent-binary）=="
download /api/v1/agent-binary "$work_dir/qagent"
[ -s "$work_dir/qagent" ] || { printf '%s\n' 'downloaded agent binary is empty' >&2; exit 1; }
chmod 0755 "$work_dir/qagent"

echo '== 3/6 检测现有核心并暂存按需安装资源 =='
run_discovery() {
  label=$1
  shift
  if "$@"; then
    return 0
  else
    result=$?
  fi
  case "$result" in
    1) return 0 ;;
    2)
      printf '%s\n' "warning: existing $label service was left unchanged because it could not be mapped safely; QAgent installation will continue and only this core's remote tasks will remain disabled" >&2
      return 0
      ;;
    *)
      printf '%s\n' "unexpected $label discovery failure; installation stopped without changing services" >&2
      exit "$result"
      ;;
  esac
}
run_discovery Xray discover_existing_xray
run_discovery sing-box discover_existing_singbox
QCH_SERVICE_MANAGER="$service_manager" sh "$repository_dir/deploy/bootstrap-core-services.sh" --prepare-agent
install -d -o root -g "$core_service_group" -m 0750 "$qagent_etc_dir"
