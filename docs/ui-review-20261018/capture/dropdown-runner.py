"""Run a captured flow using the real Playwright CLI and a matching page fixture."""
from pathlib import Path
import argparse,json,re,subprocess
p=argparse.ArgumentParser();p.add_argument('flow',choices=['selects','reality','protocol']);p.add_argument('device',choices=['desktop','mobile','tablet']);p.add_argument('stage',choices=['before','after']);p.add_argument('theme',choices=['light','dark']);p.add_argument('--base',required=True);p.add_argument('--session',required=True);args=p.parse_args()
root=Path(__file__).resolve().parents[1];scratch=Path('output/playwright/ui-review-20261018/round4');scratch.mkdir(parents=True,exist_ok=True)
source=root/'capture'/('dropdowns.js' if args.flow=='selects' else 'dropdown-'+args.flow+'.js');code=source.read_text()
for key,value in [('BASE',args.base),('STAGE',args.stage),('DEVICE',args.device),('THEME',args.theme)]:code=code.replace('__'+key+'__',value)
name='-'.join([args.flow,args.stage,args.device,args.theme]);f=scratch/(name+'.js');f.write_text(code)
r=subprocess.run(['/root/.codex/skills/playwright/scripts/playwright_cli.sh','-s='+args.session,'run-code','--filename',str(f)],capture_output=True,text=True);(scratch/(name+'.txt')).write_text(r.stdout+r.stderr);m=re.search(r'### Result\n([\s\S]*?)\n###',r.stdout)
if r.returncode or not m:raise RuntimeError(r.stdout+r.stderr)
(root/'evidence/dropdowns'/(name+'.json')).write_text(json.dumps(json.loads(m[1]),ensure_ascii=False,indent=2)+'\n')
