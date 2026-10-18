from pathlib import Path
import subprocess,re,json
p=Path('output/playwright/ui-review-20261018/round4');e=Path('docs/ui-review-20261018/evidence/dropdowns');logs=[];results=[]
def cli(*args):
 r=subprocess.run(['/root/.codex/skills/playwright/scripts/playwright_cli.sh','-s=ui-review-20261018',*args],text=True,capture_output=True);logs.append(r.stdout+r.stderr)
 if r.returncode or '### Error' in r.stdout:raise RuntimeError(r.stdout+r.stderr)
 return r.stdout
for stage,port in [('before',41767),('after',45231)]:
 f=p/'record-setup.js';f.write_text('''async(page)=>{await page.setViewportSize({width:844,height:390});await page.emulateMedia({colorScheme:'dark',reducedMotion:'reduce'});await page.goto('http://127.0.0.1:PORT/?kind=retry');await page.waitForSelector('html[data-review-ready=true]');await page.evaluate(async()=>{const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');createShellAppearance({data:{}}).applyTheme('dark');});await page.locator('#task-status').scrollIntoViewIfNeeded();}'''.replace('PORT',str(port)))
 cli('run-code','--filename',str(f));cli('video-start',str((e/(stage+'-dropdown-scroll.webm')).resolve()))
 code='''async(page)=>{const select=page.locator('#task-status');await select.click();await page.waitForTimeout(400);await page.keyboard.press('End');await page.waitForTimeout(500);await page.keyboard.press('Home');await page.waitForTimeout(500);const metrics=await page.evaluate(()=>{const p=document.querySelector('[role=listbox]');return {value:document.querySelector('#task-status').value,menu:p&&{height:p.clientHeight,scroll:p.scrollHeight,top:p.scrollTop,bounds:p.getBoundingClientRect().toJSON()}}});await page.keyboard.press('Escape');await page.waitForTimeout(300);return metrics;}'''
 f=p/'record-action.js';f.write_text(code);out=cli('run-code','--filename',str(f));m=re.search(r'### Result\n([\s\S]*?)\n###',out);results.append({'stage':stage,'viewport':{'width':844,'height':390},'theme':'dark',**json.loads(m[1])});cli('video-stop');print(stage,'recorded',flush=True)
(e/'scroll-checks.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n');(e/'scroll-recording.txt').write_text('\n'.join(logs))
