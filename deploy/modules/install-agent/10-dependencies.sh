
nftables_available() {
  if [ -n "${QCH_NFT:-}" ]; then
    [ -x "$nft_cmd" ]
    return
  fi
  [ -x "$nft_cmd" ] || command -v nft >/dev/null 2>&1
}

install_nftables() {
  nftables_available && return 0
  printf '%s\n' 'nft not found; installing the nftables package for port traffic monitoring'
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq || {
      printf '%s\n' 'failed to update APT package metadata for nftables' >&2
      exit 1
    }
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with APT' >&2
      exit 1
    }
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with apk' >&2
      exit 1
    }
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with dnf' >&2
      exit 1
    }
  elif command -v yum >/dev/null 2>&1; then
    yum install -y nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with yum' >&2
      exit 1
    }
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install nftables >/dev/null || {
      printf '%s\n' 'failed to install nftables with zypper' >&2
      exit 1
    }
  else
    printf '%s\n' 'nftables is required, but no supported package manager was found (apt, apk, dnf, yum, or zypper)' >&2
    exit 1
  fi
  nftables_available || {
    printf '%s\n' 'the nftables package was installed, but the nft executable is still unavailable' >&2
    exit 1
  }
}

iproute2_available() {
  for candidate in /usr/sbin/ip /usr/bin/ip /sbin/ip /bin/ip; do
    [ -x "$candidate" ] && return 0
  done
  return 1
}

# Independent-exit accounting inserts socket marks and must first prove that no
# pre-existing fwmark policy routing can capture them. The Agent verifies that
# with "ip rule show", so iproute2 is a runtime dependency of every managed
# core, not an optional administration tool.
install_iproute2() {
  iproute2_available && return 0
  printf '%s\n' 'ip not found; installing the iproute2 package for independent exit verification'
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq || {
      printf '%s\n' 'failed to update APT package metadata for iproute2' >&2
      exit 1
    }
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends iproute2 >/dev/null || {
      printf '%s\n' 'failed to install iproute2 with APT' >&2
      exit 1
    }
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache iproute2 >/dev/null || {
      printf '%s\n' 'failed to install iproute2 with apk' >&2
      exit 1
    }
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y iproute >/dev/null || {
      printf '%s\n' 'failed to install iproute with dnf' >&2
      exit 1
    }
  elif command -v yum >/dev/null 2>&1; then
    yum install -y iproute >/dev/null || {
      printf '%s\n' 'failed to install iproute with yum' >&2
      exit 1
    }
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install iproute2 >/dev/null || {
      printf '%s\n' 'failed to install iproute2 with zypper' >&2
      exit 1
    }
  else
    printf '%s\n' 'iproute2 is required, but no supported package manager was found (apt, apk, dnf, yum, or zypper)' >&2
    exit 1
  fi
  iproute2_available || {
    printf '%s\n' 'the iproute2 package was installed, but the ip executable is still unavailable' >&2
    exit 1
  }
}
# UDP history is optional: package-manager failure must not prevent an Agent
# update or TCP collection. Do not enable conntrackd or alter firewall rules.
install_conntrack() (
  command -v "${QCH_CONNTRACK:-conntrack}" >/dev/null 2>&1 && exit 0
  printf '%s\n' 'conntrack not found; installing UDP connection history support'
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get update -qq &&
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends conntrack >/dev/null || exit 1
  elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache conntrack-tools >/dev/null || exit 1
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y conntrack-tools >/dev/null || exit 1
  elif command -v yum >/dev/null 2>&1; then
    yum install -y conntrack-tools >/dev/null || exit 1
  elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install conntrack-tools >/dev/null || exit 1
  else
    exit 1
  fi
  command -v "${QCH_CONNTRACK:-conntrack}" >/dev/null 2>&1
)
