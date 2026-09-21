#!/usr/bin/env python3
"""Reject missing, skipped, failed, cancelled or wrong-commit quality jobs."""
import json
import os
import re
import sys


def require_success(needs, source_sha):
    if not isinstance(source_sha, str) or not re.fullmatch(r"[0-9a-f]{40}", source_sha):
        raise ValueError("invalid source SHA")
    if not isinstance(needs, dict) or set(needs) != {"quality", "security"}:
        raise ValueError("both quality and security results are required")
    for name, job in needs.items():
        if not isinstance(job, dict) or job.get("result") != "success":
            raise ValueError(f"{name} did not succeed")
        outputs = job.get("outputs", {})
        if not isinstance(outputs, dict) or outputs.get("audited_sha") != source_sha:
            raise ValueError(f"{name} did not validate this exact commit")
    return source_sha


def main():
    try:
        sha = require_success(json.loads(os.environ["NEEDS_JSON"]), os.environ["GITHUB_SHA"])
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as out:
            out.write(f"audited_sha={sha}\n")
        print(f"Strict quality gate passed for {sha}")
    except (KeyError, ValueError, OSError) as exc:
        print(f"Strict quality gate BLOCKED: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
