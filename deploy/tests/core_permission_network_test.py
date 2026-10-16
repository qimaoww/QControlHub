#!/usr/bin/env python3
"""Run normally for unit regressions, or in a disposable network namespace:

sudo unshare --net env QCH_TEST_PERMISSION_NETNS=1 python3 -B \
    deploy/tests/core_permission_network_test.py
"""
import errno
import http.server
import json
import os
import pathlib
import socket
import subprocess
import tempfile
import threading
import unittest
from unittest.mock import MagicMock, Mock, patch

from core_permission_network import (
    add_loopback_alias, bind_backend_socket, socks, tcp_roundtrip,
    wait_for_forwarding, wait_tcp_listener,
)


class BindReadinessTests(unittest.TestCase):
    def test_transient_address_readiness_preserves_protocol(self):
        for host, udp, family, kind in (
            ("198.18.1.1", False, socket.AF_INET, socket.SOCK_STREAM),
            ("fd42:1::1", True, socket.AF_INET6, socket.SOCK_DGRAM),
        ):
            with self.subTest(host=host), patch("core_permission_network.socket.socket") as factory, \
                    patch("core_permission_network.time.monotonic", side_effect=[0, 0.01]), \
                    patch("core_permission_network.time.sleep") as sleep:
                sock = factory.return_value
                sock.bind.side_effect = [OSError(errno.EADDRNOTAVAIL, "tentative"), None]
                self.assertIs(bind_backend_socket(host, udp), sock)
                factory.assert_called_once_with(family, kind)
                self.assertEqual(sock.bind.call_count, 2)
                sock.bind.assert_called_with((host, 0))
                sleep.assert_called_once_with(0.05)
                sock.close.assert_not_called()

    def test_missing_address_has_deadline_and_closes_socket(self):
        with patch("core_permission_network.socket.socket") as factory, \
                patch("core_permission_network.time.monotonic", side_effect=[0, 2.99, 3]), \
                patch("core_permission_network.time.sleep") as sleep:
            failure = OSError(errno.EADDRNOTAVAIL, "missing address")
            factory.return_value.bind.side_effect = failure
            with self.assertRaisesRegex(TimeoutError, "fd42:1::1.*after 3s") as caught:
                bind_backend_socket("fd42:1::1", False)
            self.assertIs(caught.exception.__cause__, failure)
            factory.return_value.close.assert_called_once()
            self.assertEqual(sleep.call_count, 1)
            self.assertLess(sleep.call_args.args[0], 0.05)

    def test_other_bind_errors_fail_immediately_and_close_socket(self):
        for code in (errno.EACCES, errno.EADDRINUSE):
            with self.subTest(code=code), patch("core_permission_network.socket.socket") as factory, \
                    patch("core_permission_network.time.sleep") as sleep:
                failure = OSError(code, "permanent error")
                factory.return_value.bind.side_effect = failure
                with self.assertRaises(OSError) as caught:
                    bind_backend_socket("::1", False)
                self.assertIs(caught.exception, failure)
                factory.return_value.bind.assert_called_once()
                factory.return_value.close.assert_called_once()
                sleep.assert_not_called()

    def test_loopback_alias_skips_dad_only_for_ipv6(self):
        run = Mock()
        add_loopback_alias("198.18.1.1/32", run)
        add_loopback_alias("fd42:1::1/128", run)
        self.assertEqual(run.call_args_list[0].args, ("ip", "address", "add", "198.18.1.1/32", "dev", "lo"))
        self.assertEqual(run.call_args_list[1].args, ("ip", "address", "add", "fd42:1::1/128", "dev", "lo", "nodad"))

    def test_listener_waits_for_server_and_closes_probe(self):
        with patch("core_permission_network.socket.create_connection") as connect, \
                patch("core_permission_network.time.monotonic", side_effect=[0, 0, 0.01, 0.02]), \
                patch("core_permission_network.time.sleep") as sleep:
            probe = MagicMock()
            connect.side_effect = [ConnectionRefusedError(errno.ECONNREFUSED, "starting"), probe]
            wait_tcp_listener("127.0.0.1", 1234)
            self.assertEqual(connect.call_count, 2)
            sleep.assert_called_once_with(0.05)
            probe.__exit__.assert_called_once()

    def test_listener_timeout_fails_instead_of_continuing_traffic(self):
        with patch("core_permission_network.socket.create_connection", side_effect=ConnectionRefusedError(errno.ECONNREFUSED, "stopped")), \
                patch("core_permission_network.time.monotonic", side_effect=[0, 0, 5]), \
                patch("core_permission_network.time.sleep") as sleep:
            with self.assertRaisesRegex(TimeoutError, "127.0.0.1:1234.*within 5s"):
                wait_tcp_listener("127.0.0.1", 1234)
            sleep.assert_not_called()


