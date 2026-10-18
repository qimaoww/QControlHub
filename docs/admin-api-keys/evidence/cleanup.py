#!/usr/bin/python3
"""Delayed deletion of resources recorded for QControlHub PR 304 only."""
import datetime, hashlib, json, pathlib, shutil, subprocess, sys
base=pathlib.Path(__file__).resolve().parent
m=json.loads((base/'manifest.json').read_text())
check=sys.argv[1:]==['--check']
if not check and datetime.datetime.now(datetime.timezone.utc)<datetime.datetime.fromisoformat(m['due_at']):
    raise SystemExit('cleanup is not due')
r=subprocess.run(['docker','inspect',m['container_name']],capture_output=True,text=True)
container=None
if r.returncode==0:
    container=json.loads(r.stdout)[0]
    if container['Id']!=m['container_id'] or container['Config']['Labels'].get('qch.task')!=m['container_label']:
        raise SystemExit('container identity mismatch; nothing deleted')
    if container['State']['Running']:
        raise SystemExit('container was restarted; nothing deleted')
for value in m['directories']:
    p=pathlib.Path(value)
    if p.exists() and (p.is_symlink() or str(p.resolve())!=value or (p/'.task-cleanup-marker').read_text()!=m['marker']):
        raise SystemExit('directory identity mismatch; nothing deleted')
if check:
    print(json.dumps({'dry_run':True,'container_identity_verified':container is not None,'directories':m['directories'],'recorded_browser_artifacts':len(m['files'])}))
    raise SystemExit(0)
if container is not None:
    subprocess.run(['docker','rm','-v',m['container_id']],check=True,capture_output=True)
for value in m['directories']:
    p=pathlib.Path(value)
    if p.exists():shutil.rmtree(p)
removed=0
for value in m['files']:
    p=pathlib.Path(value['path'])
    if p.is_file() and not p.is_symlink() and hashlib.sha256(p.read_bytes()).hexdigest()==value['sha256']:
        p.unlink();removed+=1
(base/'completed.json').write_text(json.dumps({'completed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'task':m['task'],'browser_files_removed':removed},indent=2)+'\n')
print('PR 304 test resources cleaned; saved evidence retained')
