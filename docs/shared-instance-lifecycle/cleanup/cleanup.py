#!/usr/bin/env python3
"""Remove only the recorded, labeled shared-instance acceptance fixture."""
import argparse
import datetime as dt
import json
from pathlib import Path
import shutil
import subprocess


def command(arguments, check=True):
    return subprocess.run(arguments, check=check, capture_output=True, text=True)


def cleanup(manifest, execute=False):
    scope = 'shared-instance-4b5d'
    if manifest['scope'] != scope:
        raise ValueError('unexpected fixture scope')
    workspace = Path(manifest['workspace'])
    if not workspace.is_absolute():
        raise ValueError('an absolute recorded workspace is required')
    # Container cleanup must still work if the worktree was removed meanwhile.
    workspace = workspace.resolve()
    task = Path(manifest['task_directory'])
    expected = workspace / '.preview' / scope
    artifacts = workspace / 'docs' / 'shared-instance-lifecycle'
    if task != expected or task.is_symlink() or task.resolve() != expected:
        raise ValueError('refusing an unexpected temporary directory')
    if artifacts == task or task in artifacts.parents:
        raise ValueError('artifacts must remain outside cleanup scope')
    due = dt.datetime.fromisoformat(manifest['execute_at_utc'])
    if execute and dt.datetime.now(dt.timezone.utc) < due:
        raise ValueError('72-hour retention has not elapsed')
    result = {'scope': scope, 'dry_run': not execute, 'observed_at_utc': dt.datetime.now(dt.timezone.utc).isoformat(), 'actions': []}
    for unit in manifest['host_units']:
        if unit not in ['qch-shared-4b5d-control.service', 'qch-shared-4b5d-web.service', 'qch-shared-4b5d-probe.service']:
            raise ValueError('unexpected service name')
        if execute:
            command(['systemctl', 'stop', unit], check=False)
        result['actions'].append({'unit': unit, 'action': 'stop'})
    for item in manifest['containers']:
        container_id = item['id']
        if len(container_id) != 64 or any(c not in '0123456789abcdef' for c in container_id):
            raise ValueError('a complete captured container identity is required')
        probe = command(['docker', 'inspect', container_id], check=False)
        if probe.returncode:
            if 'No such object' not in probe.stderr and 'No such container' not in probe.stderr:
                raise RuntimeError('container inspection unavailable; retry cleanup when Docker is available')
            result['actions'].append({'container': item['name'], 'action': 'already absent'})
            continue
        container = json.loads(probe.stdout)[0]
        if container['Id'] != container_id or container['Name'].lstrip('/') != item['name'] or container['Config']['Labels'].get('qch.test.scope') != scope:
            raise ValueError('container identity or fixture label changed')
        if execute:
            command(['docker', 'rm', '--force', '--volumes', container_id])
        result['actions'].append({'container': item['name'], 'id': container_id, 'action': 'remove container and its anonymous volumes'})
    for raw in manifest.get('browser_profiles', []):
        profile = Path(raw)
        # Explicitly recorded profile paths may only refer to this fixture's
        # sessions; never remove shared Playwright caches or browser binaries.
        if profile.is_symlink() or not profile.name.startswith('shared-') or profile.name not in manifest.get('browser_sessions', []):
            raise ValueError('unexpected browser profile')
        if execute and profile.exists():
            shutil.rmtree(profile)
        result['actions'].append({'browser_profile': str(profile), 'action': 'remove recorded session profile'})
    if execute and task.exists():
        shutil.rmtree(task)
    result['actions'].append({'directory': str(task), 'action': 'remove only temporary fixture files'})
    result['artifacts_retained'] = str(artifacts)
    result['completed'] = execute
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', required=True, type=Path)
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--result', type=Path)
    args = parser.parse_args()
    result = cleanup(json.loads(args.manifest.read_text()), args.execute)
    encoded = json.dumps(result, ensure_ascii=False, indent=2) + '\n'
    if args.result:
        args.result.write_text(encoded)
    print(encoded, end='')


if __name__ == '__main__':
    main()
