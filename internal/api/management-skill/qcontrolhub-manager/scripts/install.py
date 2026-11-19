#!/usr/bin/env python3
"""Install one extracted Skill release in a client's project directory."""

import argparse
import json
import os
from pathlib import Path
import shutil
import tempfile


ROOTS = {
    "codex": ".agents/skills",
    "claude": ".claude/skills",
    "gemini": ".gemini/skills",
    "copilot": ".github/skills",
    "opencode": ".opencode/skills",
    "cursor": ".cursor/skills",
    "pi": ".agents/skills",
    "dsh": ".dsh/skills",
    "hermes": ".hermes/skills",
    "openclaw": "skills",
}


def install(client, project):
    source = Path(__file__).resolve().parents[1]
    if not (source / "references/openapi.json").is_file():
        raise SystemExit("Extract the complete management Skill ZIP first.")
    project = project.resolve(strict=True)
    if client in ("dsh", "hermes"):
        git_root = next(
            (p for p in (project, *project.parents) if (p / ".git").exists()), None
        )
        if client == "hermes" and git_root is None:
            raise SystemExit("Select a Hermes project checkout containing .git.")
        project = git_root or project
    parent = project / ROOTS[client]
    parent.mkdir(parents=True, exist_ok=True)
    destination = parent / "qcontrolhub-manager"
    if destination.resolve().is_relative_to(source):
        raise SystemExit("Extract the ZIP outside the target Skill directory.")
    if destination.exists() or destination.is_symlink():
        raise SystemExit(
            "The installation already exists. Select an empty installation directory."
        )
    with tempfile.TemporaryDirectory(dir=parent) as stage:
        copied = Path(stage) / destination.name
        shutil.copytree(
            source, copied, ignore=shutil.ignore_patterns("__pycache__", "*.pyc")
        )
        if client == "dsh":
            (copied / "references/dsh-readonly.patch.yml").write_text(
                "- insert:\n    - id: qch-authorization\n      name: '@deepseek-ai/dsh-mcp-client'\n"
                "      config:\n        transport: stdio\n        serverName: qch\n        command: python3\n"
                "        args: "
                + json.dumps([str(destination / "scripts/dsh_authorization.py")])
                + "\n        env:\n          QCH_API_BASE: !!js process.env.QCH_API_BASE\n"
                "          QCH_API_KEY: !!js process.env.QCH_API_KEY\n"
            )
        os.rename(copied, destination)
    return destination


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("client", choices=sorted(ROOTS))
    parser.add_argument("--project", type=Path, required=True)
    args = parser.parse_args()
    print(
        json.dumps(
            {
                "state": "files_installed",
                "path": str(install(args.client, args.project)),
            }
        )
    )
