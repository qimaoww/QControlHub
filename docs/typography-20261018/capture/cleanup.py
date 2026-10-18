#!/usr/bin/env python3
"""Remove this task's inactive scratch directories and preserve review evidence."""
import argparse
import datetime as dt
import json
from pathlib import Path
import shutil
import stat

parser = argparse.ArgumentParser()
parser.add_argument('manifest', type=Path)
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
manifest = json.loads(args.manifest.read_text())
now = dt.datetime.now(dt.timezone.utc)
if not args.check and now < dt.datetime.fromisoformat(manifest['execute_after_utc']):
    raise SystemExit('Refusing early cleanup')
for name in manifest['browser_sessions']:
    for proc in Path('/proc').glob('[0-9]*/cmdline'):
        try:
            argv = proc.read_bytes().split(b'\0')
        except (PermissionError, FileNotFoundError, ProcessLookupError):
            continue
        if any(value.endswith(b'/cliDaemon.js') and index + 1 < len(argv) and
               argv[index + 1] == name.encode() for index, value in enumerate(argv)):
            raise SystemExit('Browser session is still running: ' + name)
root = Path(manifest['workspace'])
expected = {root / 'output/playwright/typography-20261018'}
if {Path(item['path']) for item in manifest['directories']} != expected:
    raise SystemExit('Unexpected cleanup scope')
for item in manifest['directories']:
    path = Path(item['path'])
    if not path.exists() and not path.is_symlink():
        continue
    info = path.lstat()
    if (not stat.S_ISDIR(info.st_mode) or path.resolve() != path or
            (info.st_dev, info.st_ino) != (item['device'], item['inode'])):
        raise SystemExit('Directory identity changed; keeping: ' + str(path))
cli_directory = root / '.playwright-cli'
for item in manifest['ephemeral_files']:
    path = Path(item['path'])
    if path.parent != cli_directory:
        raise SystemExit('Unexpected ephemeral file scope')
    if not path.exists() and not path.is_symlink():
        continue
    info = path.lstat()
    if (not stat.S_ISREG(info.st_mode) or path.resolve() != path or
            (info.st_dev, info.st_ino) != (item['device'], item['inode'])):
        raise SystemExit('File identity changed; keeping: ' + str(path))
if args.check:
    print(json.dumps({'status': 'scope-verified', 'manifest': manifest}, ensure_ascii=False, indent=2))
else:
    removed = []
    for item in manifest['directories']:
        path = Path(item['path'])
        if path.exists():
            shutil.rmtree(path)
            removed.append(str(path))
    for item in manifest['ephemeral_files']:
        path = Path(item['path'])
        if path.exists():
            path.unlink()
            removed.append(str(path))
    # New files from a later browser session are never part of this manifest.
    try:
        cli_directory.rmdir()
    except (FileNotFoundError, OSError):
        pass
    result = {'status': 'complete', 'executed_at_utc': now.isoformat(), 'removed': removed,
              'preserved': manifest['preserved_evidence']}
    Path(manifest['execution_log']).write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(result, ensure_ascii=False))
