#!/usr/bin/env python3
"""Compare exported Agent listener counters with a namespace Ethernet capture.

Counts IPv4 total length / IPv6 payload length + 40. These are IP bytes,
not Ethernet bytes or unencrypted application payload. IPv6 extension headers
and VLAN captures are outside this fixture's supported capture format.
"""
import argparse
import collections
import json
import re
import struct
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("directory", type=Path)
args = parser.parse_args()
root = args.directory
capture_log = (root / "tcpdump.txt").read_text()
assert re.search(r"\b0 packets dropped by kernel", capture_log), capture_log
policies = json.loads((root / "policies.json").read_text())
ports = {p["port"] for p in policies}
targets = json.loads((root / "targets.json").read_text()) if (root / "targets.json").exists() else {}
target_counts = collections.defaultdict(lambda: [0, 0])
data = (root / "listener.pcap").read_bytes()
assert data[:4] in (b"\xd4\xc3\xb2\xa1", b"\xa1\xb2\xc3\xd4"), "unsupported pcap format"
endian = "<" if data[:4] == b"\xd4\xc3\xb2\xa1" else ">"
assert struct.unpack(endian + "I", data[20:24])[0] == 1, "requires Ethernet capture"
counts = collections.defaultdict(lambda: [0, 0, 0, 0])
pos = 24
while pos < len(data):
    _, _, captured, original = struct.unpack(endian + "IIII", data[pos:pos + 16])
    pos += 16
    packet = data[pos:pos + captured]
    pos += captured
    assert captured == original and len(packet) == captured, "truncated capture"
    ether_type = struct.unpack("!H", packet[12:14])[0]
    ip = packet[14:]
    if ether_type == 0x0800:
        length = struct.unpack("!H", ip[2:4])[0]
        protocol, offset = ip[9], (ip[0] & 15) * 4
    elif ether_type == 0x86DD:
        length = struct.unpack("!H", ip[4:6])[0] + 40
        protocol, offset = ip[6], 40
        assert protocol not in (0, 43, 44, 60), "unsupported IPv6 extension headers"
    else:
        continue
    if protocol not in (6, 17):
        continue
    source, destination = struct.unpack("!HH", ip[offset:offset + 4])
    overhead = offset + (8 if protocol == 17 else (ip[offset + 12] >> 4) * 4)
    for port, target in targets.items():
        if source == target:
            target_counts[int(port)][0] += length
        if destination == target:
            target_counts[int(port)][1] += length
    for port in ports:
        if destination == port:
            counts[port][0] += length
            counts[port][2] += length - overhead
        if source == port:
            counts[port][1] += length
            counts[port][3] += length - overhead

snapshot = {u["policy_id"]: u for u in json.loads((root / "phase-final.json").read_text())}
comparison = []
for policy in policies:
    usage = snapshot[policy["id"]]
    received, sent, payload_up, payload_down = counts[policy["port"]]
    target_rx, target_tx = target_counts[policy["port"]]
    comparison.append(dict(
        client_received=received, client_sent=sent,
        target_received=target_rx, target_sent=target_tx,
        port=policy["port"], transport_payload_upload=payload_up,
        transport_payload_download=payload_down, pcap_upload=received + target_rx,
        pcap_download=sent + target_tx, agent_upload=usage["received_bytes"],
        agent_download=usage["sent_bytes"],
        upload_difference=usage["received_bytes"] - received - target_rx,
        download_difference=usage["sent_bytes"] - sent - target_tx,
    ))
result = json.dumps(comparison, indent=2) + "\n"
print(result, end="")
(root / "pcap-comparison.json").write_text(result)
# Offload may change the number of observable headers. A difference is reported
# as a failed comparison; it is never silently normalized or hidden.
assert all(r["upload_difference"] == r["download_difference"] == 0 for r in comparison)
