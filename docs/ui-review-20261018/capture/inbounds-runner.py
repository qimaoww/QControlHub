from pathlib import Path
import argparse,json,re,subprocess
p=argparse.ArgumentParser();p.add_argument('device',choices=['desktop','mobile','tablet']);p.add_argument('stage',choices=['before','after']);p.add_argument('theme',choices=['light','dark']);p.add_argument('--modal',action='store_true');p.add_argument('--base',required=True);p.add_argument('--session',required=True);a=p.parse_args()
root=Path(__file__).resolve().parents[1];scratch=Path('output/playwright/ui-review-20261018/round5');scratch.mkdir(parents=True,exist_ok=True)
code=(root/('capture/modal-inbounds.js' if a.modal else 'capture/inbounds.js')).read_text()
for k,v in [('BASE',a.base),('STAGE',a.stage),('DEVICE',a.device),('THEME',a.theme)]:code=code.replace('__'+k+'__',v)
name=('modal-' if a.modal else '')+'-'.join([a.stage,a.device,a.theme]);f=scratch/(name+'.js');f.write_text(code)
r=subprocess.run(['/root/.codex/skills/playwright/scripts/playwright_cli.sh','-s='+a.session,'run-code','--filename',str(f)],capture_output=True,text=True);(scratch/(name+'.txt')).write_text(r.stdout+r.stderr);m=re.search(r'### Result\n([\s\S]*?)\n###',r.stdout)
if r.returncode or not m:raise RuntimeError(r.stdout+r.stderr)
(root/'evidence/inbounds'/(name+'.json')).write_text(json.dumps(json.loads(m[1]),ensure_ascii=False,indent=2)+'\n');print(name+' captured')
