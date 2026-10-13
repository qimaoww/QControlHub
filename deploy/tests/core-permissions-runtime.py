#!/usr/bin/env python3
"""Opt-in real forwarding with the shipped systemd/OpenRC service policies.

Run as root with QCH_PERMISSION_BIN pointing at verified official binaries.
OpenRC runs inside a disposable Alpine container; systemd uses unique /run units.
All traffic stays on loopback. Unique units and a counter-only nft table are
removed on exit; production services and firewall rules remain untouched.
"""
import json
import os
import pathlib
import pwd
import shutil
import ssl
import socket
import struct
import subprocess
import tempfile
import threading
import time

REPO = pathlib.Path(__file__).resolve().parents[2]
MANAGER = os.environ.get("QCH_SERVICE_MANAGER", "systemd")
BIN = pathlib.Path(os.environ["QCH_PERMISSION_BIN"])
ACCOUNT = pwd.getpwnam("qcontrolhub-core")
TCP_RESPONSE = b"HTTP/1.0 200 OK\r\nContent-Length: 9\r\n\r\nqch-tcpok"
ROOT = pathlib.Path(tempfile.mkdtemp(prefix="qch-permissions-", dir="/var/lib"))
ROOT.chmod(0o755)
ALIASES = []


def run(*args, check=True):
    result = subprocess.run(args, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=40)
    if check and result.returncode:
        raise RuntimeError(f"{' '.join(map(str, args))}: {result.stdout}")
    return result.stdout.strip()


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def receive(sock, size):
    data = b""
    while len(data) < size:
        more = sock.recv(size - len(data))
        if not more:
            raise RuntimeError("unexpected SOCKS EOF")
        data += more
    return data


def address(host, p):
    family = socket.AF_INET6 if ":" in host else socket.AF_INET
    return bytes([4 if family == socket.AF_INET6 else 1]) + socket.inet_pton(family, host) + struct.pack("!H", p)


def socks(p, command, host, target):
    sock = socket.create_connection(("127.0.0.1", p), timeout=3)
    sock.sendall(b"\x05\x01\x00")
    assert receive(sock, 2) == b"\x05\x00"
    sock.sendall(b"\x05" + bytes([command, 0]) + address(host, target))
    header = receive(sock, 4)
    assert header[:2] == b"\x05\x00", header
    size = 16 if header[3] == 4 else 4
    peer = socket.inet_ntop(socket.AF_INET6 if size == 16 else socket.AF_INET, receive(sock, size))
    bound = struct.unpack("!H", receive(sock, 2))[0]
    return sock, peer, bound


def backend(host, udp):
    family = socket.AF_INET6 if ":" in host else socket.AF_INET
    sock = socket.socket(family, socket.SOCK_DGRAM if udp else socket.SOCK_STREAM)
    sock.bind((host, 0))
    if not udp:
        sock.listen()
    hits = [0]

    def serve():
        while True:
            try:
                if udp:
                    body, peer = sock.recvfrom(65535)
                    hits[0] += 1
                    sock.sendto(b"qch-echo:" + body, peer)
                else:
                    conn, _ = sock.accept()
                    with conn:
                        conn.recv(4096)
                        hits[0] += 1
                        conn.sendall(TCP_RESPONSE)
            except OSError:
                return

    threading.Thread(target=serve, daemon=True).start()
    return sock, sock.getsockname()[1], hits


TARGETS = [(host, backend(host, False), backend(host, True)) for host in ("127.0.0.1", "::1")]
BLOCKED_TARGET = backend("127.0.0.2", False)
TABLE = f"qch_permissions_{os.getpid()}"


def marked_packets():
    rules = json.loads(run("nft", "--json", "list", "chain", "inet", TABLE, "egress"))
    return [expression["counter"]["packets"] for item in rules["nftables"] if "rule" in item for expression in item["rule"]["expr"] if "counter" in expression]


