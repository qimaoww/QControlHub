
# Deploying QAgent must not create four unused core services. Keep the
# credential-protected installation assets in a protected local directory. The
# Agent invokes the bootstrap for exactly one engine only after an explicit
# panel install/import task arrives.
for asset_directory in \
  "$qagent_bin_dir" \
  "$qagent_bin_dir/cores" \
  "$core_share_root" \
  "$core_asset_root" \
  "$core_asset_root/deploy" \
  "$core_asset_root/deploy/$service_manager" \
  "$core_asset_root/examples" \
  "$core_asset_root/examples/configs"
do
  [ ! -L "$asset_directory" ] || { printf '%s\n' "refusing symlinked core asset directory: $asset_directory" >&2; exit 1; }
  install -d -o root -g root -m 0755 "$asset_directory"
done

stage_core_asset() {
  source_file=$1
  destination=$2
  mode=$3
  [ ! -L "$destination" ] || { printf '%s\n' "refusing symlinked core asset: $destination" >&2; exit 1; }
  [ ! -e "$destination" ] || [ -f "$destination" ] || {
    printf '%s\n' "refusing non-regular core asset: $destination" >&2
    exit 1
  }
  install -o root -g root -m "$mode" "$source_file" "$destination"
}

stage_core_asset "$repository_dir/deploy/bootstrap-core-services.sh" "$core_asset_root/deploy/bootstrap-core-services.sh" 0755
stage_core_asset "$repository_dir/deploy/existing-core-mapping.sh" "$core_asset_root/deploy/existing-core-mapping.sh" 0644
for config_asset in mihomo-minimal.yaml xray-minimal.json sing-box-minimal.json shadowsocks-rust-minimal.json; do
  stage_core_asset "$repository_dir/examples/configs/$config_asset" "$core_asset_root/examples/configs/$config_asset" 0644
done
for service_asset in $service_assets; do
  if [ "$service_manager" = openrc ]; then service_mode=0755; else service_mode=0644; fi
  stage_core_asset "$repository_dir/deploy/$service_manager/$service_asset" "$core_asset_root/deploy/$service_manager/$service_asset" "$service_mode"
done
