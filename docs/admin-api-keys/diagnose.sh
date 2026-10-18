#!/bin/sh
# Usage: QCH_API_BASE=https://panel.example.com/api/v1 QCH_API_KEY=... ./diagnose.sh [agent_id] [engine]
set -eu
exec python3 - "$@" <<'PY'
import json, os, sys, urllib.request, urllib.error, urllib.parse
base=os.environ['QCH_API_BASE'].rstrip('/')
key=os.environ['QCH_API_KEY']
agent=sys.argv[1] if len(sys.argv)>1 else ''
engine=sys.argv[2] if len(sys.argv)>2 else 'mihomo'
def get(path, params=None):
    url=base+path+('?' + urllib.parse.urlencode(params) if params else '')
    req=urllib.request.Request(url,headers={'Authorization':'Bearer '+key,'Accept':'application/json'})
    # Follow-up redirects must never forward credentials to a different host.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self,*args,**kwargs): return None
    try:
        with urllib.request.build_opener(NoRedirect).open(req,timeout=30) as response:
            value=json.load(response)
    except urllib.error.HTTPError as error:
        print(json.dumps({'path':path,'status':error.code,'error':json.loads(error.read())},ensure_ascii=False),file=sys.stderr)
        sys.exit(1)
    print(json.dumps({'path':path,'data':value},ensure_ascii=False))
    return value
info=get('/api-key')
allowed=set(info['endpoints'])
def maybe(pattern,path,params=None):
    if 'GET /api/v1'+pattern in allowed:return get(path,params)
maybe('/settings/deployment','/settings/deployment')
nodes=maybe('/agents','/agents') or []
if not agent and nodes: agent=nodes[0]['id']
maybe('/deployments','/deployments')
if agent:
    qagent=urllib.parse.quote(agent,safe='');qengine=urllib.parse.quote(engine,safe='')
    maybe('/agents/{id}/configs/{engine}/state',f'/agents/{qagent}/configs/{qengine}/state')
    maybe('/agents/{id}/configs/{engine}',f'/agents/{qagent}/configs/{qengine}')
    tasks=maybe('/tasks','/tasks',{'agent_id':agent,'limit':10}) or []
    for task in tasks[:3]:maybe('/tasks/{id}','/tasks/'+urllib.parse.quote(task['id'],safe=''))
    sources=maybe('/core-log-sources','/core-log-sources') or []
    for source in [s for s in sources if s['agent_id']==agent and s['engine']==engine][:3]:
        maybe('/core-logs','/core-logs',{'agent_id':agent,'instance_id':source['instance_id'],'engine':engine,'limit':10})
maybe('/audit','/audit',{'limit':10})
PY
