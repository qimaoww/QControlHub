#!/usr/bin/env python3
"""Call the configured QControlHub API with bounded waits and safe output."""
import argparse
import datetime
import email.utils
import hashlib
import ipaddress
import json
import math
import os
from pathlib import Path
import queue
import re
import secrets
import socket
import ssl
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

BODY_LIMIT = 16 * 1024 * 1024
RESPONSE_LIMIT = 32 * 1024 * 1024
METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE")
ID = re.compile(r"(?:agt|cfg|tsk|shr|usr|crt|key|req|tpl|ndg|trf|sst|ssi|cor|cac|log)_[0-9a-f]{16}")
TASK_PATH = re.compile(r"(?:/tasks|/agents/agt_[0-9a-f]{16}/tls-migrations)/(tsk_[a-f0-9]{16})")
MIGRATE_PATH = re.compile(r"(/agents/agt_[0-9a-f]{16})/configs/(?:xray|mihomo|sing-box)/server-inbounds/[^/]+/tls-frontend/migrate")
SUBMISSION_PATH = re.compile(r"/agents/agt_[0-9a-f]{16}/configs/(?:mihomo|xray|sing-box|ss-rust)/(?:server-inbounds|source|outbounds)")
STATES = {"pending", "running", "succeeded", "failed", "canceled", "online", "offline", "unknown",
          "verifying", "rolling_back", "rolled_back", "recovery_incomplete", "blocked", "runtime_evidence_required"}
CODES = {"invalid_input", "authentication_failed", "permission_denied", "not_found", "conflict", "rate_limited", "internal_error", "service_unavailable",
         "stale_version", "instance_changed", "unsupported_mode", "port_group_incomplete", "port_group_unauthorized",
         "dependency_missing", "port_conflict", "route_conflict", "proxy_type_conflict", "business_validation_failed",
         "rollback_failed", "deployment_drift", "certificate_unauthorized", "certificate_binding_changed",
         "certificate_change_required", "ingress_unauthorized", "pending_tasks", "agent_offline",
         "unsupported_target_configuration", "state_unverified", "tls_certificate_conflict", "backend_port_conflict",
         "task_schema_incompatible", "stale_plan", "agent_upgrade_required", "read_timeout"}
ENGINES = {"sing-box", "xray", "mihomo", "ss-rust"}


class Failure(Exception):
    def __init__(self, classification, **fields):
        self.result = {"classification": classification, **fields}


class SafeParser(argparse.ArgumentParser):
    def error(self, message):
        raise Failure("invalid_arguments")


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, target):
        return None


def target_url(base, path):
    panel = urllib.parse.urlsplit(base)
    try:
        loopback = ipaddress.ip_address(panel.hostname or "").is_loopback
    except ValueError:
        loopback = panel.hostname == "localhost"
    if not panel.hostname or (panel.scheme != "https" and not (panel.scheme == "http" and loopback)):
        raise Failure("invalid_base")
    if panel.username is not None or panel.password is not None or panel.query or panel.fragment or not panel.path.rstrip("/").endswith("/api/v1"):
        raise Failure("invalid_base")
    request = urllib.parse.urlsplit(path)
    decoded = urllib.parse.unquote(request.path)
    if (not path.startswith("/") or request.scheme or request.netloc or decoded.startswith("//")
            or request.fragment or "\\" in decoded or "%" in decoded or any(ord(c) < 32 for c in path + decoded)
            or any(part in (".", "..") for part in decoded.split("/"))):
        raise Failure("invalid_path")
    return base.rstrip("/") + path


def read_body(args):
    if not args.stdin and args.body_file is None:
        return None
    if args.method == "GET":
        raise Failure("invalid_body")
    if args.stdin:
        data = sys.stdin.buffer.read(BODY_LIMIT + 1)
    else:
        with open(args.body_file, "rb") as source:
            data = source.read(BODY_LIMIT + 1)
    if len(data) > BODY_LIMIT:
        raise Failure("body_too_large")
    try:
        json.loads(data, parse_constant=lambda _: invalid_json())
    except (UnicodeError, ValueError, RecursionError):
        raise Failure("invalid_json")
    return data


def invalid_json():
    raise ValueError()


def catalog():
    path = Path(__file__).resolve().parents[1] / "references/openapi.json"
    if not path.is_file():
        # Repository source tree. The distributed ZIP includes references/.
        path = Path(__file__).resolve().parents[3] / "contract/openapi.json"
    return json.loads(path.read_bytes())


def allowed(value, options):
    return isinstance(value, str) and value in options


