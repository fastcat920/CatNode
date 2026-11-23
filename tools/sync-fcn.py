#!/usr/bin/env python3
"""Keep the App's vendored FCN module identical to this canonical module."""
import argparse
from pathlib import Path
import shutil

parser = argparse.ArgumentParser()
parser.add_argument('app', type=Path, help='FastCatAPP checkout')
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
source = Path(__file__).resolve().parents[1] / 'protocol/fcn'
target = args.app.resolve() / 'core/Clash.Meta/transport/fcn'
files = {p.relative_to(source) for p in source.rglob('*') if p.is_file()}
if args.check:
    other = {p.relative_to(target) for p in target.rglob('*') if p.is_file()}
    assert files == other, 'FCN module file sets differ'
    for name in sorted(files):
        assert (source / name).read_bytes() == (target / name).read_bytes(), f'FCN module differs: {name}'
    print('FCN App and CatNode modules match')
else:
    target.mkdir(parents=True, exist_ok=True)
    shutil.copytree(source, target, dirs_exist_ok=True)
    print('FCN App module synchronized')
