#!/usr/bin/env python3
"""Prepare release metadata locally. No network writes, tags or GitHub releases."""
import argparse
import os
from pathlib import Path
import re
import subprocess


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def eligible(message):
    return not (re.search(r"\[(?:skip|no)[ -]?release\]", message, re.I)
                or re.match(r"chore: bump version to v[0-9]+\.[0-9]+\.[0-9]+", message))


def next_version(tags, message):
    versions = [tuple(map(int, m.groups())) for tag in tags
                if (m := re.fullmatch(r"v([0-9]+)\.([0-9]+)\.([0-9]+)", tag))]
    major, minor, patch = max(versions, default=(0, 0, 0))
    if re.search(r"^(?:feat|feature)(?:\(.*\))?!:|^BREAKING CHANGE:|breaking change", message, re.I | re.M):
        major, minor, patch = major + 1, 0, 0
    elif re.search(r"^(?:feat|feature)(?:\(.*\))?:", message, re.I):
        minor, patch = minor + 1, 0
    else:
        patch += 1
    return f"v{major}.{minor}.{patch}"


def emit(**values):
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as out:
        for key, value in values.items():
            out.write(f"{key}={value}\n")


def prepare(source, notes):
    if git("rev-parse", "HEAD") != source or git("status", "--porcelain", "--untracked-files=no"):
        raise ValueError("release must start at the clean, audited source commit")
    message = git("log", "-1", "--format=%B")
    if not eligible(message):
        raise ValueError("release was explicitly skipped")
    tags = git("tag", "--list").splitlines()
    version = next_version(tags, message)
    p = Path("version.go")
    updated, count = re.subn(r'^const Version = "[^"\n]+"$', f'const Version = "{version[1:]}"', p.read_text(), flags=re.M)
    if count != 1:
        raise ValueError("expected exactly one Version constant")
    p.write_text(updated)
    files = ["version.go"]
    readme = Path("README.md")
    if readme.exists():
        module = re.search(r"^module\s+(\S+)$", Path("go.mod").read_text(), re.M).group(1)
        readme.write_text(re.sub(r"go get " + re.escape(module) + r"@v[0-9]+\.[0-9]+\.[0-9]+", "go get " + module + "@" + version, readme.read_text()))
        files.append("README.md")
    subprocess.run(["git", "add", "--", *files], check=True)
    subprocess.run(["git", "commit", "-m", f"chore: bump version to {version} [skip ci]"], check=True)
    candidate = git("rev-parse", "HEAD")
    if git("rev-parse", "HEAD^") != source:
        raise ValueError("candidate is not a direct child of audited source")
    notes.write_text(f"## {version}\n\n{message}\n\nValidated source: `{source}`\nRelease candidate: `{candidate}`\n")
    emit(version=version, candidate_sha=candidate)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["eligible", "prepare"])
    parser.add_argument("--source", required=True)
    parser.add_argument("--notes", type=Path)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.source) or git("rev-parse", "HEAD") != args.source:
        parser.error("checkout must match the exact triggering SHA")
    if args.mode == "eligible":
        emit(eligible=str(eligible(git("log", "-1", "--format=%B"))).lower())
    else:
        if args.notes is None:
            parser.error("--notes is required for prepare")
        prepare(args.source, args.notes)


if __name__ == "__main__":
    main()