def resource(item):
    if not isinstance(item, dict):
        return {}
    result = {}
    for field in ("id", "agent_id", "task_id", "submission_id", "config_id", "user_id", "workspace_id", "instance_id", "request_id"):
        value = item.get(field)
        if isinstance(value, str) and ID.fullmatch(value):
            result[field] = value
    for field in ("version", "revision", "config_version", "deployed_version", "certificate_version", "attempt", "public_port", "backend_port"):
        if type(item.get(field)) is int:
            result[field] = item[field]
    for field, options in (("status", STATES), ("engine", ENGINES), ("target_mode", {"caddy", "nginx"}),
                           ("from", {"caddy", "nginx"}), ("to", {"caddy", "nginx"}),
                           ("transport", {"websocket", "httpupgrade", "http", "xhttp"})):
        if allowed(item.get(field), options):
            result[field] = item[field]
    return result


def route_identity(row):
    result = resource(row)
    if not isinstance(row, dict):
        return result
    if row.get("instance_id") == "":
        result["instance_id"] = ""
    # Both names can be user supplied. Retain correlation without printing them.
    for field in ("path", "tag"):
        if isinstance(row.get(field), str):
            result[field + "_sha256"] = hashlib.sha256(row[field].encode()).hexdigest()
    return result


def preview(value):
    result = resource(value)
    plan = value.get("plan_id")
    if isinstance(plan, str) and re.fullmatch("[0-9a-f]{64}", plan):
        result["plan_id"] = plan
    result["members"] = []
    for member in value.get("members", []):
        safe = route_identity(member)
        result["members"].append(safe)
    result["blockers"] = [v if allowed(v, CODES) else "unknown_blocker" for v in value["blockers"]]
    checks = {"agent_listener_ownership_ipv4_ipv6", "proxy_config_and_service", "original_client_round_trip", "restore_original_paths"}
    result["runtime_checks"] = [v for v in value.get("runtime_checks", []) if allowed(v, checks)]
    if type(value.get("connection_interruption_possible")) is bool:
        result["connection_interruption_possible"] = value["connection_interruption_possible"]
    return result


def migration(value):
    result = resource(value)
    if allowed(value.get("state"), STATES | {"unchanged"}):
        result["state"] = value["state"]
    if isinstance(value.get("plan"), dict):
        result["plan"] = preview(value["plan"])
    receipt = value.get("result")
    if isinstance(receipt, dict):
        safe = {}
        if allowed(receipt.get("step"), {"preflight", "handoff", "verify", "restore", "complete"}):
            safe["step"] = receipt["step"]
        if allowed(receipt.get("code"), CODES):
            safe["code"] = receipt["code"]
        safe["paths"] = []
        for row in receipt.get("paths", []):
            if isinstance(row, dict):
                path = route_identity(row)
                for phase in ("candidate", "recovery"):
                    if allowed(row.get(phase), {"succeeded", "failed", "unknown"}):
                        path[phase] = row[phase]
                safe["paths"].append(path)
        result["result"] = safe
    return result


def project(path, value, contract):
    path = urllib.parse.urlsplit(path).path
    if path == "/openapi.json":
        if value != contract:
            raise Failure("contract_changed", recovery="download_management_skill")
        return contract
    if path == "/api-key" and isinstance(value, dict):
        operations = {method.upper() + " " + route: op for route, methods in contract["paths"].items() for method, op in methods.items() if op.get("x-api-key")}
        perms = {p for op in operations.values() for p in op["x-key-policy"].get("permissions", [])}
        perms.add("secrets.read")
        result = resource(value)
        for field, choices in (("endpoints", operations), ("permissions", perms)):
            result[field] = [v for v in value.get(field, []) if allowed(v, choices)]
        if allowed(value.get("mode"), {"read_only", "read_write"}):
            result["mode"] = value["mode"]
        return result
    if path == "/preferences/ui" and isinstance(value, dict):
        schema = contract["paths"]["/api/v1/preferences/ui"]["patch"]["requestBody"]["content"]["application/json"]["schema"]
        result = resource(value)
        pages = {}
        for page, spec in schema["properties"]["values"]["properties"].items():
            data = value.get("values", {}).get(page, {})
            for field, rule in spec["properties"].items():
                v = data.get(field)
                if type(v) is bool and rule.get("type") == "boolean" or allowed(v, rule.get("enum", [])):
                    pages.setdefault(page, {})[field] = v
        result["values"] = pages
        return result
    if path.endswith("/tls-frontend/preview") and isinstance(value, dict):
        return preview(value)
    if (MIGRATE_PATH.fullmatch(path) or (TASK_PATH.fullmatch(path) and "/tls-migrations/" in path)) and isinstance(value, dict):
        return migration(value)
    if re.fullmatch(r"/agents/agt_[0-9a-f]{16}/tls-ports/[0-9]+", path) and isinstance(value, dict):
        result = resource(value)
        if type(value.get("visible_routes")) is int:
            result["visible_routes"] = value["visible_routes"]
        if allowed(value.get("state"), {"partial", "observed_free", "unconfirmed", "visible_in_use"}):
            result["state"] = value["state"]
        return result
    if path in ("/agents", "/tasks", "/deployments", "/configs") and isinstance(value, list):
        return [resource(v) for v in value]
    if isinstance(value, dict):
        result = resource(value)
        for field in ("config", "task"):
            if isinstance(value.get(field), dict):
                result[field] = resource(value[field])
        return result
    return {}


