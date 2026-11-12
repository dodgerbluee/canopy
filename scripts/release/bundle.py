#!/usr/bin/env python3
import hashlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import tarfile

root = Path(__file__).resolve().parents[2]
prefix, version = os.environ['PREFIX'], os.environ['VERSION']
digest = Path('digests/app.digest').read_text().strip()
if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
    raise SystemExit('Invalid image digest')
images = {'app': {'image': prefix, 'digest': digest}}
out = Path('release')
out.mkdir(exist_ok=True)
manifest = {'version': version, 'revision': os.environ['GITHUB_SHA'], 'images': images}
(out/'images.json').write_text(json.dumps(manifest, indent=2)+'\n')
with tarfile.open(out/f'canopy-{version}-deploy.tar.gz', 'w:gz') as archive:
    compose = (root/'deploy/compose.yaml').read_text()
    data = compose.encode()
    info = tarfile.TarInfo('compose.yaml'); info.size = len(data); info.mode = 0o644
    archive.addfile(info, io.BytesIO(data))
    # This template contains placeholders only; never archive a live .env.
    template = (root/'deploy/.env.example').read_text().replace('ghcr.io/dodgerbluee/canopy', prefix).replace('CANOPY_VERSION=local', 'CANOPY_VERSION='+version)
    data = template.encode()
    info = tarfile.TarInfo('.env.example'); info.size=len(data); info.mode=0o644
    archive.addfile(info, io.BytesIO(data))
    archive.add(root/'docs/deployment.md', arcname='README.md')
    archive.add(out/'images.json', arcname='images.json')
checksums = []
for path in sorted(out.iterdir()):
    if path.name != 'SHA256SUMS':
        checksums.append(hashlib.sha256(path.read_bytes()).hexdigest()+'  '+path.name)
(out/'SHA256SUMS').write_text('\n'.join(checksums)+'\n')
