#!/usr/bin/env python3
"""Pure release metadata: no shell interpolation, network or repository writes."""
import argparse
import json
import re

SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$")


def metadata(repository, sha, channel, tag=""):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("Expected an owner/repository")
    if not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("Expected a full commit SHA")
    prefix = "ghcr.io/" + repository.lower()
    if channel == "dev":
        version = "dev-" + sha
        prerelease = True
    elif channel == "release":
        version = tag.removeprefix("v")
        if not tag.startswith("v") or not SEMVER.fullmatch(version) or len(version) > 100:
            raise ValueError("Expected vMAJOR.MINOR.PATCH, optionally -rc.1 (no build metadata)")
        prerelease = "-" in version
    else:
        raise ValueError("Unknown channel")
    return dict(prefix=prefix, version=version, prerelease=str(prerelease).lower(), sha=sha)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    for name in ("repository", "sha", "channel"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--tag", default="")
    parser.add_argument("--github-output")
    args = parser.parse_args()
    try:
        value = metadata(args.repository, args.sha, args.channel, args.tag)
    except ValueError as error:
        parser.error(str(error))
    if args.github_output:
        with open(args.github_output, "a", encoding="utf-8") as output:
            for key, item in value.items():
                output.write(f"{key}={item}\n")
    else:
        print(json.dumps(value, indent=2))