def retry_seconds(value):
    if value is None:
        return 2
    try:
        if re.fullmatch(r"[0-9]{1,18}", value):
            return int(value)
        when = email.utils.parsedate_to_datetime(value)
        return max(0, math.ceil((when - datetime.datetime.now(datetime.timezone.utc)).total_seconds()))
    except (ValueError, OverflowError, TypeError):
        raise Failure("rate_limited", recovery="check_retry_after")


def fetch(opener, request, deadline):
    # urlopen's socket timeout applies separately to each read and excludes DNS.
    # A daemon thread bounds even a stalled resolver or trickling response.
    replies = queue.Queue(1)
    def run():
        try:
            try:
                response = opener.open(request, timeout=max(0.001, deadline - time.monotonic()))
            except urllib.error.HTTPError as err:
                response = err
            with response:
                size = RESPONSE_LIMIT if response.status < 400 else 65536
                data = response.read(size + 1)
                if len(data) > size:
                    if response.status < 400:
                        raise Failure("response_too_large")
                    data = b""
                try:
                    value = json.loads(data)
                except (ValueError, UnicodeError, RecursionError):
                    value = None
                replies.put((response.status, response.headers, value))
        except Exception as error:
            replies.put(error)
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise Failure("read_timeout")
    threading.Thread(target=run, daemon=True).start()
    try:
        answer = replies.get(timeout=remaining)
    except queue.Empty:
        raise Failure("read_timeout")
    if isinstance(answer, Failure):
        raise answer
    if isinstance(answer, Exception):
        reason = getattr(answer, "reason", answer)
        if isinstance(reason, socket.gaierror):
            raise Failure("dns_failure")
        if isinstance(reason, ssl.SSLCertVerificationError):
            raise Failure("tls_certificate_failure")
        if isinstance(reason, TimeoutError):
            raise Failure("read_timeout")
        raise Failure("connection_failure")
    return answer


def call(base, key, method, path, body, deadline, submission=None):
    url = target_url(base, path)
    headers = {"Authorization": "Bearer " + key, "Accept": "application/json", "Content-Type": "application/json",
               "User-Agent": "QControlHub-Manager/1.1"}
    if submission:
        headers["X-Config-Task-ID"] = submission
    request = urllib.request.Request(url, data=body, method=method, headers=headers)
    opener = urllib.request.build_opener(NoRedirect())
    for attempt in range(3):
        status, headers, value = fetch(opener, request, deadline)
        if 200 <= status < 300:
            return value
        code = {400: "invalid_input", 401: "authentication_failed", 403: "permission_denied", 404: "target_missing",
                409: "conflict", 429: "rate_limited"}.get(status, "server_failure" if status >= 500 else "http_failure")
        detail = {"status": status}
        rid = headers.get("X-Request-ID") or (value.get("request_id") if isinstance(value, dict) else None)
        if isinstance(rid, str) and re.fullmatch(r"req_[0-9a-f]{16}", rid):
            detail["request_id"] = rid
        if isinstance(value, dict):
            for field in ("reason", "code"):
                if allowed(value.get(field), CODES):
                    detail[field] = value[field]
        if status == 429:
            wait = retry_seconds(headers.get("Retry-After"))
            detail["retry_after_seconds"] = wait
            if method == "GET" and attempt < 2 and wait < deadline - time.monotonic():
                time.sleep(wait)
                continue
        raise Failure(code, **detail)


