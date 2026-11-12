import hashlib
import json
import os
from pathlib import Path
import runpy
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT=Path(__file__).resolve().parents[2]
RAW=b'{"schemaVersion":2}'
DIGEST='sha256:'+hashlib.sha256(RAW).hexdigest()

class ArtifactTest(unittest.TestCase):
    def test_bundle_contains_templates_and_verified_checksums(self):
        with tempfile.TemporaryDirectory() as directory:
            work=Path(directory);(work/'digests').mkdir()
            (work/'digests'/'app.digest').write_text(DIGEST)
            env={**os.environ,'PREFIX':'ghcr.io/owner/repo','VERSION':'1.2.3','GITHUB_SHA':'b'*40}
            subprocess.run([sys.executable,str(ROOT/'scripts/release/bundle.py')],cwd=work,env=env,check=True)
            for line in (work/'release/SHA256SUMS').read_text().splitlines():
                checksum,name=line.split('  ')
                self.assertEqual(checksum,hashlib.sha256((work/'release'/name).read_bytes()).hexdigest())
            with tarfile.open(work/'release/canopy-1.2.3-deploy.tar.gz') as archive:
                self.assertEqual(set(archive.getnames()),{'compose.yaml','.env.example','README.md','images.json'})
                compose=archive.extractfile('compose.yaml').read().decode()
                self.assertIn('${CANOPY_IMAGE_PREFIX:-ghcr.io/owner/repo}:${CANOPY_VERSION:-1.2.3}',compose)
                self.assertNotIn('build:',compose)
                self.assertNotIn('docker.sock',compose)
                self.assertNotIn('CANOPY_WORK_DIR',compose)
                template=archive.extractfile('.env.example').read().decode()
                self.assertIn('CANOPY_VERSION=1.2.3',template)
                self.assertIn('POSTGRES_PASSWORD=\n',template)

    def promotion(self, bad=False):
        with tempfile.TemporaryDirectory() as directory:
            work=Path(directory);(work/'candidate').mkdir()
            images={'app':{'image':'ghcr.io/owner/repo','digest':DIGEST}}
            (work/'candidate/images.json').write_text(json.dumps({'version':'1.2.3','revision':'b'*40,'images':images}))
            old=Path.cwd()
            try:
                os.chdir(work)
                with patch.dict(os.environ,{'VERSION':'1.2.3','GITHUB_REPOSITORY':'Owner/Repo'}),patch('subprocess.check_output',return_value=b'bad-manifest' if bad else RAW),patch('subprocess.run') as command:
                    if bad:
                        with self.assertRaises(SystemExit):runpy.run_path(str(ROOT/'scripts/release/promote.py'))
                        command.assert_not_called()
                    else:
                        runpy.run_path(str(ROOT/'scripts/release/promote.py'))
                        self.assertEqual(command.call_count,1)
                        for call in command.call_args_list:
                            self.assertTrue(call.args[0][-1].endswith('@'+DIGEST))
                            self.assertTrue(call.args[0][-2].endswith(':latest'))
            finally:os.chdir(old)

    def test_promotion_preserves_all_digests(self):self.promotion()
    def test_drifted_release_fails_before_any_alias_write(self):self.promotion(bad=True)
