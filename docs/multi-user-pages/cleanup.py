#!/usr/bin/env python3
"""Delete only this validation's recorded resources after its retention period."""
import argparse
import datetime as dt
import json
from pathlib import Path
import shutil
import subprocess


def command(arguments, check=True):
    return subprocess.run(arguments, check=check, capture_output=True, text=True)


def cleanup(manifest, execute):
    scope = 'multiuser-d6b9'
    if manifest['scope'] != scope:
        raise ValueError('unexpected validation scope')
    workspace = Path(manifest['workspace'])
    directory = Path(manifest['test_directory'])
    expected = workspace / '.preview' / scope
    if not workspace.is_absolute() or directory != expected or directory.is_symlink() or directory.resolve() != expected.resolve():
        raise ValueError('unexpected temporary directory')
    if workspace.resolve() / '.preview' / scope != directory.resolve():
        raise ValueError('temporary directory escaped its recorded workspace')
    due = dt.datetime.fromisoformat(manifest['execute_at_utc'])
    ended = dt.datetime.fromisoformat(manifest['testing_ended_at_utc'])
    if due - ended != dt.timedelta(hours=72):
        raise ValueError('retention must be exactly 72 hours')
    if execute and dt.datetime.now(dt.timezone.utc) < due:
        raise ValueError('72-hour retention has not elapsed')
    result = {'scope': scope, 'dry_run': not execute, 'actions': []}
    allowed_names = {'qch-multiuser-d6b9-db', 'qch-multiuser-d6b9-node'}
    for item in manifest['containers']:
        identity, name = item['id'], item['name']
        if name not in allowed_names or len(identity) != 64 or any(c not in '0123456789abcdef' for c in identity):
            raise ValueError('unexpected container identity')
        probe = command(['docker', 'inspect', identity], check=False)
        if probe.returncode:
            if 'No such object' not in probe.stderr and 'No such container' not in probe.stderr:
                raise RuntimeError('Docker unavailable; cleanup must retry')
            result['actions'].append({'name': name, 'action': 'already absent'})
            continue
        container = json.loads(probe.stdout)[0]
        if container['Id'] != identity or container['Name'].lstrip('/') != name or container['Config']['Labels'].get('qch.test.scope') != scope:
            raise ValueError('container identity or validation label changed')
        if execute:
            command(['docker', 'rm', '--force', '--volumes', identity])
        result['actions'].append({'name': name, 'action': 'remove recorded container and its anonymous volumes'})
    if execute and directory.exists():
        shutil.rmtree(directory)
    result['actions'].append({'directory': str(directory), 'action': 'remove only this temporary fixture'})
    result['artifacts_retained'] = str(workspace / 'docs' / 'multi-user-pages')
    result['completed'] = execute
    result['observed_at_utc'] = dt.datetime.now(dt.timezone.utc).isoformat()
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--manifest', required=True, type=Path)
    parser.add_argument('--execute', action='store_true')
    parser.add_argument('--result', type=Path)
    arguments = parser.parse_args()
    encoded = json.dumps(cleanup(json.loads(arguments.manifest.read_text()), arguments.execute), indent=2) + '\n'
    if arguments.result:
        arguments.result.write_text(encoded)
    print(encoded, end='')


if __name__ == '__main__':
    main()
