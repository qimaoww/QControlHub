// Evidence only: production modules rendered with the repository's deterministic API fixtures.
import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { resolve, join, extname } from 'node:path';
const root=resolve(process.argv[2]), capture=resolve('docs/typography-20261018/capture');
const server=createServer(async(req,res)=>{
 try {
  const path=new URL(req.url,'http://localhost').pathname;
  res.setHeader('Cache-Control','no-store');
  if(path==='/favicon.ico'){res.writeHead(204).end();return;}
  if(path==='/'){
   res.setHeader('Content-Type','text/html;charset=utf-8');
   res.end('<!doctype html><html lang="zh-CN" data-review-version="after"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/assets/app.css"><div id="app"></div><script type="module" src="/review-runtime.mjs"></script></html>');return;
  }
  if(path==='/substore-fixture.mjs'||path==='/preset-fixture.mjs'){
   let code=await readFile(join(root,'frontend',path==='/substore-fixture.mjs'?'config_scope_browser_runtime.mjs':'presets_browser_runtime.mjs'),'utf8');
   if(path==='/preset-fixture.mjs')code=code.slice(0,code.indexOf('async function testEditedRetryDialog'));
   res.setHeader('Content-Type','text/javascript');
   res.end(code.replaceAll('"./modules/','"/assets/modules/').replaceAll('"./browser/','"/assets/browser/')+'\nexport { '+(path==='/substore-fixture.mjs'?'subStoreFixture':'fixture')+' };');return;
  }
  const file=path==='/review-runtime.mjs'?join(capture,'runtime.mjs'):path==='/assets/preset-plans.json'?resolve('output/playwright/typography-20261018/preset-plans.json'):resolve(root,'frontend',path.replace(/^\/assets\//,''));
  if(!file.startsWith(root+'/')&&!file.startsWith(capture+'/')&&path!=='/assets/preset-plans.json')throw Error('outside capture roots');
  res.setHeader('Content-Type',({'.css':'text/css','.js':'text/javascript','.mjs':'text/javascript','.json':'application/json','.svg':'image/svg+xml'})[extname(file)]||'text/plain');
  res.end(await readFile(file));
 }catch(e){res.writeHead(500).end(String(e));}
});
server.listen(0,'127.0.0.1',()=>console.log(`http://127.0.0.1:${server.address().port}`));
process.on('SIGTERM',()=>server.close());
