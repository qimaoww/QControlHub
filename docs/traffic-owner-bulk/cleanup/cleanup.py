#!/usr/bin/python3
"""Durable, identity-guarded cleanup for this task's disposable local fixture."""
import datetime
import json
import pathlib
import shutil
import subprocess
import sys

REGISTRY = pathlib.Path('/var/lib/qcontrolhub-task-cleanup/qch-traffic-owner-bulk-20261019')
schedule = json.loads((REGISTRY / 'schedule.json').read_text())
now = datetime.datetime.now(datetime.timezone.utc)
if now < datetime.datetime.fromisoformat(schedule['not_before']):
    sys.exit(0)


def run(*args):
    return subprocess.run(args, text=True, capture_output=True)


if run('/usr/bin/docker', 'info', '--format', '{{.ServerVersion}}').returncode:
    raise RuntimeError('Docker unavailable; cron will retry')
container = run('/usr/bin/docker', 'inspect', schedule['container_id'])
if container.returncode == 0:
    item = json.loads(container.stdout)[0]
    if (item['Id'] != schedule['container_id'] or
            item['Name'] != '/' + schedule['container_name'] or
            item['Config']['Labels'].get('qch.task') != schedule['task_id']):
        raise RuntimeError('Container identity changed; refusing cleanup')
    mounts = [m.get('Name') for m in item.get('Mounts', []) if m['Type'] == 'volume']
    if mounts != [schedule['volume']['Name']]:
        raise RuntimeError('Container mounts changed; refusing cleanup')
    result = run('/usr/bin/docker', 'rm', '-v', schedule['container_id'])
    if result.returncode:
        raise RuntimeError(result.stderr)
volume = run('/usr/bin/docker', 'volume', 'inspect', schedule['volume']['Name'])
if volume.returncode == 0:
    item = json.loads(volume.stdout)[0]
    if any(item.get(k) != schedule['volume'][k] for k in ('Name', 'Driver', 'CreatedAt')):
        raise RuntimeError('Volume identity changed; refusing cleanup')
    result = run('/usr/bin/docker', 'volume', 'rm', schedule['volume']['Name'])
    if result.returncode:
        raise RuntimeError(result.stderr)
for value in schedule['directories']:
    path = pathlib.Path(value)
    if not path.exists():
        continue
    if (path.is_symlink() or str(path.resolve()) != value or
            (path / '.cleanup-owner').read_text().strip() != schedule['marker']):
        raise RuntimeError('Directory ownership changed; refusing cleanup: ' + value)
    shutil.rmtree(path)
schedule['status'] = 'complete'
schedule['completed_at'] = now.isoformat()
(REGISTRY / 'schedule.json').write_text(json.dumps(schedule, indent=2) + '\n')
(REGISTRY / 'receipt.json').write_text(json.dumps(schedule, indent=2) + '\n')
pathlib.Path('/etc/cron.d/qch-traffic-owner-bulk-20261019').unlink(missing_ok=True)
