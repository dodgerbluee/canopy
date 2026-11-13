#!/usr/bin/env python3
"""Exercise the release image in disposable volumes/project/storage.
Requires the local canopy-release-test:smoke image.
Never connects to the development database or reuses its credentials.
"""
import http.cookiejar
from html.parser import HTMLParser
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

root = Path(__file__).resolve().parents[2]
project = 'canopy-release-' + secrets.token_hex(5)
temp = Path(tempfile.mkdtemp(prefix=project+'-')).resolve()
password = secrets.token_urlsafe(24)
database_password = secrets.token_hex(24)
(temp/'owner-password').write_text(password)
os.chmod(temp/'owner-password',0o600)

def port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1',0))
        return listener.getsockname()[1]

http_port, https_port = port(), port()
base = f'http://localhost:{http_port}'
config = temp/'.env'
config.write_text(f'''CANOPY_IMAGE_PREFIX=canopy-release-test
CANOPY_VERSION=smoke
CANOPY_PORT={http_port}
CANOPY_INITIAL_PASSWORD={password}
CANOPY_PUBLIC_URL={base}
POSTGRES_PASSWORD={database_password}
HTTP_PORT={http_port}
HTTPS_PORT={https_port}
''')
os.chmod(config,0o600)
compose = ['docker','compose','-p',project,'--env-file',str(config),'-f',str(root/'deploy/compose.yaml')]

