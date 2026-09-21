#!/usr/bin/env python3
"""Require exact 100% Go statement coverage; merge cross-package duplicates."""
import json
import re
import sys
from pathlib import Path


def summarize(text: str) -> dict:
    lines = text.splitlines()
    if not lines or lines[0] not in ('mode: set', 'mode: count', 'mode: atomic'):
        raise ValueError('missing or invalid coverage mode')
    blocks = {}
    for line in lines[1:]:
        match = re.fullmatch(r'(.+:\d+\.\d+,\d+\.\d+) (\d+) (\d+)', line)
        if not match:
            raise ValueError(f'invalid profile line: {line!r}')
        key, n, count = match.group(1), int(match.group(2)), int(match.group(3))
        previous_n, previous_count = blocks.get(key, (n, 0))
        if previous_n != n:
            raise ValueError(f'inconsistent statement count for {key}')
        blocks[key] = (n, previous_count + count)
    total = sum(n for n, _ in blocks.values())
    if not total:
        raise ValueError('empty coverage profile')
    missing = {key: n for key, (n, count) in blocks.items() if n and not count}
    covered = total - sum(missing.values())
    return {'covered_statements': covered, 'total_statements': total,
            'percent': 100 * covered / total, 'uncovered_blocks': missing,
            'passed': not missing}


if __name__ == '__main__':
    if len(sys.argv) != 2:
        sys.exit('usage: check_coverage.py coverage.out')
    try:
        result = summarize(Path(sys.argv[1]).read_text(encoding='utf-8'))
    except (OSError, ValueError) as exc:
        sys.exit(str(exc))
    print(json.dumps(result, indent=2, sort_keys=True))
    sys.exit(0 if result['passed'] else 1)
