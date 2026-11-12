#!/usr/bin/env python3
"""Preflight the full release set before advancing any latest alias."""
import json
import hashlib
import os
from pathlib import Path
import re
import subprocess
from metadata import metadata

version = os.environ['VERSION']
manifest = json.loads(Path('candidate/images.json').read_text())
expected = metadata(os.environ['GITHUB_REPOSITORY'], manifest['revision'], 'release', 'v'+version)
if expected['prerelease'] != 'false' or manifest['version'] != version:
    raise SystemExit('Stable release manifest required')
if set(manifest['images']) != {'app'}:
    raise SystemExit('Incomplete release image set')

def digest(ref):
    raw = subprocess.check_output(['docker','buildx','imagetools','inspect','--raw',ref])
    return 'sha256:' + hashlib.sha256(raw).hexdigest()

for image in manifest['images'].values():
    if image['image'] != expected['prefix'] or not re.fullmatch(r'sha256:[0-9a-f]{64}', image['digest']):
        raise SystemExit('Invalid release image identity')
    if digest(image['image']+':'+version) != image['digest']:
        raise SystemExit('Version tag no longer matches the tested release manifest')
for image in manifest['images'].values():
    subprocess.run(['docker','buildx','imagetools','create','--tag',image['image']+':latest',image['image']+'@'+image['digest']],check=True)
    if digest(image['image']+':latest') != image['digest']:
        raise SystemExit('Promoted digest differs from the release')
print('Promoted the existing release digest; no rebuild.')