class ForwardingReadinessTests(unittest.TestCase):
    def test_open_listener_can_close_socks_until_forwarding_is_ready(self):
        probe = Mock(side_effect=[EOFError("SOCKS accepted before tunnel ready"), None])
        with patch("core_permission_network.time.sleep") as sleep:
            wait_for_forwarding(probe, "proxy=127.0.0.1:1234")
        self.assertEqual(probe.call_count, 2)
        sleep.assert_called_once_with(0.05)

    def test_permanent_failure_times_out_with_last_error(self):
        failure = EOFError("closed after SOCKS handshake")
        probe = Mock(side_effect=failure)
        with patch("core_permission_network.time.monotonic", side_effect=[0, 0, 5]), \
                patch("core_permission_network.time.sleep") as sleep:
            with self.assertRaisesRegex(TimeoutError, "127.0.0.1:1234.*within 5s.*EOFError") as caught:
                wait_for_forwarding(probe, "proxy=127.0.0.1:1234")
        self.assertIs(caught.exception.__cause__, failure)
        probe.assert_called_once_with(1)
        sleep.assert_not_called()

    def test_payload_rejection_and_permission_errors_fail_immediately(self):
        for failure in (AssertionError("SOCKS rejected"), AssertionError("wrong payload"), PermissionError("denied")):
            with self.subTest(failure=failure), patch("core_permission_network.time.sleep") as sleep:
                probe = Mock(side_effect=failure)
                with self.assertRaises(type(failure)) as caught:
                    wait_for_forwarding(probe, "proxy=127.0.0.1:1234")
                self.assertIs(caught.exception, failure)
                probe.assert_called_once()
                sleep.assert_not_called()

    def test_failed_socks_probe_closes_connection(self):
        for reply, expected in ((b"", EOFError), (b"\x05\xff", AssertionError)):
            with self.subTest(reply=reply), patch("core_permission_network.socket.create_connection") as connect:
                connect.return_value.recv.return_value = reply
                with self.assertRaises(expected):
                    socks(1234, 1, "127.0.0.1", 4321)
                connect.return_value.close.assert_called_once()


@unittest.skipUnless(os.environ.get("QCH_PERMISSION_BIN"), "requires verified official Mihomo binary")
class RealForwardingReadinessTests(unittest.TestCase):
    def test_mihomo_listens_and_accepts_socks_before_tunnel_is_running(self):
        # Gate a local provider to deterministically hold the official core
        # between updateListeners and tunnel.OnRunning. No Internet is used.
        requested, release = threading.Event(), threading.Event()

        class Provider(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                requested.set()
                if not release.wait(10):
                    return
                body = b"proxies: [{name: fixture, type: direct}]\n"
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *args):
                pass

        with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Provider) as provider, \
                bind_backend_socket("127.0.0.1", False) as target, \
                tempfile.TemporaryDirectory(prefix="qch-mihomo-readiness-") as directory:
            provider.daemon_threads = True
            provider_thread = threading.Thread(target=provider.serve_forever, daemon=True)
            provider_thread.start()
            target.listen()
            target.settimeout(5)
            hits = []
            request, response = b"qch-request", b"qch-response"

            def echo():
                conn, _ = target.accept()
                with conn:
                    conn.settimeout(3)
                    body = b""
                    while len(body) < len(request):
                        data = conn.recv(len(request) - len(body))
                        if not data:
                            return
                        body += data
                    hits.append(body)
                    conn.sendall(response)

            echo_thread = threading.Thread(target=echo, daemon=True)
            echo_thread.start()
            with bind_backend_socket("127.0.0.1", False) as reservation:
                proxy_port = reservation.getsockname()[1]
            path = pathlib.Path(directory)
            config = path / "config.yaml"
            config.write_text(
                f"mixed-port: {proxy_port}\nbind-address: 127.0.0.1\n"
                f"proxy-providers:\n  startup:\n    type: http\n"
                f"    url: http://127.0.0.1:{provider.server_port}/provider.yaml\n"
                "    path: ./provider.yaml\nrules: ['MATCH,DIRECT']\n"
            )
            with (path / "core.log").open("w+") as log:
                core = subprocess.Popen(
                    [str(pathlib.Path(os.environ["QCH_PERMISSION_BIN"]) / "mihomo"),
                     "-d", directory, "-f", str(config)],
                    stdout=log, stderr=subprocess.STDOUT,
                )
                try:
                    self.assertTrue(requested.wait(5), "Mihomo did not reach gated provider initialization")
                    wait_tcp_listener("127.0.0.1", proxy_port)

                    def probe(timeout=1):
                        tcp_roundtrip(proxy_port, "127.0.0.1", target.getsockname()[1],
                                      request, response, timeout=timeout)

                    # The previous CI immediately ran traffic after listening;
                    # the core closes a successful SOCKS connection with EOF.
                    with self.assertRaises((EOFError, ConnectionResetError)):
                        probe()
                    self.assertEqual(hits, [])

                    def startup_probe(timeout):
                        try:
                            probe(timeout)
                        except (EOFError, ConnectionResetError):
                            release.set()
                            raise

                    wait_for_forwarding(startup_probe, f"Mihomo 127.0.0.1:{proxy_port}")
                    self.assertEqual(hits, [request])
                    self.assertIsNone(core.poll(), "core exited during startup")
                except BaseException:
                    log.flush()
                    log.seek(0)
                    print(log.read(), flush=True)
                    raise
                finally:
                    release.set()
                    core.terminate()
                    core.wait(timeout=5)
                    provider.shutdown()
                    provider_thread.join(timeout=5)
                    echo_thread.join(timeout=5)


