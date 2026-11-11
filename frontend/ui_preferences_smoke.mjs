import assert from 'node:assert/strict';
import { createUIPreferences } from './modules/ui-preferences.js';
const deferred = () => { let resolve, reject; const promise = new Promise((a,b) => { resolve=a; reject=b; }); return {promise,resolve,reject}; };
const clone = value => structuredClone(value);
let document = {values: {logs:{q:'old',automatic:true},traffic:{scope:'all'}},revision:1};
let writes=0, gets=0, conflict=false, lost=false, reports=[];
const request = async (_, options={}) => {
 if (!options.method) {gets++;return clone(document);}
 writes++;
 if (conflict) {conflict=false;document.values.logs.level='error';document.revision++;}
 if (options.body.revision !== document.revision) throw Object.assign(new Error('conflict'),{status:409});
 for (const [page,fields] of Object.entries(options.body.values)) Object.assign(document.values[page] ||= {},fields);
 document.revision++;
 if (lost) {lost=false;throw Object.assign(new Error('response lost'),{status:408});}
 return clone(document);
};
const p=createUIPreferences({request,delay:100000,report:m=>reports.push(m)});
await p.ready();
p.patch('logs',{q:'',automatic:false});conflict=true;
await p.flush();
assert.equal(document.values.logs.q,'');assert.equal(document.values.logs.automatic,false);assert.equal(document.values.logs.level,'error');assert.equal(document.values.traffic.scope,'all');assert.equal(writes,2);
// Choosing a default is an intentional write even if a different device
// changed that field since this client's last read.
document.values.logs.q='other device';document.revision++;p.patch('logs',{q:''});await p.flush();assert.equal(document.values.logs.q,'');
const beforeLost=writes;
p.patch('logs',{q:'committed'});lost=true;await p.flush();assert.equal(writes,beforeLost+1);assert.equal(document.values.logs.q,'committed');assert.deepEqual(reports,[]);
// Slow initial reads cannot replace a user's newer input, including defaults.
const held=deferred();let delayedWrites=[];
const slow=createUIPreferences({delay:100000,request:async(_,o={})=>{if(!o.method)return held.promise;delayedWrites.push(o.body);return {values:o.body.values,revision:2};}});
const ready=slow.ready();slow.patch('logs',{q:'new',automatic:false});held.resolve({values:{logs:{q:'old',automatic:true}},revision:1});await ready;
assert.equal(slow.get('logs').q,'new');await slow.flush();assert.equal(delayedWrites[0].values.logs.automatic,false);
// A change arriving during a save is not acknowledged by the old response.
const saving=deferred();let rounds=0;
const racing=createUIPreferences({delay:100000,request:async(_,o={})=>{if(!o.method)return {values:{},revision:0};if(++rounds===1)return saving.promise;return {values:{logs:{q:o.body.values.logs.q}},revision:2};}});
await racing.ready();racing.patch('logs',{q:'first'});const running=racing.flush();await Promise.resolve();await Promise.resolve();racing.patch('logs',{q:'last'});saving.resolve({values:{logs:{q:'first'}},revision:1});await running;assert.equal(racing.get('logs').q,'last');assert.equal(rounds,2);
// Repeated conflicts stop, keep the active choice, and can be retried.
let calls=0;
const blocked=createUIPreferences({delay:100000,report:m=>reports.push(m),request:async(_,o={})=>{calls++;if(!o.method)return {values:{},revision:calls};throw Object.assign(new Error('busy'),{status:409});}});
await blocked.ready();blocked.patch('logs',{q:'kept'});await blocked.flush();assert.ok(calls<=10);assert.equal(blocked.get('logs').q,'kept');assert.equal(reports.length,1);
// Disposed accounts neither retry nor write into the next authenticated session.
const login=deferred();let crossWrites=0;
const old=createUIPreferences({delay:100000,request:async(_,o={})=>{if(o.method)crossWrites++;return login.promise;}});
old.patch('logs',{q:'private'});const oldRead=old.ready();old.dispose();login.resolve({values:{logs:{q:'private'}},revision:0});await oldRead;await old.flush();assert.equal(crossWrites,0);assert.deepEqual(old.get('logs'),{});
// pagehide can end the document lifetime before the next microtask. Loaded
// choices must start their keepalive request during that event itself.
let departing=true, sent=false;
const unload=createUIPreferences({delay:100000,isCurrent:()=>departing,request:async(_,o={})=>{if(o.method){sent=o.keepalive;return {values:o.body.values,revision:1};}return {values:{},revision:0};}});
await unload.ready();unload.patch('nodes',{search:'before reload'});const leaving=unload.flush();departing=false;assert.equal(sent,true);await leaving;
for(const item of [p,slow,racing,blocked,old,unload])item.dispose();
console.log('Account UI preferences: field patches, empty/false, conflicts, uncertain commits, late reads, races and logout isolation passed');
