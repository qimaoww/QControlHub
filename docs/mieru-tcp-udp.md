# Mieru UDP forwarding with a TCP tunnel

## Root cause and affected deployments

QControlHub's historical Mihomo systemd units allowed `AF_UNIX AF_INET AF_INET6`
and omitted `AF_NETLINK`. On Linux, Mihomo's direct UDP outbound checks whether
the peer address belongs to a local interface. That check enumerates interfaces
through netlink. The service sandbox prevented this query and the UDP outbound
failed before sending a datagram to its destination:

```text
[UDP] dial qch-trf-8443-example (match InName/mieru-example) ... --> 1.1.1.1:53
error: route ip+net: netlinkrib: address family not supported by protocol
```

TCP business traffic remained functional because its loopback check does not
take this interface-enumeration path. The same restriction also affects UDP
business traffic carried by a Mieru UDP tunnel. Changing the tunnel transport or
adding another `udp: true` declaration does not repair this service policy.

The failing deployment used Mihomo **v1.19.32**, built with Go **1.26.8**, with
embedded Mieru **v3.38.0**, on Debian/systemd. Its Agent reported revision
`fd969c20da8699177cf061ce5c249c9e76f1cfe2`. Clients running Mihomo **v1.19.31**
(Mieru **v3.37.0**) and **v1.19.32** both reproduced the problem. A native Mieru
v3.37.0 SDK client also reproduced the UDP timeout. This is a project service
policy defect; no upstream core downgrade or upgrade is required.

Official source supporting the diagnosis:

- [Mihomo direct UDP outbound](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/adapter/outbound/direct.go)
  calls the [loopback detector](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/loopback/detector.go),
  which uses [local interface discovery](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/iface/iface.go).
