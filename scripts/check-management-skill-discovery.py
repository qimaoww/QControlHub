#!/usr/bin/env python3
"""Check discovery of an extracted release in disposable client directories."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


def codex_catalog(root, project):
    proc = subprocess.Popen(
        ["codex", "app-server", "--listen", "stdio://"],
        cwd=project,
        env={
            "PATH": os.environ["PATH"],
            "HOME": str(root),
            "CODEX_HOME": str(root / ".codex"),
        },
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
    )

    def message(method, params, request_id=None):
        req = {"method": method, "params": params}
        if request_id is not None:
            req["id"] = request_id
        proc.stdin.write(json.dumps(req) + "\n")
        proc.stdin.flush()
        if request_id is not None:
            for line in proc.stdout:
                data = json.loads(line)
                if data.get("id") == request_id:
                    return data["result"]
            raise RuntimeError("Codex catalog unavailable")

    try:
        message(
            "initialize", {"clientInfo": {"name": "release-test", "version": "1"}}, 1
        )
        message("initialized", {})
        result = message(
            "skills/list", {"cwds": [str(project)], "forceReload": True}, 2
        )
        return [
            Path(s["path"])
            for entry in result["data"]
            for s in entry["skills"]
            if s["name"] == "qcontrolhub-manager"
        ]
    finally:
        proc.terminate()
        proc.wait(timeout=5)


def dsh_catalog(root, project):
    launcher = Path(shutil.which("dsh")).resolve()
    provider = (
        launcher.parents[1]
        / "node_modules/@deepseek-ai/dsh-skill-filesystem/lib/index.js"
    )
    program = """
