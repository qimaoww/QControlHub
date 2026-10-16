"""Network setup for disposable core permission fixtures."""
import errno
import socket
import struct
import time


def add_loopback_alias(prefix, run):
    # These unique aliases belong to the local test only. DAD is unnecessary
    # on loopback and can leave a newly added IPv6 address tentative on runners.
    args = ["ip", "address", "add", prefix, "dev", "lo"]
    if ":" in prefix:
        args.append("nodad")
    run(*args)


def bind_backend_socket(host, udp, timeout=3):
    family = socket.AF_INET6 if ":" in host else socket.AF_INET
    sock = socket.socket(family, socket.SOCK_DGRAM if udp else socket.SOCK_STREAM)
    deadline = time.monotonic() + timeout
    try:
        while True:
            try:
                sock.bind((host, 0))
                return sock
            except OSError as error:
                # Only address readiness is transient here. Permission, port
                # and other setup errors must remain visible immediately.
                if error.errno != errno.EADDRNOTAVAIL:
                    raise
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError(
                        f"fixture address {host} is not bindable after {timeout:g}s; "
                        "check that the test alias exists and IPv6 DAD has completed"
                    ) from error
                time.sleep(min(0.05, remaining))
    except BaseException:
        sock.close()
        raise


def wait_tcp_listener(host, port, timeout=5):
    deadline = time.monotonic() + timeout
    while True:
        try:
            with socket.create_connection((host, port), timeout=min(0.2, max(0.001, deadline - time.monotonic()))):
                return
        except OSError as error:
            if error.errno not in (errno.ECONNREFUSED, errno.ETIMEDOUT) and not isinstance(error, TimeoutError):
                raise
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(f"fixture TCP listener {host}:{port} did not become ready within {timeout:g}s") from error
            time.sleep(min(0.05, remaining))


def receive(sock, size):
    data = b""
    while len(data) < size:
        more = sock.recv(size - len(data))
        if not more:
            raise EOFError(f"unexpected SOCKS EOF after {len(data)}/{size} bytes")
        data += more
    return data


def address(host, p):
    family = socket.AF_INET6 if ":" in host else socket.AF_INET
    return bytes([4 if family == socket.AF_INET6 else 1]) + socket.inet_pton(family, host) + struct.pack("!H", p)


def socks(p, command, host, target, timeout=3):
    sock = socket.create_connection(("127.0.0.1", p), timeout=timeout)
    try:
        sock.sendall(b"\x05\x01\x00")
        assert receive(sock, 2) == b"\x05\x00"
        sock.sendall(b"\x05" + bytes([command, 0]) + address(host, target))
        header = receive(sock, 4)
        assert header[:2] == b"\x05\x00", header
        size = 16 if header[3] == 4 else 4
        peer = socket.inet_ntop(socket.AF_INET6 if size == 16 else socket.AF_INET, receive(sock, size))
        bound = struct.unpack("!H", receive(sock, 2))[0]
        return sock, peer, bound
    except BaseException:
        sock.close()
        raise


def tcp_roundtrip(proxy_port, host, target_port, request, response, timeout=3):
    sock, _, _ = socks(proxy_port, 1, host, target_port, timeout=timeout)
    with sock:
        sock.sendall(request)
        actual = receive(sock, len(response))
        assert actual == response, f"TCP payload mismatch: {actual!r} != {response!r}"


def wait_for_forwarding(probe, description, timeout=5):
    """Wait for actual forwarding, which may start after a proxy listens.

    The probe gets the remaining budget for its socket timeout. Only transient
    connection failures are retried; rejected SOCKS requests, wrong payloads and
    permission errors fail immediately. The strict traffic matrix runs afterward.
    """
    deadline = time.monotonic() + timeout
    while True:
        remaining = deadline - time.monotonic()
        try:
            probe(min(1, max(0.001, remaining)))
            return
        except (EOFError, ConnectionRefusedError, ConnectionResetError, BrokenPipeError, TimeoutError) as error:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError(
                    f"fixture forwarding {description} did not become ready within {timeout:g}s; "
                    f"last probe: {type(error).__name__}: {error}"
                ) from error
            time.sleep(min(0.05, remaining))