- [Mihomo Mieru listener](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/listener/mieru/server.go)
  receives UDP associations and returns packets through the Mieru connection.
  [The outbound adapter](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/adapter/outbound/mieru.go)
  uses Mieru's
  [packet-over-stream framing](https://github.com/enfein/mieru/blob/v3.38.0/apis/common/packet_over_stream.go)
  and [UDP association wrapper](https://github.com/enfein/mieru/blob/v3.38.0/apis/common/udp_associate_wrapper.go).
- [systemd execution policy](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#RestrictAddressFamilies=)
  documents the address-family filter. The additive repair directive is also
  exercised under a real systemd manager by the regression below.

## Configuration and forwarding evidence

The actual loaded server configuration was read through an Agent
`read-managed-config` task, and compared with the panel's client export. The
relevant shape below replaces the account, names and endpoint with examples:

```yaml
# Server
listeners:
  - name: mieru-example
    type: mieru
    listen: 0.0.0.0
    port: 8443
    transport: TCP
    users: {example-user: example-password}
    traffic-pattern: GgQIARAIIggIARABGAYgCCoFCEAQgAEyBAgEEAc=
proxies:
  - name: qch-trf-8443-example
    type: direct
    udp: true
    routing-mark: 1363353851
rules:
  - IN-NAME,mieru-example,qch-trf-8443-example
  - MATCH,DIRECT

# Exported client node
# {name: LIVE, type: mieru, server: 192.0.2.1, port: 8443,
#  username: example-user, password: example-password, udp: true,
#  transport: TCP, traffic-pattern: GgQIARAIIggIARABGAYgCCoFCEAQgAEyBAgEEAc=}
```

`transport: TCP` selects the tunnel between the two Mieru endpoints. `udp: true`
declares that the client proxy can accept UDP business traffic. The server's
destination socket still uses UDP. Accordingly, a TCP Mieru listener needs TCP
ingress on its listener port and UDP egress/return traffic for destination ports;
opening UDP ingress on the same Mieru port does not fix this failure.

Both clients used SOCKS5 UDP ASSOCIATE, a one-member `TEST` group selecting the
target Mieru node, and `MATCH,TEST`. HTTPS succeeded; raw UDP DNS queries to two
independent resolvers timed out. Client UDP route logs confirmed that the node
was selected. Server UDP logs confirmed the datagram had crossed the TCP tunnel
and reached the listener's independent direct outbound. The outbound failed at
interface discovery, before destination send or return processing. No interface
binding override was present. A local echo target's receive counter remained
unchanged under the same historical systemd restriction and increased after
the repair. Thus the identified failure precedes destination routing/NAT and
is independent of public DNS behavior.

The configuration regression follows `Generate` -> `MutateGenerated` ->
`PrepareIndependentEgress` -> `Parse` -> client profile -> display formatting ->
Sub-Store node selection and renaming, for TCP and UDP transport. It verifies
that UDP capability, transport, credentials and traffic pattern remain intact.
Existing generation, parser, mutation and persistence tests remain in the suite.
The new Sub-Store check verifies the submitted node content; it does not claim
that an external Sub-Store instance or an arbitrary downstream client preserves
every field.

## Repair and rollout

The normal and shared Mihomo systemd templates now allow `AF_NETLINK`. On normal
managed services, Agent deployment/start/restart/import preparation checks the
effective address-family property even when capabilities are already correct.
It adds the netlink allowance to the existing QControlHub capability drop-in.
Shared deployments recognize and refresh the exact historical shared template.
Unknown service definitions remain rejected. OpenRC has no such systemd filter.

Existing processes need a restart to use the updated socket policy. For an
existing standard QAgent-managed Mihomo service, the immediate manual repair is:

```sh
sudo install -d -m 0755 /etc/systemd/system/qagent-mihomo.service.d
sudo tee /etc/systemd/system/qagent-mihomo.service.d/10-qcontrolhub-bind-low-ports.conf >/dev/null <<'EOF'
[Service]
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN
RestrictAddressFamilies=AF_NETLINK
EOF
sudo systemctl daemon-reload
sudo systemctl restart qagent-mihomo.service
sudo systemctl show qagent-mihomo.service -p RestrictAddressFamilies
```

Restart briefly interrupts active connections. This only extends socket-family
access; it preserves the existing capability and filesystem restrictions. For
rollback, restore the previous managed template/drop-in and restart; the old
restriction will also restore the UDP failure. Administrator-managed services
require review of their own effective policy.

## Reproducible real communication regression

On a Linux host running systemd, place an official Mihomo release at
`/absolute/core-root/bin/mihomo`, then run from the repository:

```sh
sudo env QCH_LIVE_MIERU_SYSTEMD_TEST=1 \
  QCH_LIVE_CORE_ROOT=/absolute/core-root \
  "$(command -v go)" test ./internal/agent \
  -run '^TestMieruUDPWithSystemdSocketPolicy$' -count=1 -v
```

This launches real systemd services with the project's socket-family policies,
and real Mihomo clients consuming project-generated exports. It does not emulate
the complete filesystem sandbox. Each positive case checks 24 binary UDP
echoes round-robin across two IPv4 targets and one IPv6 loopback target, raw UDP
DNS with a unique name and TTL zero, recovery after client/server UDP mappings
expire, a fresh SOCKS association, and TCP HTTP before and after. UDP route logs
must identify both the selected proxy and compiled independent outbound. A
stopped-tunnel check proves that successful traffic did not use direct fallback.
DNS uses only UDP sockets and verifies the transaction, question and answer;
there is no DNS cache or TCP fallback in the fixture.

| Socket policy / tunnel | Mihomo v1.19.31 / Mieru v3.37.0 | Mihomo v1.19.32 / Mieru v3.38.0 |
| --- | --- | --- |
| Historical filter / TCP | TCP passes, UDP blocked before target | TCP passes, UDP blocked before target |
| Updated normal template / TCP | Bidirectional UDP and TCP pass | Bidirectional UDP and TCP pass |
| Historical filter plus repair directive / TCP | Bidirectional UDP and TCP pass | Bidirectional UDP and TCP pass |
| Updated shared template / TCP | Bidirectional UDP and TCP pass | Bidirectional UDP and TCP pass |
| Updated normal template / UDP control | Bidirectional UDP and TCP pass | Bidirectional UDP and TCP pass |

The CI matrix downloads these exact official release assets, verifies their
SHA-256 digests, records core/build dependency versions, and runs the systemd
communication regression. Ordinary `make check` covers unit/configuration
regressions; it skips this opt-in test without a real systemd host and core.

The reported remote node was confirmed failing before repair. After its owner
applied the service drop-in and restarted Mihomo, both client versions passed
24 unique raw UDP DNS queries round-robin to two public resolvers, recovery
after 65 seconds idle with the association held open, a fresh association to
both targets, and HTTPS before/after. Reply source address, transaction ID and
question were checked; truncated replies were rejected and no TCP DNS fallback
was used. UDP route logs confirmed `TEST[LIVE]`. Retrieving fresh server logs
through the panel timed out; the earlier server logs established the failing
stage, and the post-repair DNS datagrams provide the remote communication result.

The local regression additionally proves controlled binary echo replies,
independent egress selection and IPv6 destination datagrams over an IPv4
tunnel. Public IPv6 reachability, an IPv6 outer tunnel, TUN ingress, external
Sub-Store processing, the complete filesystem sandbox, direct inspection of
production firewall/NAT rules and long-duration soak behavior remain outside
the verified scope.
