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
        ["codex", "app-server", "--listen", "stdio://"], cwd=project,
        env={"PATH": os.environ["PATH"], "HOME": str(root), "CODEX_HOME": str(root / ".codex")},
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True,
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
        message("initialize", {"clientInfo": {"name": "release-test", "version": "1"}}, 1)
        message("initialized", {})
        result = message("skills/list", {"cwds": [str(project)], "forceReload": True}, 2)
        return [Path(s["path"]) for entry in result["data"] for s in entry["skills"] if s["name"] == "qcontrolhub-manager"]
    finally:
        proc.terminate()
        proc.wait(timeout=5)


def dsh_catalog(root, project):
    launcher = Path(shutil.which("dsh")).resolve()
    provider = launcher.parents[1] / "node_modules/@deepseek-ai/dsh-skill-filesystem/lib/index.js"
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
    result = subprocess.run(["node", "--input-type=module", "-e", program, str(provider), str(project), str(root / "empty")],
                            capture_output=True, timeout=15, check=True)
    return [Path(line) for line in result.stdout.decode().splitlines()]


def main(source):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        project = root / "parent/child"
        project.mkdir(parents=True)
        (project.parent / ".git").mkdir()
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
        print("DSH filesystem provider loaded the release from the parent project root.", flush=True)


if __name__ == "__main__":
    main(Path(sys.argv[1]).resolve())
