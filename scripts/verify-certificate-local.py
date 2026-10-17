#!/usr/bin/env python3
"""Exercise a real ordinary-user session against the optional local Pebble fixture.
No production CA, DNS account or public listener is used by this evidence runner.
"""
import http.cookiejar, json, pathlib, time, urllib.request
BASE='http://127.0.0.1:35479/api/v1'
jar=http.cookiejar.CookieJar()
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
csrf=''
def call(method,path,data=None):
    body=json.dumps(data).encode() if data is not None else None
    request=urllib.request.Request(BASE+path,data=body,method=method,headers={'Content-Type':'application/json','X-QControlHub-CSRF':csrf})
    with client.open(request,timeout=35) as response:
        raw=response.read()
        return json.loads(raw) if raw else None
session=call('POST','/auth/login',{'username':'cert-alice','token':'certificate local fixture password'})
csrf=session['csrf_token']
accounts=call('GET','/certificate-accounts')
account=next((a for a in accounts if a['name']=='Pebble local test'),None)
if account is None:
    account=call('POST','/certificate-accounts',{'name':'Pebble local test','kind':'acme','ca':'letsencrypt-staging','email':'alice@example.test','accept_terms':True})
print('Ordinary-user ACME account registered with local Pebble',flush=True)
cert=call('POST','/certificates',{'name':'ACME · 双域名验证','source':'acme','domains':['edge.example.test','alt.example.test'],'method':'dns-manual','account_id':account['id'],'key_type':'ecdsa256','auto_renew':False})
order=None
for _ in range(30):
    detail=call('GET','/certificates/'+cert['id'])
    if detail['orders'] and detail['orders'][0]['status']=='waiting_dns':
        order=detail['orders'][0];break
    time.sleep(1)
assert order, detail
print('Persisted manual DNS order exposes each SAN challenge',flush=True)
records=[]
try:
    for challenge in order['challenges']:
        records.append(challenge['name']+'.')
        request=urllib.request.Request('http://127.0.0.1:35486/set-txt',data=json.dumps({'host':records[-1],'value':challenge['value']}).encode(),headers={'Content-Type':'application/json'})
        urllib.request.urlopen(request,timeout=5).close()
    call('POST','/certificates/'+cert['id']+'/orders/'+order['id']+'/continue',{})
    for _ in range(60):
        detail=call('GET','/certificates/'+cert['id'])
        if detail['certificate']['version']>0:break
        time.sleep(1)
    assert detail['certificate']['version']==1, detail
    assert detail['certificate']['domains']==['edge.example.test','alt.example.test'],detail
    print('Real local CA validated both DNS-01 SAN authorizations and issued v1',flush=True)
    pair=call('POST','/certificates',{'name':'通配符 · 开发环境','source':'self-signed','domains':['example.test','*.example.test'],'key_type':'rsa2048','days':90})
    call('POST','/certificates/'+pair['id']+'/grants',{'username':'cert-bob'})
    pathlib.Path('.preview/certificate-center-a978/local-result.json').write_text(json.dumps({'acme_certificate':cert['id'],'self_signed_certificate':pair['id'],'account':account['id'],'local_ca':'Pebble v2.10.1; actual DNS challenge verification enabled'},indent=2))
finally:
    for name in records:
        request=urllib.request.Request('http://127.0.0.1:35486/clear-txt',data=json.dumps({'host':name}).encode(),headers={'Content-Type':'application/json'})
        urllib.request.urlopen(request,timeout=5).close()
print('Local TXT records cleaned; certificate data retained in task-owned database',flush=True)
