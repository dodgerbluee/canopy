import unittest
from metadata import metadata


class MetadataTest(unittest.TestCase):
    def test_dev_is_commit_specific(self):
        got = metadata("Owner/Canopy", "a" * 40, "dev")
        self.assertEqual(got["prefix"], "ghcr.io/owner/canopy")
        self.assertEqual(got["version"], "dev-" + "a" * 40)

    def test_stable_and_prerelease(self):
        for tag, expected in [("v1.2.3", "false"), ("v1.2.3-rc.1", "true")]:
            self.assertEqual(metadata("owner/repo", "b" * 40, "release", tag)["prerelease"], expected)

    def test_invalid_or_injected_tags_fail(self):
        for tag in ["latest", "v1.2", "v01.2.3", "v1.2.3-01", "v1.2.3+meta", "v1.2.3\nlatest=true", "v1.2.3;echo oops"]:
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                metadata("owner/repo", "b" * 40, "release", tag)

    def test_invalid_identity_fails(self):
        for repo, sha in [("repo", "a" * 40), ("a/b\n", "a" * 40), ("a/b", "deadbeef")]:
            with self.assertRaises(ValueError):
                metadata(repo, sha, "dev")


if __name__ == "__main__":
    unittest.main()
