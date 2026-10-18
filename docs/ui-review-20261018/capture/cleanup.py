#!/usr/bin/env python3
"""Remove only the stopped database and scratch directory captured by this run."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('manifest')
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
manifest = json.loads(Path(args.manifest).read_text())
resource = manifest['container']
result = subprocess.run(['docker', 'inspect', resource['id']], capture_output=True, text=True)
if result.returncode == 0:
    item = json.loads(result.stdout)[0]
    assert item['Id'] == resource['id'] and item['Name'] == '/' + resource['name']
    assert item['Config']['Labels'].get('qch.task') == 'ui-review-20261018'
    assert not item['State']['Running'], 'Task database is running; retain it and retry later.'
    mounts = [{key: mount.get(key) for key in ['Type', 'Name', 'Destination']} for mount in item['Mounts']]
    assert mounts == resource['mounts'], 'Container mounts changed; retain resources.'
    if not args.check:
        subprocess.run(['docker', 'rm', '--volumes', resource['id']], check=True)
elif 'No such object' not in result.stderr:
    raise RuntimeError(result.stderr)

for scratch in manifest['directories']:
    path = Path(scratch['path'])
    assert str(path) in {
        '/root/.codex/worktrees/7d05/qcontrolhub/output/playwright/ui-review-20261018',
        '/root/.codex/worktrees/7d05/qcontrolhub/.playwright-cli',
    }
    if path.exists() or path.is_symlink():
        info = os.lstat(path)
        assert not path.is_symlink() and info.st_dev == scratch['device'] and info.st_ino == scratch['inode']
        if not args.check:
            shutil.rmtree(path)
print('Scope verified.' if args.check else 'Task database, its anonymous volume, and scratch directory cleaned. Evidence retained.')
