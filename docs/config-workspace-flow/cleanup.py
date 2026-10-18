#!/usr/bin/python3
"""One-task cleanup; deployed with its manifest outside the checkout."""
import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import shutil
import subprocess


TASK = "config-workspace-flow-7850"
WORKTREE = Path("/root/.codex/worktrees/7850/qcontrolhub")
ALLOWED = {
    WORKTREE / "output/playwright/config-workspace-flow",
    WORKTREE / "bin/qcontrol-plane",
    WORKTREE / "bin/qagent",
}


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def identity(path, expected):
    if not path.exists() and not path.is_symlink():
        return False
    stat = path.lstat()
    if path.is_symlink() or (stat.st_dev, stat.st_ino) != (expected["device"], expected["inode"]):
        raise RuntimeError(f"Resource identity changed: {path}")
    if expected["kind"] == "file":
        if not path.is_file() or hashlib.sha256(path.read_bytes()).hexdigest() != expected["sha256"]:
            raise RuntimeError(f"Build output changed: {path}")
    elif not path.is_dir():
        raise RuntimeError(f"Expected directory: {path}")
    return True


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--dry-run", action="store_true")
    dry = parser.parse_args().dry_run
    manifest = json.loads(Path(__file__).with_name("manifest.json").read_text())
    if manifest["task"] != TASK:
        raise RuntimeError("Unexpected task manifest")
    due = datetime.fromisoformat(manifest["due_utc"])
    if not dry and datetime.now(timezone.utc) < due:
        raise RuntimeError("Retention period has not elapsed")

    paths = []
    for resource in manifest["paths"]:
        path = Path(resource["path"])
        if path not in ALLOWED or path.resolve() != path:
            raise RuntimeError(f"Unexpected cleanup path: {path}")
        if identity(path, resource):
            paths.append((path, resource["kind"]))

    expected = manifest["container"]
    container = expected["id"]
    containers = command("docker", "container", "ls", "-aq", "--no-trunc", "--filter", f"id={container}").splitlines()
    if containers:
        if containers != [container]:
            raise RuntimeError("Container ID is not exact")
        actual = json.loads(command("docker", "inspect", container))[0]
        if (actual["Name"] != "/qch-config-flow-7850-db" or
                actual["Config"]["Labels"].get("qch.task") != TASK or
                actual["State"]["Running"] or actual["Mounts"] != expected["mounts"]):
            raise RuntimeError("Container identity, mounts or stopped state changed")

    volume = manifest["volume"]
    volume_names = command("docker", "volume", "ls", "-q", "--filter", f"name={volume['Name']}").splitlines()
    if volume["Name"] in volume_names:
        actual = json.loads(command("docker", "volume", "inspect", volume["Name"]))[0]
        for field in ["Name", "CreatedAt", "Mountpoint", "Driver"]:
            if actual[field] != volume[field]:
                raise RuntimeError(f"Volume identity changed: {field}")
        users = command("docker", "container", "ls", "-aq", "--no-trunc", "--filter", f"volume={volume['Name']}").splitlines()
        if any(user != container for user in users):
            raise RuntimeError("Temporary volume is used by another container")

    print(json.dumps({"dry_run": dry, "due_utc": manifest["due_utc"], "paths": [str(path) for path, _ in paths],
                      "container": container, "volume": volume["Name"]}), flush=True)
    if dry:
        return
    if containers:
        command("docker", "rm", container)
    if volume["Name"] in volume_names:
        command("docker", "volume", "rm", volume["Name"])
    for path, kind in paths:
        if kind == "directory":
            shutil.rmtree(path)
        else:
            path.unlink()
    build_dir = WORKTREE / "bin"
    if identity(build_dir, manifest["build_directory"]) and not any(build_dir.iterdir()):
        build_dir.rmdir()
    print("Cleanup completed; committed evidence and shared caches retained.", flush=True)


if __name__ == "__main__":
    main()