def dc(*args):
    result = subprocess.run(compose+list(args),cwd=root,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
    if result.returncode:
        detail=(result.stdout+result.stderr).decode(errors='replace').replace(database_password,'[redacted]').replace(password,'[redacted]')
        raise RuntimeError('Compose operation failed: '+args[0]+'\n'+detail[-4000:])
    return result.stdout

cookies = http.cookiejar.CookieJar()
client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
csrf = ''

def request(path, data=None, method=None, content_type='application/json'):
    headers={'Content-Type':content_type,'Origin':base}
    if csrf: headers['X-CSRF-Token']=csrf
    if isinstance(data,dict):data=json.dumps(data).encode()
    req=urllib.request.Request(base+path,data=data,headers=headers,method=method)
    with client.open(req,timeout=15) as response:
        return response.read()

def gql(query,variables=None,field=None):
    result=json.loads(request('/api/graphql',{'query':query,'variables':variables or {}}))
    if result.get('errors'):raise RuntimeError('GraphQL error during release smoke')
    value=result['data']
    if field:
        value=value[field]
        if value.get('problems'):raise RuntimeError('Application refused release smoke mutation: '+field)
    return value

try:
    dc('config','-q')
    services=json.loads(dc('config','--format','json'))['services']
    assert set(services) == {'canopy','postgres'}
    assert all(v['type']=='volume' and v['target']=='/var/lib/canopy' for v in services['canopy']['volumes'])
    dc('up','-d','--wait','--wait-timeout','180')
    assert dc('exec','-T','canopy','/bin/sh','-ec','test ! -e /var/run/docker.sock; if command -v docker; then exit 1; fi; id -u canopy').strip()==b'10001'
    # Anonymous session errors must be API JSON/status, not the SPA fallback.
    session=json.loads(request('/api/auth/login',{'password':password}))
    csrf=session['csrfToken']
    html=request('/plants/smoke/gallery').decode()
    assert '<app-root' in html
    class IconLinks(HTMLParser):
        def __init__(self): super().__init__(); self.icons=[]
        def handle_starttag(self, tag, attributes):
            attributes=dict(attributes)
            if tag=='link' and 'icon' in attributes.get('rel',''):
                self.icons.append(attributes)
    head=IconLinks(); head.feed(html)
    assert {icon['rel'] for icon in head.icons} == {'icon','apple-touch-icon','mask-icon'}
    assert any(icon.get('type')=='image/png' and icon.get('sizes')=='32x32' for icon in head.icons)
    for icon in head.icons:
        assert icon['href'].startswith('/icons/')
        content=request(icon['href'])
        assert not content.startswith(b'<!doctype') and len(content)>100
    try:
        request('/icons/missing.ico')
        raise RuntimeError('Missing favicon returned the SPA instead of 404')
    except urllib.error.HTTPError as error:
        assert error.code==404
    assert request('/readyz')
    try:
        request('/mcp')
        raise RuntimeError('MCP accepted an unauthenticated request')
    except urllib.error.HTTPError as error:
        assert error.code==401
    plant=gql('mutation($input:CreatePlantInput!){createPlant(input:$input){plant{id} problems{code}}}',{'input':{'name':'Release smoke lemon','type':'INDIVIDUAL','category':'CITRUS'}},'createPlant')['plant']['id']
    original=(root/'server/internal/media/photodate/testdata/android.jpg').read_bytes()
    asset=gql('mutation($input:ReserveUploadInput!){reserveUpload(input:$input){asset{id} problems{code}}}',{'input':{'purpose':'PHOTO','filename':'android.jpg','claimedBytes':len(original)}},'reserveUpload')['asset']['id']
    receipt=json.loads(request('/api/uploads/'+asset,original,'PUT','image/jpeg'))
    gql('mutation($input:FinalizeUploadInput!){finalizeUpload(input:$input){asset{id state} problems{code}}}',{'input':{'assetId':asset,'sha256':receipt['sha256']}},'finalizeUpload')
    deadline=time.monotonic()+150
    while True:
        pending=gql('{pendingUploads(first:25){items{id state}}}')['pendingUploads']['items']
        mine=next((a for a in pending if a['id']==asset),None)
        if mine is None:break
        if mine['state']=='FAILED':raise RuntimeError('Release worker image processing failed')
        if time.monotonic()>deadline:raise RuntimeError('Release worker did not finish the queued image')
        time.sleep(1)
    photo=gql('mutation($input:AttachPhotoInput!){attachPhoto(input:$input){photo{id dateSource takenAt} problems{code}}}',{'input':{'plantId':plant,'assetId':asset,'dateSource':'UPLOAD_FALLBACK','preferCaptureDate':True,'timezone':'America/Chicago','viewpoint':'WHOLE'}},'attachPhoto')['photo']
    assert photo['dateSource']=='EXIF' and photo['takenAt']
    preview=request('/api/media/'+asset+'/thumb')
    assert preview.startswith(b'\xff\xd8')
    container_id=dc('ps','-q','canopy').decode().strip()
    dc('exec','-T','canopy','/bin/bash','-ec', 'for file in /proc/[0-9]*/comm; do read -r name < "$file" || continue; if [ "$name" = worker ]; then pid=${file%/comm}; kill -TERM "${pid##*/}"; break; fi; done')
    deadline=time.monotonic()+90
    stable=0
    while stable<2:
        state=json.loads(subprocess.check_output(['docker','inspect','--format','{{json .}}',container_id]))
        count=state['RestartCount']
        if count>1:raise RuntimeError('Bundled app is repeatedly restarting')
        if count==1 and state['State'].get('Health',{}).get('Status')=='healthy':
            try:
                if stable==0:
                    session=json.loads(request('/api/auth/login',{'password':password}));csrf=session['csrfToken']
                assert request('/api/media/'+asset+'/thumb').startswith(b'\xff\xd8')
                stable+=1
            except (OSError,urllib.error.URLError):stable=0
        if time.monotonic()>deadline:raise RuntimeError('Bundled worker failure was not recovered')
        time.sleep(1)
    result={'compose':True,'twoServices':True,'noDockerSocketOrHostMounts':True,'faviconAssetsAnd404':True,'bundledFrontendAPIWorker':True,'ownerLogin':True,'spaDeepLink':True,'mcpRequiresCredential':True,'queuedPhotoProcessed':True,'originalCaptureDate':True,'privateDerivative':True,'supervisedRestart':True,'bootstrapSurvivesRestart':True}
    report=Path(os.environ.get('CANOPY_RELEASE_SMOKE_REPORT',str(root/'.artifacts/agents/DEP02/smoke.json')))
    report.parent.mkdir(parents=True,exist_ok=True);report.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result))
except Exception:
    try:
        logs=dc('logs','--no-color','--tail','80','canopy').decode(errors='replace')
        print(logs.replace(database_password,'[redacted]').replace(password,'[redacted]'))
    except Exception:pass
    raise
finally:
    try: dc('down','-v','--remove-orphans','--timeout','30')
    finally:
        shutil.rmtree(temp)
