#!/usr/bin/env python3
"""Reject app bundles whose Mach-O dependencies require a newer macOS."""
import json
import plistlib
import re
import subprocess
import sys
from pathlib import Path

app = Path(sys.argv[1]).resolve()
info = plistlib.loads((app / 'Contents/Info.plist').read_bytes())
declared = info['LSMinimumSystemVersion']
def version(text):
    parts = [int(x) for x in text.split('.')]
    return tuple((parts + [0, 0, 0])[:3])
rows = []
for item in sorted((app / 'Contents/Frameworks').glob('*.dylib')) + sorted((app / 'Contents/MacOS').iterdir()):
    if not item.is_file():
        continue
    result = subprocess.run(['vtool', '-show-build', str(item)], capture_output=True, text=True, check=True)
    minimums = re.findall(r'^\s*minos\s+([0-9.]+)', result.stdout, re.MULTILINE)
    if not minimums:
        raise SystemExit(f'Missing macOS build metadata: {item.name}')
    required = max(minimums, key=version)
    rows.append({'file': str(item.relative_to(app)), 'minimum_macos': required})
    if version(required) > version(declared):
        raise SystemExit(f'{item.name} requires macOS {required}; app incorrectly declares {declared}')
print(json.dumps({'declared_minimum_macos': declared, 'binaries': rows}, indent=2))
