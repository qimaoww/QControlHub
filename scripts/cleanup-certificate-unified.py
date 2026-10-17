#!/usr/bin/env python3
"""Remove only this task's retained fixtures after the recorded 72-hour hold.

Install a root-owned copy and manifest under ROOT, and invoke it from the
persistent systemd timer described in docs/certificate-unified/README.md.
--check validates identities and scope without stopping/removing resources.
"""
import datetime
import json
import pathlib
import re
import shutil
import subprocess
import sys

TASK = "certificate-unified-e528"
ROOT = pathlib.Path("/var/lib/qch-certificate-unified-e528")
CHECKOUT = pathlib.Path("/root/.codex/worktrees/e528/qcontrolhub")
ALLOWED_PATHS = {
    CHECKOUT / ".preview" / TASK,
    CHECKOUT / "output/playwright/certificate-unified",
}


def command(*args, check=True):
    return subprocess.run(args, check=check, text=True, capture_output=True)


def inspect_resource(kind, identity):
    if not re.fullmatch(r"[0-9a-f]{64}", identity):
        raise RuntimeError("unexpected resource identity")
    args = ["docker"] + (["network"] if kind == "network" else [])
    result = command(*args, "inspect", identity, check=False)
    if result.returncode:
        if "No such" not in result.stderr:
            raise RuntimeError("Docker unavailable; cleanup must be retried")
        return False
    resource = json.loads(result.stdout)[0]
    labels = resource.get("Labels", {}) if kind == "network" else resource["Config"].get("Labels", {})
    if resource["Id"] != identity or labels.get("qch.task") != TASK:
        raise RuntimeError("resource task label mismatch")
    return True


def main():
    if sys.argv[1:] not in ([], ["--check"]):
        raise RuntimeError("unsupported cleanup arguments")
    check_only = bool(sys.argv[1:])
    manifest_path = ROOT / "manifest.json"
    for path in [ROOT, manifest_path]:
        info = path.lstat()
        if info.st_uid != 0 or info.st_mode & 0o022 or path.is_symlink():
            raise RuntimeError("unsafe cleanup manifest directory/file")
    manifest = json.loads(manifest_path.read_text())
    if manifest["task"] != TASK:
        raise RuntimeError("unexpected task identity")
    execute_at = datetime.datetime.fromisoformat(manifest["execute_utc"])
    finished = datetime.datetime.fromisoformat(manifest["test_finished_utc"])
    if execute_at - finished != datetime.timedelta(hours=72) or execute_at.tzinfo is None:
        raise RuntimeError("cleanup must retain fixtures for exactly 72 hours")
    for value in manifest["paths"]:
        path = pathlib.Path(value)
        if path not in ALLOWED_PATHS or path.is_symlink() or path.resolve() != path:
            raise RuntimeError("unexpected cleanup path")
    containers = [identity for identity in manifest["containers"] if inspect_resource("container", identity)]
    networks = [identity for identity in manifest["networks"] if inspect_resource("network", identity)]
    if check_only:
        print("Validated task labels, immutable identities, exact path allowlist and 72-hour hold")
        return
    if datetime.datetime.now(datetime.timezone.utc) < execute_at:
        raise RuntimeError("retention period has not elapsed")
    for identity in containers:
        command("docker", "rm", "--force", "--volumes", identity)
        print("Removed task container " + identity, flush=True)
    for identity in networks:
        command("docker", "network", "rm", identity)
        print("Removed task network " + identity, flush=True)
    for value in manifest["paths"]:
        path = pathlib.Path(value)
        if path.exists():
            shutil.rmtree(path)
        print("Removed task path " + str(path), flush=True)
    (ROOT / "completed.json").write_text(json.dumps({"task": TASK, "scheduled_utc": manifest["execute_utc"], "status": "complete"}, indent=2) + "\n")
    command("systemctl", "disable", "qch-cert-e528-cleanup.timer")


if __name__ == "__main__":
    main()
