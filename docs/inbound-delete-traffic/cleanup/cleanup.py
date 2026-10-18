#!/usr/bin/python3
import datetime,json,pathlib,shutil,subprocess,sys
REGISTRY=pathlib.Path('/var/lib/qcontrolhub-task-cleanup/qch-inbound-delete-20261019')
schedule=json.loads((REGISTRY/'schedule.json').read_text())
now=datetime.datetime.now(datetime.timezone.utc)
if now<datetime.datetime.fromisoformat(schedule['not_before']):
    sys.exit(0)
def run(*args):
    return subprocess.run(args,text=True,capture_output=True)
daemon=run('/usr/bin/docker','info','--format','{{.ServerVersion}}')
if daemon.returncode: raise RuntimeError('Docker unavailable; retry cleanup later')
container=run('/usr/bin/docker','inspect',schedule['container_id'])
if container.returncode==0:
    item=json.loads(container.stdout)[0]
    if item['Id']!=schedule['container_id'] or item['Name']!='/'+schedule['container_name']:
        raise RuntimeError('Container identity changed; refusing cleanup')
    mounts=[m.get('Name') for m in item.get('Mounts',[]) if m['Type']=='volume']
    if mounts!=[schedule['volume']]:
        raise RuntimeError('Container mounts changed; refusing cleanup')
    removed=run('/usr/bin/docker','rm','-f','-v',schedule['container_id'])
    if removed.returncode: raise RuntimeError(removed.stderr)
volume=run('/usr/bin/docker','volume','inspect',schedule['volume'])
if volume.returncode==0:
    removed=run('/usr/bin/docker','volume','rm',schedule['volume'])
    if removed.returncode: raise RuntimeError(removed.stderr)
for value in schedule['directories']:
    path=pathlib.Path(value)
    if not path.exists(): continue
    if path.is_symlink() or str(path.resolve())!=value or (path/'.cleanup-owner').read_text().strip()!=schedule['marker']:
        raise RuntimeError('Directory ownership changed; refusing cleanup: '+value)
    shutil.rmtree(path)
schedule['status']='complete'
schedule['completed_at']=now.isoformat()
(REGISTRY/'schedule.json').write_text(json.dumps(schedule,indent=2)+'\n')
(REGISTRY/'receipt.json').write_text(json.dumps(schedule,indent=2)+'\n')
pathlib.Path('/etc/cron.d/qch-inbound-delete-20261019').unlink(missing_ok=True)