@unittest.skipUnless(os.environ.get("QCH_TEST_PERMISSION_NETNS") == "1", "requires disposable network namespace")
class RealAddressReadinessTests(unittest.TestCase):
    @staticmethod
    def run_command(*args):
        return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT, timeout=10)

    @classmethod
    def setUpClass(cls):
        # QCH_TEST_PERMISSION_NETNS must only be set inside unshare --net.
        cls.run_command("ip", "link", "set", "lo", "up")

    def test_owned_loopback_aliases_bind_immediately(self):
        for index in range(5):
            for prefix in (f"198.18.1.{index + 1}/32", f"fd42:1::{index + 1}/128"):
                with self.subTest(prefix=prefix):
                    add_loopback_alias(prefix, self.run_command)
                    try:
                        host = prefix.split("/")[0]
                        for udp in (False, True):
                            # No waiting allowed: nodad must make the alias ready.
                            with bind_backend_socket(host, udp, timeout=0) as sock:
                                self.assertEqual(sock.getsockname()[0], host)
                    finally:
                        self.run_command("ip", "address", "del", prefix, "dev", "lo")

    def test_tentative_ipv6_reproduces_failure_then_becomes_bindable(self):
        self.run_command("ip", "link", "add", "qchdadtest", "type", "veth", "peer", "name", "qchdadpeer")
        try:
            for name in ("qchdadtest", "qchdadpeer"):
                self.run_command("ip", "link", "set", name, "up")
            for option in ("accept_dad", "dad_transmits"):
                self.run_command("sysctl", "-qw", f"net.ipv6.conf.qchdadtest.{option}=1")
            host = "fd42:2::1"
            self.run_command("ip", "-6", "address", "add", host + "/128", "dev", "qchdadtest")
            addresses = json.loads(self.run_command("ip", "-j", "-6", "address", "show", "dev", "qchdadtest"))
            entry = next(a for a in addresses[0]["addr_info"] if a["local"] == host)
            self.assertIn("tentative", entry)
            with socket.socket(socket.AF_INET6) as sock:
                with self.assertRaises(OSError) as caught:
                    sock.bind((host, 0))
                self.assertEqual(caught.exception.errno, errno.EADDRNOTAVAIL)
            with bind_backend_socket(host, False) as sock:
                self.assertEqual(sock.getsockname()[0], host)
            with bind_backend_socket(host, True) as sock:
                self.assertEqual(sock.getsockname()[0], host)
        finally:
            self.run_command("ip", "link", "del", "qchdadtest")


if __name__ == "__main__":
    unittest.main(verbosity=2)
