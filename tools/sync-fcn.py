#!/usr/bin/env python3
"""Synchronize the canonical FCN modules between CatNode and FastCatAPP."""
import argparse
from pathlib import Path
import shutil

parser = argparse.ArgumentParser()
parser.add_argument('app', type=Path, help='FastCatAPP checkout')
parser.add_argument('--check', action='store_true')
parser.add_argument('--v3', action='store_true', help='use the FCN v3 module from FastCatAPP')
args = parser.parse_args()
catnode = Path(__file__).resolve().parents[1]
app = args.app.resolve()
if args.v3:
    source, target = app / 'core/fcnv3', catnode / 'protocol/fcnv3'
else:
    source, target = catnode / 'protocol/fcn', app / 'core/Clash.Meta/transport/fcn'
if not (source / 'go.mod').is_file():
    parser.error(f'FCN module not found in {source}')
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
    print(f'FCN module synchronized to {target}')