def main():
    parser = SafeParser(description=__doc__, allow_abbrev=False)
    parser.add_argument("method", choices=METHODS)
    parser.add_argument("path")
    sources = parser.add_mutually_exclusive_group()
    sources.add_argument("--stdin", action="store_true")
    sources.add_argument("--body-file")
    parser.add_argument("--wait", action="store_true", help="poll a task until terminal")
    parser.add_argument("--budget", type=float, default=60)
    args = parser.parse_args()
    missing = [name for name in ("QCH_API_BASE", "QCH_API_KEY") if not os.environ.get(name)]
    if missing:
        raise Failure("missing_environment", missing=missing)
    base, key = os.environ["QCH_API_BASE"], os.environ["QCH_API_KEY"]
    if not key.startswith("qch_key_") or any(c.isspace() for c in key):
        raise Failure("invalid_credential")
    if not math.isfinite(args.budget) or not 0 < args.budget <= 86400:
        raise Failure("invalid_budget")
    task = TASK_PATH.fullmatch(urllib.parse.urlsplit(args.path).path)
    if args.wait and not (args.method == "GET" and task):
        raise Failure("invalid_wait_target")
    body = read_body(args)
    spec = catalog()
    deadline = time.monotonic() + args.budget
    submission = None
    if args.method == "POST" and SUBMISSION_PATH.fullmatch(args.path):
        submission = "tsk_" + secrets.token_hex(8)
    lookup = "/tasks/" + submission + "?view=status" if submission else None
    migrate = MIGRATE_PATH.fullmatch(args.path) if args.method == "POST" else None
    if migrate and body:
        data = json.loads(body)
        candidate = data.get("task_id") if isinstance(data, dict) else None
        if isinstance(candidate, str) and re.fullmatch(r"tsk_[0-9a-f]{16}", candidate):
            lookup = migrate[1] + "/tls-migrations/" + candidate
    last = None
    while True:
        try:
            write = args.method != "GET"
            limit = min(deadline, time.monotonic() + args.budget * 0.75) if write else deadline
            value = call(base, key, args.method, args.path, body, limit, submission)
        except Failure as error:
            classification = error.result["classification"]
            if write and classification in ("connection_failure", "read_timeout", "server_failure"):
                error.result["result"] = "unknown"
                if lookup:
                    expected = candidate if migrate else submission
                    error.result["task_id"] = expected
                    try:
                        found = call(base, key, "GET", lookup, None, deadline)
                        if isinstance(found, dict) and expected in (found.get("id"), found.get("submission_id"), found.get("task_id")):
                            return project(lookup, found, spec)
                    except Failure:
                        pass
            if classification == "conflict" and migrate:
                try:
                    selection = {k: data[k] for k in ("expected_instance_id", "target_mode", "expected_version")}
                    workspace = args.path.split("/server-inbounds/")[0]
                    latest = call(base, key, "GET", workspace, None, deadline)
                    selection["expected_version"] = latest["version"]
                    check = call(base, key, "POST", args.path.removesuffix("migrate") + "preview", json.dumps(selection).encode(), deadline)
                    error.result["preview"] = preview(check)
                    error.result["effect_present"] = not check["connection_interruption_possible"] and not check["blockers"]
                except (Failure, KeyError, ValueError, TypeError, AttributeError):
                    pass
            if classification == "conflict" and args.path == "/preferences/ui" and args.method == "PATCH":
                try:
                    latest = call(base, key, "GET", args.path, None, deadline)
                    patch = json.loads(body)["values"]
                    error.result.update(resource(latest))
                    error.result["effect_present"] = all(latest["values"].get(p, {}).get(k) == v for p, fields in patch.items() for k, v in fields.items())
                except (Failure, KeyError, ValueError, TypeError, AttributeError):
                    pass
            if args.wait and classification == "read_timeout" and last:
                raise Failure("pending", **last)
            if args.wait:
                error.result["task_id"] = task[1]
            raise
        result = project(args.path, value, spec)
        if not args.wait:
            return result
        state = result.get("state", result.get("status"))
        if result.get("task_id", result.get("id")) != task[1] or state not in STATES:
            raise Failure("invalid_task_response", task_id=task[1])
        if state == "succeeded":
            return result
        if state not in ("pending", "running", "verifying", "rolling_back"):
            raise Failure("task_failed", **result)
        print(json.dumps(result), file=sys.stderr, flush=True)
        last = result
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise Failure("pending", **last)
        time.sleep(min(remaining, 2))


if __name__ == "__main__":
    try:
        print(json.dumps(main(), ensure_ascii=False))
    except Failure as error:
        print(json.dumps(error.result), file=sys.stderr)
        sys.exit(1)
    except KeyboardInterrupt:
        print('{"classification":"canceled"}', file=sys.stderr)
        sys.exit(130)
    except Exception:
        print('{"classification":"invalid_input_or_response"}', file=sys.stderr)
        sys.exit(2)
