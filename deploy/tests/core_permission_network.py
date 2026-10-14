"""Network setup for disposable core permission fixtures."""
import errno
import socket
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
