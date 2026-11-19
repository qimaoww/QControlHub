"""Drive real skill clients against a deterministic local tool-calling model."""

import http.server
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import sys
import tempfile
import threading


class LocalModel(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        size = int(self.headers.get("content-length", 0))
        body = json.loads(self.rfile.read(size))
        host = self.server
        host.calls += 1
        input_text = json.dumps(body.get("messages", [])[-1:])
        if (
            "missing_environment" in input_text
            and "QCH_API_KEY" in input_text
            and "tool_result" in input_text
        ):
            host.missing_environment = True
        tools = {t.get("name"): t for t in body.get("tools", [])}
        codex = self.path.endswith("responses")
        # Capture only schema names for troubleshooting; discard model context.
        host.names.update(str(k) for k in tools)
        name = "exec_command" if codex else "bash"
        args = {"cmd" if codex else "command": host.command}
        if not codex:
            args["description"] = "Read effective panel authorization"
        invoked = {
            part.get("name")
            for msg in body.get("messages", [])
            for part in msg.get("content", [])
            if isinstance(part, dict) and part.get("type") == "tool_use"
        }
        if not codex and "skill" not in invoked:
            name, args = "skill", {"name": "qcontrolhub-manager"}
        if not codex and host.mcp and "skill" in invoked:
            name, args = "mcp__qch__authorization", {}
        final = (
            host.calls > 1
            if codex
            else ("mcp__qch__authorization" if host.mcp else "bash") in invoked
        ) or host.calls > 8
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()

        def event(kind, **kwargs):
            data = json.dumps(dict(type=kind, **kwargs))
            self.wfile.write(f"event: {kind}\ndata: {data}\n\n".encode())

        if codex:
            rid = f"resp_{host.calls}"
            meta = {
                "id": rid,
                "object": "response",
                "created_at": 1,
                "status": "in_progress",
                "model": "gpt-5",
                "output": [],
            }
            event("response.created", response=meta)
            item = {
                "id": "fc_1",
                "type": "function_call",
                "name": name,
                "call_id": "call_1",
                "arguments": json.dumps(args),
            }
            if final:
                item = {
                    "id": "msg_2",
                    "type": "message",
                    "role": "assistant",
                    "content": [{"type": "output_text", "text": "completed"}],
                    "status": "completed",
                }
            event("response.output_item.added", output_index=0, item=item)
            event("response.output_item.done", output_index=0, item=item)
            event(
                "response.completed",
                response={
                    **meta,
                    "status": "completed",
                    "output": [item],
                    "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
                },
            )
        else:
            event(
                "message_start",
                message={
                    "type": "message",
                    "id": "msg_1",
                    "role": "assistant",
                    "model": body.get("model", "deepseek-v4-flash"),
                    "content": [],
                    "usage": {"input_tokens": 1, "output_tokens": 0},
                },
            )
            block = (
                {"type": "text", "text": ""}
                if final
                else {
                    "type": "tool_use",
                    "id": f"tool_{host.calls}",
                    "name": name,
                    "input": {},
                }
            )
            event("content_block_start", index=0, content_block=block)
            delta = (
                {"type": "text_delta", "text": "completed"}
                if final
                else {"type": "input_json_delta", "partial_json": json.dumps(args)}
            )
            event("content_block_delta", index=0, delta=delta)
            event("content_block_stop", index=0)
            event(
                "message_delta",
                delta={
                    "stop_reason": "end_turn" if final else "tool_use",
                    "stop_sequence": None,
                },
                usage={"output_tokens": 1},
            )
            event("message_stop")


def run(client, bundle, root):
    project = root / "project"
    project.mkdir()
    (project / ".git").mkdir()
    install = (
        (root / ".agents" if client == "codex" else project / ".dsh")
        / "skills"
        / bundle.name
    )
    if client == "dsh-mcp":
        subprocess.run(
            [
                sys.executable,
                str(bundle / "scripts/install.py"),
                "dsh",
                "--project",
                str(project),
            ],
            capture_output=True,
            check=True,
        )
    else:
        shutil.copytree(bundle, install)
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), LocalModel)
    server.calls, server.names = 0, set()
    server.missing_environment = False
    server.mcp = client == "dsh-mcp"
    server.command = (
        "python3 " + shlex.quote(str(install / "scripts/qch.py")) + " GET /api-key"
    )
    base = f"http://127.0.0.1:{server.server_port}"
    env = {k: os.environ[k] for k in ("PATH", "QCH_API_BASE", "QCH_API_KEY")}
    env.update(
        HOME=str(root),
        CODEX_HOME=str(root / ".codex"),
        DSH_HOME=str(root / ".dsh"),
        DEEPSEEK_BASE_URL=base,
        DEEPSEEK_API_KEY="local-model-only",
    )
    (root / ".codex").mkdir()
    prompt = "Use $qcontrolhub-manager and perform only GET /api-key on the configured panel."
    if client == "codex":
        args = [
            "codex",
            "exec",
            "--skip-git-repo-check",
            "--ephemeral",
            "-s",
            "danger-full-access",
            "-c",
            'model_provider="fixture"',
            "-c",
            'model="gpt-5"',
            "-c",
            'model_providers.fixture.name="fixture"',
            "-c",
            f'model_providers.fixture.base_url="{base}/v1"',
            "-c",
            'model_providers.fixture.wire_api="responses"',
            "-c",
            'shell_environment_policy.inherit="all"',
            prompt,
        ]
    else:
        args = ["dsh", "headless"]
        if server.mcp:
            args.extend(["--patch", str(install / "references/dsh-readonly.patch.yml")])
        args.extend(["--json", "--approval-policy", "never", prompt])
    t = threading.Thread(target=server.serve_forever, daemon=True)
    t.start()
    try:
        completed = subprocess.run(
            args, env=env, cwd=project, capture_output=True, timeout=42
        )
        if client == "dsh" and server.missing_environment:
            return 77
        return 0 if completed.returncode == 0 and server.calls >= 2 else 1
    except subprocess.TimeoutExpired:
        return 1
    finally:
        server.shutdown()
        server.server_close()
        t.join()


if __name__ == "__main__":
    with tempfile.TemporaryDirectory() as root:
        sys.exit(run(sys.argv[2], Path(sys.argv[1]).resolve(), Path(root)))