def traffic(p, targets=None):
    for host, tcp, udp in TARGETS if targets is None else targets:
        before = tcp[2][0]
        sock, _, _ = socks(p, 1, host, tcp[1])
        with sock:
            sock.sendall(b"GET / HTTP/1.0\r\n\r\n")
            assert receive(sock, len(TCP_RESPONSE)) == TCP_RESPONSE
        assert tcp[2][0] == before + 1
        control, relay, rp = socks(p, 3, "127.0.0.1", 0)
        with control, socket.socket(socket.AF_INET6 if ":" in relay else socket.AF_INET, socket.SOCK_DGRAM) as datagram:
            datagram.settimeout(3)
            if relay in ("0.0.0.0", "::"):
                relay = "::1" if ":" in relay else "127.0.0.1"
            for i in range(4):
                body = os.urandom(32 + i * 313)
                before = udp[2][0]
                datagram.sendto(b"\0\0\0" + address(host, udp[1]) + body, (relay, rp))
                reply, _ = datagram.recvfrom(65535)
                size = 22 if reply[3] == 4 else 10
                assert reply[size:] == b"qch-echo:" + body, "UDP payload mismatch"
                assert udp[2][0] == before + 1, "traffic did not reach controlled core target"


def write(path, content, state=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    path.chmod(0o640)
    os.chown(path, ACCOUNT.pw_uid if state else 0, ACCOUNT.pw_gid)


def inspect_process(name, unit):
    pid = int(run("systemctl", "show", unit.name, "-p", "MainPID", "--value")) if MANAGER == "systemd" else int(pathlib.Path(f"/run/openrc/options/{name}/child_pid").read_text())
    status = dict(line.split(":", 1) for line in pathlib.Path(f"/proc/{pid}/status").read_text().splitlines() if ":" in line)
    for key in ("Uid", "Gid"):
        assert all(int(v) == (ACCOUNT.pw_uid if key == "Uid" else ACCOUNT.pw_gid) for v in status[key].split()), status
    for key in ("CapInh", "CapEff", "CapPrm", "CapAmb", "CapBnd"):
        assert int(status[key], 16) == (1 << 10 | 1 << 12), f"{name} {key}: {status[key]}"
    assert int(status["NoNewPrivs"]) == 1
    assert all(int(group) == ACCOUNT.pw_gid for group in status["Groups"].split())
    return pid


def config(engine, p, state, directory, tls_port, mieru_port):
    mark = 1363349999
    cert, key = str(directory / "cert.pem"), str(directory / "key.pem")
    if engine == "mihomo":
        return f"mixed-port: {p}\nbind-address: '*'\nallow-lan: true\nipv6: true\nlog-level: info\nlisteners:\n  - {{name: tls, type: trojan, listen: 127.0.0.1, port: {tls_port}, users: [{{username: audit, password: audit}}], certificate: {cert}, private-key: {key}}}\n  - {{name: mieru, type: mieru, listen: 127.0.0.1, port: {mieru_port}, transport: TCP, users: {{audit: audit}}}}\nproxies:\n  - {{name: marked, type: direct, udp: true, routing-mark: {mark}}}\n  - {{name: bound, type: direct, udp: true, interface-name: lo}}\nrules: ['IP-CIDR,127.0.0.0/8,bound,no-resolve', 'IP-CIDR6,::1/128,bound,no-resolve', 'MATCH,marked']\n"
    if engine == "xray":
        return json.dumps({"log": {"loglevel": "info", "access": str(state / "access.log"), "error": str(state / "error.log")}, "inbounds": [{"listen": "::", "port": p, "protocol": "socks", "settings": {"udp": True}}, {"listen": "127.0.0.1", "port": tls_port, "protocol": "trojan", "settings": {"clients": [{"password": "audit"}]}, "streamSettings": {"security": "tls", "tlsSettings": {"certificates": [{"certificateFile": cert, "keyFile": key}]}}}], "outbounds": [{"protocol": "freedom", "streamSettings": {"sockopt": {"mark": mark, "interface": "lo"}}}]})
    if engine == "sing-box":
        return json.dumps({"log": {"level": "info", "output": str(state / "core.log")}, "inbounds": [{"type": "socks", "listen": "::", "listen_port": p}, {"type": "trojan", "listen": "127.0.0.1", "listen_port": tls_port, "users": [{"password": "audit"}], "tls": {"enabled": True, "certificate_path": cert, "key_path": key}}], "outbounds": [{"type": "direct", "routing_mark": mark, "bind_interface": "lo"}], "experimental": {"cache_file": {"enabled": True, "path": str(state / "cache.db")}}})
    return json.dumps({"server": "::", "server_port": p, "password": "qch-local-test", "method": "aes-256-gcm", "mode": "tcp_and_udp", "outbound_fwmark": mark, "outbound_bind_interface": "lo"})


def test(engine, shared):
    name = f"qch-permission-{os.getpid()}-{engine}-{'share' if shared else 'base'}"
    directory = ROOT / name
    directory.mkdir(mode=0o750)
    directory.chmod(0o750)
    os.chown(directory, 0, ACCOUNT.pw_gid)
    state = pathlib.Path("/var/lib") / name
    state.mkdir(mode=0o750)
    state.chmod(0o750)
    os.chown(state, ACCOUNT.pw_uid, ACCOUNT.pw_gid)
    # OpenRC CI explicitly sets ip_unprivileged_port_start=1024. Also inspect
    # actual bind capabilities on hosts with a more permissive sysctl.
    p = 1023
    while p > 900:
        try:
            with socket.socket(socket.AF_INET6) as s, socket.socket(socket.AF_INET6, socket.SOCK_DGRAM) as u:
                s.bind(("::", p))
                u.bind(("::", p))
            break
        except OSError:
            p -= 1
    binary = directory / ("ssserver" if engine == "shadowsocks-rust" else engine)
    shutil.copyfile(BIN / binary.name, binary)
    binary.chmod(0o755)
    cp = directory / ("config.yaml" if engine == "mihomo" else "config.json")
    tls_port, mieru_port = port(), port()
    if engine != "shadowsocks-rust":
        run("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=qch-local-test", "-keyout", str(directory / "key.pem"), "-out", str(directory / "cert.pem"))
        for path in (directory / "key.pem", directory / "cert.pem"):
            path.chmod(0o640)
            os.chown(path, 0, ACCOUNT.pw_gid)
    write(cp, config(engine, p, state, directory, tls_port, mieru_port))
    acl = directory / "qch-mainland-block.acl"
    write(acl, "[outbound_block_list]\n127.0.0.2\n")
    base = f"qagent-{engine}"
    asset = REPO / "deploy" / MANAGER / (base + ("@" if shared else "") + ".service" if MANAGER == "systemd" else base)
    content = asset.read_text()
    original_config = f"/etc/qagent/{engine}" + ("/shares/%i" if shared and MANAGER == "systemd" else "")
    content = content.replace(original_config, str(directory)).replace(f"/usr/local/lib/qagent/cores/{binary.name}", str(binary))
    content = content.replace(f"/var/lib/qcontrolhub-{engine}" + ("-%i" if shared and MANAGER == "systemd" else ""), str(state))
    content = content.replace(f"StateDirectory=qcontrolhub-{engine}" + ("-%i" if shared else ""), "StateDirectory=" + name)
    client = None
    unit = pathlib.Path("/run/systemd/system") / (name + ".service") if MANAGER == "systemd" else pathlib.Path("/etc/init.d") / name
    if MANAGER == "openrc":
        content = content.replace(base, name)
    write(unit, content)
    unit.chmod(0o644 if MANAGER == "systemd" else 0o755)
    try:
        if MANAGER == "systemd":
            run("systemctl", "daemon-reload")
        else:
            pathlib.Path("/run/openrc").mkdir(exist_ok=True)
            pathlib.Path("/run/openrc/softlevel").write_text("default\n")
        control = ["systemctl"] if MANAGER == "systemd" else ["rc-service", name]
        def action(a):
            run(*(control + [a, unit.name] if MANAGER == "systemd" else control + [a]))
        if engine == "xray" and not shared and MANAGER == "systemd":
            # Reproduce the historical main-unit omission with the real kernel.
            write(unit, content.replace("=CAP_NET_BIND_SERVICE CAP_NET_ADMIN", "=CAP_NET_BIND_SERVICE"))
            run("systemctl", "daemon-reload")
            action("start")
            time.sleep(0.3)
            old_pid = int(run("systemctl", "show", unit.name, "-p", "MainPID", "--value"))
            before_missing_marks = marked_packets()
            traffic(p)
            # Xray logs setsockopt failure but continues without the mark.
            # A successful TCP/UDP echo alone therefore misses this bug.
            assert marked_packets() == before_missing_marks, "historical Xray unexpectedly retained independent egress marks"
            assert "SO_MARK" in (state / "error.log").read_text()
            write(unit, content)
            run("systemctl", "daemon-reload")
            action("start")
            assert int(run("systemctl", "show", unit.name, "-p", "MainPID", "--value")) == old_pid, "reload unexpectedly changed process permissions"
            action("restart")
        else:
            action("start")
        if engine == "shadowsocks-rust":
            local = port()
            client = subprocess.Popen([str(BIN / "sslocal"), "-s", "127.0.0.1:" + str(p), "-b", "127.0.0.1:" + str(local), "-m", "aes-256-gcm", "-k", "qch-local-test", "-U"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        elif engine == "mihomo":
            local = port()
            client_config = directory / "client.yaml"
            write(client_config, f"mixed-port: {local}\nipv6: true\nproxies:\n  - {{name: tunnel, type: mieru, server: 127.0.0.1, port: {mieru_port}, transport: TCP, username: audit, password: audit, udp: true}}\nrules: ['MATCH,tunnel']\n")
            client = subprocess.Popen([str(binary), "-d", str(directory / "client-state"), "-f", str(client_config)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        else:
            local = p
        for _ in range(50):
            try:
                with socket.create_connection(("127.0.0.1", local), timeout=0.2):
                    break
            except OSError:
                time.sleep(0.1)
        pid = inspect_process(name, unit)
        if engine != "shadowsocks-rust":
            context = ssl._create_unverified_context()
            with socket.create_connection(("127.0.0.1", tls_port), timeout=3) as raw, context.wrap_socket(raw, server_hostname="qch-local-test") as tls:
                assert tls.getpeercert(binary_form=True), "core did not read certificate and private key"
        before_marks = marked_packets()
        traffic(p if engine == "mihomo" else local)
        after_marks = marked_packets()
        assert all(after > before for before, after in zip(before_marks, after_marks)), f"TCP or UDP was forwarded without the required SO_MARK: {before_marks} -> {after_marks}"
        if engine == "mihomo":
            traffic(local, TARGETS[:2])  # Mieru TCP carrying UDP, without fallback.
        if engine == "shadowsocks-rust":
            try:
                denied, _, _ = socks(local, 1, "127.0.0.2", BLOCKED_TARGET[1])
                with denied:
                    denied.sendall(b"GET / HTTP/1.0\r\n\r\n")
                    denied.recv(4096)
            except (OSError, RuntimeError, AssertionError):
                pass
            assert BLOCKED_TARGET[2][0] == 0, "core did not read/enforce its protected ACL"
        # An atomic binary replacement and a real service restart retain grants.
        candidate = binary.with_suffix(".new")
        shutil.copyfile(binary, candidate)
        candidate.chmod(0o755)
        candidate.replace(binary)
        candidate_config = cp.with_suffix(".new")
        write(candidate_config, cp.read_text())
        candidate_config.replace(cp)
        action("restart")
        time.sleep(0.3)
        assert inspect_process(name, unit) != pid, "binary update did not replace the process"
        before_marks = marked_packets()
        traffic(p if engine == "mihomo" else local)
        assert all(after > before for before, after in zip(before_marks, marked_packets())), "binary/config replacement lost actual outbound marks"
        traffic(local, TARGETS[:2] if engine == "mihomo" else None)
        if MANAGER == "openrc":
            # The shipped logger uses O_APPEND. Copytruncate keeps its inode,
            # owner and mode and must remain writable after rotation.
            log = state / "access.log" if engine == "xray" else state / "core.log" if engine == "sing-box" else pathlib.Path(f"/var/log/qagent/{name}.log")
            inode = log.stat().st_ino
            log.write_text("")
            traffic(local, TARGETS[:2] if engine == "mihomo" else None)
            assert log.stat().st_ino == inode and log.stat().st_size > 0
        action("stop")
        before = sum(t[1][2][0] + t[2][2][0] for t in TARGETS)
        stopped_passed = False
        try:
            traffic(local, TARGETS[:2] if engine == "mihomo" else None)
            stopped_passed = True
        except (OSError, RuntimeError, AssertionError):
            pass
        assert not stopped_passed, "stopped core forwarded traffic"
        assert before == sum(t[1][2][0] + t[2][2][0] for t in TARGETS)
        if engine == "xray":
            assert (state / "access.log").stat().st_size > 0
        if engine == "sing-box":
            assert (state / "core.log").stat().st_size > 0
            assert (state / "cache.db").stat().st_size > 0
        print(f"PASS {MANAGER}/{engine}/{'shared' if shared else 'normal'} uid={ACCOUNT.pw_uid} caps=0x1400; low-port, marked+lo-bound TCP/UDP IPv4/IPv6, certificate/key reads where supported, file writes, atomic binary update+restart, {'copytruncate log rotation' if MANAGER == 'openrc' else 'journal output'}, stopped-core control", flush=True)
    except Exception:
        print(run("journalctl", "--namespace=qagent-cores", "-u", unit.name, "-n", "30", "--no-pager", check=False) if MANAGER == "systemd" else pathlib.Path(f"/var/log/qagent/{name}.log").read_text(), flush=True)
        raise
    finally:
        if MANAGER == "systemd":
            run("systemctl", "stop", unit.name, check=False)
            run("systemctl", "reset-failed", unit.name, check=False)
        else:
            run("rc-service", name, "stop", check=False)
        if client:
            client.terminate()
            client.wait(timeout=5)
        unit.unlink(missing_ok=True)
        shutil.rmtree(state)
        shutil.rmtree(directory)
        if MANAGER == "systemd":
            run("systemctl", "daemon-reload")


try:
    # Mihomo deliberately skips marks on non-global socket addresses. Test
    # marks against owned benchmark/ULA aliases as well as ordinary loopback.
    for prefix in (f"198.18.{(os.getpid() // 250) % 250}.{os.getpid() % 250 + 1}/32", f"fd42:{os.getpid() & 0xffff:x}:1::1/128"):
        run("ip", "address", "add", prefix, "dev", "lo")
        ALIASES.append(prefix)
        host = prefix.split("/")[0]
        TARGETS.append((host, backend(host, False), backend(host, True)))
    run("nft", "add", "table", "inet", TABLE)
    run("nft", "add", "chain", "inet", TABLE, "egress", "{ type filter hook output priority 0; policy accept; }")
    for protocol, position in (("tcp", 1), ("udp", 2)):
        ports = "{ " + ", ".join(str(target[position][1]) for target in TARGETS) + " }"
        run("nft", "add", "rule", "inet", TABLE, "egress", protocol, "dport", ports, "meta", "mark", "1363349999", "counter")
    for engine in ("mihomo", "xray", "sing-box", "shadowsocks-rust"):
        for shared in (False, True):
            test(engine, shared)
finally:
    run("nft", "delete", "table", "inet", TABLE, check=False)
    for prefix in ALIASES:
        run("ip", "address", "del", prefix, "dev", "lo", check=False)
    shutil.rmtree(ROOT)
