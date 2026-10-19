from pathlib import Path
import subprocess,re,json
p=Path('output/playwright/ui-review-20261018/round3'); evidence=Path('docs/ui-review-20261018/evidence/round3'); template=Path('docs/ui-review-20261018/capture/long-content.js').read_text(); logs=[];results=[]
def cli(*args):
 r=subprocess.run(['/root/.codex/skills/playwright/scripts/playwright_cli.sh','-s=ui-review-mobile',*args],text=True,capture_output=True);logs.append(r.stdout+r.stderr)
 if r.returncode or '### Error' in r.stdout:raise RuntimeError(r.stdout+r.stderr)
 return r.stdout
for stage,port in [('before',39511),('after',40409)]:
 code=template.replace('__BASE__',f'http://127.0.0.1:{port}').replace('__STAGE__',stage).replace('__DEVICE__','mobile').replace('__THEME__','light');code=code.replace('return {stage,device,theme,results};',"await page.locator('dialog[open] [data-cert-modal-content]').evaluate(e=>e.scrollTop=0);return {stage,device,theme,results};");f=p/'record-setup.js';f.write_text(code);cli('run-code','--filename',str(f));cli('video-start',str(evidence.resolve()/(stage+'-long-dns-scroll.webm')))
 f=p/'record-scroll.js';f.write_text('''async(page)=>{const scroller=page.locator('dialog[open] [data-cert-modal-content]');const start=await scroller.evaluate(e=>e.scrollTop);await page.mouse.move(195,500);for(let i=0;i<5;i++){await page.mouse.wheel(0,500);await page.waitForTimeout(280);}const end=await scroller.evaluate(e=>({top:e.scrollTop,height:e.clientHeight,total:e.scrollHeight}));for(let i=0;i<5;i++){await page.mouse.wheel(0,-500);await page.waitForTimeout(280);}const back=await scroller.evaluate(e=>e.scrollTop);if(end.top<=start||back>1)throw Error('Scroll did not travel down and back');return {start,end,back};}''');out=cli('run-code','--filename',str(f));match=re.search(r'### Result\n([\s\S]*?)\n###',out);results.append({'stage':stage,**json.loads(match[1])});cli('video-stop');print(stage,'scroll recorded',flush=True)
(evidence/'scroll-checks.json').write_text(json.dumps(results,indent=2)+'\n');(evidence/'scroll-recording.txt').write_text('\n'.join(logs))
