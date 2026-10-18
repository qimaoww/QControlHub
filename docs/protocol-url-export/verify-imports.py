"""Verify test exports through a running official Sub-Store release and Mihomo parser output."""
import json
from pathlib import Path
import urllib.request

ROOT = Path(__file__).resolve().parent / "evidence"
NAME = "香港 🚀 + %2F @:/?#&="
SYNC_NAME = "同步 🚀 + %2F @:/?#&="
SCHEMES = ("ss://", "vless://", "vmess://", "trojan://", "hysteria2://", "tuic://", "socks5://", "anytls://")


def parse(value):
    request = urllib.request.Request(
        "http://127.0.0.1:34129/api/preview/sub",
        json.dumps({"source": "local", "content": value, "process": []}).encode(),
        {"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=10) as response:
        return json.load(response).get("data", {}).get("processed", [])


def checks(sample, nodes, name, client):
    if len(nodes) != 1:
        return {"count": False}
    node, source = nodes[0], sample["input"]
    protocol = source["protocol"]
    result = {
        "name": node.get("name") == name,
        "server": node.get("server") == sample["address"],
        "port": int(node.get("port", 0)) == source["port"],
    }
    key = "uuid" if protocol in ("vless", "vless-xhttp-reality", "vmess", "tuic") else "password"
    result["credential"] = node.get(key) == source["credential"]
    if protocol == "tuic":
        result["password"] = node.get("password") == source["secondary_credential"]
    if protocol == "socks":
        result["username"] = node.get("username") == source["username"]
    if source["tls_enabled"]:
        result["sni"] = node.get("sni", node.get("servername")) == "tls.example.test"
    if source["tls_alpn"]:
        result["alpn"] = node.get("alpn", ["h3"] if protocol == "hysteria2" else None) == source["tls_alpn"].split(",")
    if source["reality_enabled"]:
        reality = node.get("reality-opts", {})
        result["reality"] = (
            reality.get("public-key") == source["reality_public_key"]
            and reality.get("short-id") == source["reality_short_id"]
            and node.get("flow") == source["flow"]
        )
    transport = source["transport"]
    if transport == "websocket":
        ws = node.get("ws-opts", {})
        result["path"] = ws.get("path") == source["transport_path"]
        if source["transport_host"]:
            result["host"] = ws.get("headers", {}).get("Host") == source["transport_host"]
    if transport == "grpc":
        result["serviceName"] = node.get("grpc-opts", {}).get("grpc-service-name") == source["transport_path"]
    if transport == "xhttp":
        xhttp = node.get("xhttp-opts", {})
        result["xhttp"] = all(xhttp.get(k) == source[v] for k, v in (("mode", "xhttp_mode"), ("path", "transport_path"), ("host", "transport_host")))
    return result


def record(sample, nodes, name, client):
    verified = checks(sample, nodes, name, client)
    return {"case": sample["name"], "checks": verified, "pass": all(verified.values()), "nodes": nodes}


raw = {}
for phase in ("before", "after"):
    raw[phase] = []
    for sample in json.loads((ROOT / f"{phase}-urls.json").read_text()):
        profile = sample["profile"]
        if not profile["uri"].startswith(SCHEMES):
            raw[phase].append({"case": sample["name"], "status": "native format required"})
            continue
        raw[phase].append(record(sample, parse(profile["uri"]), NAME, "substore"))

samples = json.loads((ROOT / "after-urls.json").read_text())
sync_values = json.loads((ROOT / "sync-values.json").read_text())
sync = [record(s, parse(sync_values[s["name"]]), SYNC_NAME, "substore") for s in samples if s["name"] in sync_values]
mihomo_parsed = json.loads((ROOT / "mihomo-parsed.json").read_text())
mihomo = [record(s, mihomo_parsed[s["name"]], NAME, "mihomo") for s in samples if s["profile"]["uri"].startswith(SCHEMES)]
for filename, value in (("substore-imports.json", raw), ("substore-sync-imports.json", sync), ("mihomo-checks.json", mihomo)):
    (ROOT / filename).write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
print("Sub-Store raw URLs:", sum(r.get("pass", False) for r in raw["after"]), "/", sum("pass" in r for r in raw["after"]))
print("Sub-Store sync:", sum(r["pass"] for r in sync), "/", len(sync))
print("Mihomo:", sum(r["pass"] for r in mihomo), "/", len(mihomo))
assert all(r["pass"] for r in sync)
assert all(r["pass"] for r in mihomo)
