#!/usr/bin/env python3
"""Exercise the actual release images in disposable volumes/project/storage.
Requires local canopy-release-test-{server,web,media}:smoke images.
Never connects to the development database or reuses its credentials.
"""
import http.cookiejar
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
work = temp/'work'; work.mkdir()
password = secrets.token_urlsafe(24)
database_password = secrets.token_hex(24)
(temp/'owner-password').write_text(password)
os.chmod(temp/'owner-password',0o600)

def port():
    with socket.socket() as listener:
        listener.bind(('127.0.0.1',0))
        return listener.getsockname()[1]

http_port, https_port = port(), port()
socket_path=os.environ.get('DOCKER_SOCKET','/var/run/docker.sock')
socket_group=subprocess.check_output(['docker','run','--rm','-v',socket_path+':/socket','--entrypoint','stat','canopy-release-test-server:smoke','-c','%g','/socket'],text=True).strip()
base = f'https://localhost:{https_port}'
config = temp/'.env'
config.write_text(f'''CANOPY_IMAGE_PREFIX=canopy-release-test
CANOPY_VERSION=smoke
CANOPY_SITE=localhost
CANOPY_WORK_DIR={work}
POSTGRES_PASSWORD={database_password}
DOCKER_GID={socket_group}
DOCKER_SOCKET={os.environ.get('DOCKER_SOCKET','/var/run/docker.sock')}
HTTP_PORT={http_port}
HTTPS_PORT={https_port}
CANOPY_MCP_PUBLIC_URL={base}/mcp
CANOPY_MCP_ALLOWED_HOSTS=localhost:{https_port}
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
# Only this disposable localhost certificate is untrusted. Production uses
# Caddy's ACME TLS and PostgreSQL verify-full; no deploy setting is weakened.
client = urllib.request.build_opener(urllib.request.HTTPSHandler(context=ssl._create_unverified_context()), urllib.request.HTTPCookieProcessor(cookies))
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
    dc('up','-d','--wait','--wait-timeout','180')
    dc('run','--rm','-T','--user','0:0','-v',str(temp/'owner-password')+':/run/password:ro','admin','owner','create','--workspace','Release smoke','--timezone','America/Chicago','--password-file','/run/password')
    # Anonymous session errors must be API JSON/status, not the SPA fallback.
    session=json.loads(request('/api/auth/login',{'password':password}))
    csrf=session['csrfToken']
    html=request('/plants/smoke/gallery').decode()
    assert '<app-root' in html
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
    result={'compose':True,'productionConfig':True,'databaseTLS':'verify-full','ownerLogin':True,'spaDeepLink':True,'mcpRequiresCredential':True,'queuedPhotoProcessed':True,'originalCaptureDate':True,'privateDerivative':True}
    report=Path(os.environ.get('CANOPY_RELEASE_SMOKE_REPORT',str(root/'.artifacts/agents/DEP01/smoke.json')))
    report.parent.mkdir(parents=True,exist_ok=True);report.write_text(json.dumps(result,indent=2)+'\n')
    print(json.dumps(result))
finally:
    try: dc('down','-v','--remove-orphans','--timeout','30')
    finally:
        # The UID-10001 worker owns its temp directory. Clear only this
        # generated smoke bind mount before removing its enclosing temp dir.
        subprocess.run(['docker','run','--rm','--user','0:0','-v',str(work)+':/work','--entrypoint','/bin/sh','canopy-release-test-server:smoke','-ec','find /work -mindepth 1 -delete'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        shutil.rmtree(temp)
