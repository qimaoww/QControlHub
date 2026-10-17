#!/usr/bin/env python3
"""Persistent cleanup for this task's isolated certificate evidence environment.

The root-owned manifest lives outside the checkout so cleanup survives branch
changes and reboots. Container identities and the task label must both match.
Reports, committed screenshots, existing images and other workloads are retained.
"""
import json
import pathlib
import re
import shutil
import subprocess

TASK = "certificate-center-a978"
ROOT = pathlib.Path("/var/lib/qch-certificate-a978")
CHECKOUT = pathlib.Path("/root/.codex/worktrees/a978/qcontrolhub")
ALLOWED_PATHS = {
    CHECKOUT / ".preview" / TASK,
    CHECKOUT / "output/playwright/certificate-center",
    pathlib.Path("/tmp/qch-cert-tools"),
}


def command(*args, check=True):
    return subprocess.run(args, check=check, text=True, capture_output=True)


def main():
    manifest_path = ROOT / "manifest.json"
    info = manifest_path.lstat()
    if info.st_uid != 0 or info.st_mode & 0o022 or manifest_path.is_symlink():
        raise RuntimeError("unsafe cleanup manifest")
    manifest = json.loads(manifest_path.read_text())
    if manifest["task"] != TASK:
        raise RuntimeError("unexpected task identity")
    for unit in manifest["units"]:
        if not re.fullmatch(r"qch-cert-a978-[a-z]+\.service", unit):
            raise RuntimeError("unexpected service")
        command("systemctl", "stop", unit, check=False)
    for identity in manifest["containers"]:
        if not re.fullmatch(r"[0-9a-f]{64}", identity):
            raise RuntimeError("unexpected container identity")
        inspected = command("docker", "inspect", identity, check=False)
        if inspected.returncode:
            # A missing container is already clean. A failed daemon is retried.
            if "No such object" not in inspected.stderr:
                raise RuntimeError(inspected.stderr)
            continue
        container = json.loads(inspected.stdout)[0]
        if container["Id"] != identity or container["Config"].get("Labels", {}).get("qch.task") != TASK:
            raise RuntimeError("container task label mismatch")
        command("docker", "rm", "--force", "--volumes", identity)
        print("Removed task container " + identity, flush=True)
    for value in manifest["paths"]:
        path = pathlib.Path(value)
        if path not in ALLOWED_PATHS or path.is_symlink():
            raise RuntimeError("unexpected cleanup path")
        if path.exists():
            shutil.rmtree(path)
        print("Removed task path " + str(path), flush=True)
    (ROOT / "completed.json").write_text(json.dumps({"task": TASK, "scheduled_utc": manifest["execute_utc"], "status": "complete"}, indent=2) + "\n")
    command("systemctl", "disable", "qch-cert-a978-cleanup.timer")


if __name__ == "__main__":
    main()
