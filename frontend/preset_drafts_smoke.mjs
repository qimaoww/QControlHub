import assert from "node:assert/strict";
import {createPresetDrafts} from "./modules/preset-drafts.js";
import {presetRoute, readPresetRoute} from "./modules/preset-route.js";

const route = presetRoute({agentId:"node&special=1",engine:"sing-box",credential:"never-in-url"});
assert.deepEqual(readPresetRoute(route),{agentId:"node&special=1",engine:"sing-box"});
assert.equal(route.includes("never-in-url"),false);
assert.equal(readPresetRoute("#agent-config"),null);
assert.equal(readPresetRoute("#tasks?agent=x"),null);

const form = () => ({isConnected:true, elements:[
  {name:"tag",type:"text",value:"in-1"}, {name:"secret",type:"password",value:"initial"},
  {name:"enabled",type:"checkbox",value:"1",checked:true}, {name:"operation",type:"select-one",value:"modify"},
]});
const draft = createPresetDrafts(), scope = "node|xray|", key = `${scope}1|plan|in-1`;
const first = form();
draft.bind(first,key);
assert.equal(draft.dirty(),false);
first.elements[1].type="text";
assert.equal(draft.dirty(),false,"revealing a secret should not mark the form dirty");
first.elements[1].value="unsaved-secret";
first.elements[2].checked=false;
draft.capture(); first.isConnected=false;
const second = form();
draft.bind(second,key);
assert.equal(second.elements[1].value,"unsaved-secret");
assert.equal(second.elements[2].checked,false);
assert.equal(draft.otherDirty(key,scope),false);
assert.equal(draft.otherDirty(`${scope}1|source`,scope),true);
assert.equal(draft.otherDirty("node|mihomo|1|source","node|mihomo|"),false);
assert.equal(draft.dirty("node|mihomo|"),false,"another core's draft must not block current-core refresh");
assert.equal(draft.dirty(scope),true);
const newer = form(); draft.bind(newer,`${scope}2|plan|in-1`);
assert.equal(newer.elements[1].value,"initial","stale draft crossed revision boundary");
draft.bind(second,key);
assert.equal(draft.dirty(),true,"rebinding lost dirty baseline");
second.elements[1].value="initial";second.elements[2].checked=true;
assert.equal(draft.dirty(),false);
second.elements[0].value="draft";draft.capture();
draft.clear(scope);second.isConnected=false;newer.isConnected=false;
assert.equal(draft.dirty(),false,"committed draft resurfaced");
const reloaded = form();draft.bind(reloaded,key);
assert.equal(reloaded.elements[0].value,"in-1");
console.log("Preset draft logic smoke passed");
