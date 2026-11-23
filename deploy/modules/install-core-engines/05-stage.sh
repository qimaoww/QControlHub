
# A gzip stream carries no executable mode. Prepare the staged binary before
# version verification; installation only starts after every core is verified.
qch_stage_gzip_binary() {
  qch_gzip_archive=$1
  qch_gzip_binary=$2
  shift 2
  gunzip -c "$qch_gzip_archive" > "$qch_gzip_binary" || return 1
  chmod 0755 "$qch_gzip_binary" || return 1
  "$qch_gzip_binary" "$@"
}

# Follow the same stable channel as the Agent updater. Resolve the archive from
# release metadata instead of embedding a release number in the installer.
qch_mihomo_stable_url() {
  jq -er '
    select(.draft == false and .prerelease == false)
    | .tag_name as $tag
    | select($tag | test("^v[0-9]+\\.[0-9]+\\.[0-9]+$"))
    | ("https://github.com/MetaCubeX/mihomo/releases/download/" + $tag + "/mihomo-linux-amd64-v1-" + $tag + ".gz") as $url
    | [.assets[] | select(.browser_download_url == $url)]
    | if length == 1 then .[0].browser_download_url else error("missing or ambiguous stable Mihomo asset") end
  ' "$1"
}

qch_mihomo_version_matches() {
  printf '%s\n' "$1" | awk -v tag="$2" '
    NR == 1 {
      version = ($2 == "Meta" ? $3 : $2)
      valid = ($1 == "Mihomo" && version == tag)
    }
    END { exit !valid }
  '
}

# A nonzero version probe stays fatal unless every line is the GNU loader's
# missing GLIBC version diagnostic for this exact candidate.
qch_ssrust_missing_glibc() {
  awk -v candidate="$1" '
    BEGIN {
      prefix = candidate ": "; suffix = " (required by " candidate ")"
      pattern = "^/[^:]+: version `GLIBC_[0-9]+\\.[0-9]+(\\.[0-9]+)?" sprintf("%c", 39) " not found$"
    }
    {
      if (index($0, prefix) != 1 || substr($0, length($0)-length(suffix)+1) != suffix) exit 1
      body = substr($0, length(prefix)+1, length($0)-length(prefix)-length(suffix))
      if (body !~ pattern) exit 1
      found = 1
    }
    END { if (!found) exit 1 }
  ' "$2"
}

qch_ssrust_version() {
  qch_ssrust_status=0
  # Bound runtime and output even when the command runs in a substitution.
  (ulimit -f 128; exec timeout 15 "$1" --version) > "$3" 2>&1 || qch_ssrust_status=$?
  if [ "$qch_ssrust_status" -ne 0 ]; then
    head -c 2048 "$3" >&2
    return "$qch_ssrust_status"
  fi
  qch_ssrust_line=$(head -n1 "$3")
  [ "$qch_ssrust_line" = "shadowsocks $2" ] || {
    printf '%s\n' 'ss-rust version does not match the selected release' >&2
    head -c 2048 "$3" >&2
    return 1
  }
  printf '%s\n' "$qch_ssrust_line"
}
