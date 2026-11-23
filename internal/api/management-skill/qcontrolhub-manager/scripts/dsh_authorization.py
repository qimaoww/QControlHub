#!/usr/bin/env python3
"""DSH stdio tool for checking authorization on the configured panel."""

import json
from pathlib import Path
import subprocess
import sys


def respond(method, params):
    if method == "initialize":
        return {
            "protocolVersion": "2025-03-26",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "qcontrolhub-manager", "version": "1"},
        }
    if method == "ping":
        return {}
    if method == "tools/list":
        return {
            "tools": [
                {
                    "name": "authorization",
                    "description": "Read effective QControlHub API grants",
                    "inputSchema": {
                        "type": "object",
                        "properties": {},
                        "additionalProperties": False,
                    },
                }
            ]
        }
    if (
        method == "tools/call"
        and params.get("name") == "authorization"
        and not params.get("arguments")
    ):
        result = subprocess.run(
            [
                sys.executable,
                str(Path(__file__).with_name("qch.py")),
                "GET",
                "/api-key",
            ],
            capture_output=True,
            timeout=65,
        )
        projection = result.stdout if result.returncode == 0 else result.stderr
        return {
            "isError": result.returncode != 0,
            "content": [{"type": "text", "text": projection.decode()}],
        }
    raise KeyError()


def main():
    for line in sys.stdin.buffer:
        request_id = None
        try:
            request = json.loads(line)
            if "id" not in request:
                continue
            request_id = request["id"]
            if type(request_id) is not int and not (
                isinstance(request_id, str) and len(request_id) <= 256
            ):
                request_id = None
                raise ValueError()
            reply = {"result": respond(request["method"], request.get("params", {}))}
        except KeyError:
            reply = {"error": {"code": -32601, "message": "Method not found"}}
        except Exception:
            reply = {
                "error": {
                    "code": -32600,
                    "message": "Invalid authorization tool request",
                }
            }
        print(json.dumps({"jsonrpc": "2.0", "id": request_id, **reply}), flush=True)


if __name__ == "__main__":
    main()