import {pathToFileURL} from 'node:url';
const [modulePath, cwd, isolate] = process.argv.slice(1);
const {FileSystemSkillProvider} = await import(pathToFileURL(modulePath));
const host = {get:()=>undefined};
const control = {signal:new AbortController().signal,invalidate:()=>{}};
const p = new FileSystemSkillProvider(host,control,{dshHome:isolate,agentsHome:isolate});
try {
 const listing = await p.list({cwd});
 const rows = Array.isArray(listing)?listing:listing.candidates;
 for(const item of rows.filter(x=>x.name==='qcontrolhub-manager')){
   const skill=await p.get(item,{cwd});
   if(skill?.name==='qcontrolhub-manager') process.stdout.write(skill.path+'\\n');
 }
} finally { await p.dispose(); }
"""
    result = subprocess.run(
        [
            "node",
            "--input-type=module",
            "-e",
            program,
            str(provider),
            str(project),
            str(root / "empty"),
        ],
        capture_output=True,
        timeout=15,
        check=True,
    )
    return [Path(line) for line in result.stdout.decode().splitlines()]


def main(source):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        project = root / "parent/child"
        project.mkdir(parents=True)
        (project.parent / ".git").mkdir()
        installer = str(source / "scripts/install.py")
        for client, directory_name in (
            ("claude", ".claude"),
            ("gemini", ".gemini"),
            ("copilot", ".github"),
            ("opencode", ".opencode"),
            ("cursor", ".cursor"),
            ("pi", ".agents"),
            ("codex", ".agents"),
            ("dsh", ".dsh"),
            ("hermes", ".hermes"),
            ("openclaw", ""),
        ):
            workspace = root / ("install-" + client)
            workspace.mkdir()
            (workspace / ".git").mkdir()
            result = subprocess.run(
                [sys.executable, installer, client, "--project", str(workspace)],
                capture_output=True,
                check=True,
            )
            target = workspace / directory_name / "skills" / source.name
            if (
                json.loads(result.stdout)["state"] != "files_installed"
                or (target / "references/openapi.json").read_bytes()
                != (source / "references/openapi.json").read_bytes()
            ):
                raise RuntimeError("incomplete project install")
            env = {
                "PATH": os.environ["PATH"],
                "HOME": str(root / "empty-home"),
                "CLAUDE_CONFIG_DIR": str(root / "claude-test"),
                "PI_CODING_AGENT_DIR": str(root / "pi-test"),
            }
            if client == "claude" and shutil.which("claude"):
                run = subprocess.run(
                    [
                        "claude",
                        "-p",
                        "--verbose",
                        "--output-format",
                        "stream-json",
                        "--input-format",
                        "stream-json",
                        "--no-session-persistence",
                        "--setting-sources",
                        "project",
                    ],
                    cwd=workspace,
                    env={
                        **env,
                        "ANTHROPIC_BASE_URL": "http://127.0.0.1:1",
                        "ANTHROPIC_API_KEY": "offline-catalog-only",
                    },
                    input=json.dumps(
                        {
                            "type": "control_request",
                            "request_id": "catalog",
                            "request": {"subtype": "initialize"},
                        }
                    )
                    + "\n",
                    capture_output=True,
                    text=True,
                    timeout=20,
                )
                if "qcontrolhub-manager" not in run.stdout:
                    raise RuntimeError("Claude Code catalog missing release")
                print(
                    "Claude Code SDK session catalog discovered the project Skill.",
                    flush=True,
                )
            if client == "pi" and shutil.which("pi"):
                run = subprocess.run(
                    ["pi", "--mode", "rpc", "--no-session", "--approve"],
                    cwd=workspace,
                    env=env,
                    input='{"id":"catalog","type":"get_commands"}\n',
                    text=True,
                    capture_output=True,
                    timeout=15,
                )
                if "skill:qcontrolhub-manager" not in run.stdout:
                    raise RuntimeError("Pi command catalog missing release")
                print(
                    "Pi RPC session catalog discovered the project Skill.", flush=True
                )
            if client == "hermes" and shutil.which("hermes"):
                env["HERMES_HOME"] = str(root / "hermes-test")
                subprocess.run(
                    ["hermes", "skills", "trust", str(workspace)],
                    cwd=workspace,
                    env=env,
                    capture_output=True,
                    check=True,
                    timeout=30,
                )
                run = subprocess.run(
                    ["hermes", "skills", "list"],
                    cwd=workspace,
                    env=env,
                    capture_output=True,
                    text=True,
                    check=True,
                    timeout=45,
                )
                if source.name not in run.stdout:
                    raise RuntimeError("Hermes project catalog missing release")
                print("Hermes discovered the trusted checkout's release.", flush=True)
            if client == "openclaw" and shutil.which("openclaw"):
                config = root / "openclaw-test.json"
                config.write_text(
                    json.dumps({"agents": {"defaults": {"workspace": str(workspace)}}})
                )
                env.update(
                    OPENCLAW_CONFIG_PATH=str(config),
                    OPENCLAW_STATE_DIR=str(root / "openclaw-test"),
                )
                run = subprocess.run(
                    ["openclaw", "skills", "info", source.name, "--json"],
                    cwd=workspace,
                    env=env,
                    capture_output=True,
                    text=True,
                    check=True,
                    timeout=45,
                )
                if json.loads(run.stdout).get("name") != source.name:
                    raise RuntimeError("OpenClaw workspace catalog missing release")
                print("OpenClaw resolved the workspace Skill by name.", flush=True)
        dest = root / ".agents/skills" / source.name
        shutil.copytree(source, dest)
        (root / ".codex").mkdir()
        if dest / "SKILL.md" not in codex_catalog(root, project):
            raise RuntimeError("Codex user install missing from catalog")
        print("Codex app-server discovered the user installation.", flush=True)
        nested = project / ".dsh/skills" / source.name
        shutil.copytree(source, nested)
        if dsh_catalog(root, project):
            raise RuntimeError("unexpected nested DSH catalog entry")
        dest = project.parent / ".dsh/skills" / source.name
        shutil.copytree(source, dest)
        if dsh_catalog(root, project) != [dest / "SKILL.md"]:
            raise RuntimeError("DSH project installation missing from catalog")
        print(
            "DSH filesystem provider loaded the release from the parent project root.",
            flush=True,
        )


if __name__ == "__main__":
    main(Path(sys.argv[1]).resolve())
