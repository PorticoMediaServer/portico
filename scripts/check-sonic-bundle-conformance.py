#!/usr/bin/env python3
"""Run the packaged native helper against original-checkpoint reference outputs."""
import argparse
import json
import math
from pathlib import Path
import runpy
import struct
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bundle', type=Path, required=True)
    parser.add_argument('--reference', type=Path, required=True)
    parser.add_argument('--target', choices=['linux-x64', 'linux-arm64'], required=True)
    args = parser.parse_args()
    bundle = runpy.run_path(str(Path(__file__).with_name('sonic-bundle.py')))
    facts = bundle['verify'](args.bundle, args.target)
    reference = json.loads(args.reference.read_text())
    if (reference.get('modelSHA256') != facts['model']['sha256']
            or reference.get('reference') != 'original-tensorflow-checkpoint'
            or len(reference.get('cases', [])) != 3):
        raise ValueError('unbound original model reference')
    maximum = 0.0
    for case, expected in enumerate(reference['cases']):
        if len(expected) != 250 or not all(type(v) is float and math.isfinite(v) for v in expected):
            raise ValueError('invalid original model outputs')
        values = [0.0 if case == 0 else 2 + math.sin(i * .013 * case) + math.cos(i * .003) for i in range(187 * 96)]
        result = subprocess.run([str(args.bundle / 'portico-sonic'), '--model', str(args.bundle / 'musicnn-mtt/model.onnx')],
                                input=struct.pack('<17952f', *values), capture_output=True, timeout=180)
        if result.returncode or len(result.stdout) != 1008 or result.stdout[:8] != b'PSNC0001':
            raise ValueError('packaged helper did not complete its bounded numeric protocol')
        actual = struct.unpack('<250f', result.stdout[8:])
        if not all(math.isfinite(v) for v in actual) or not all(0 <= v <= 1 for v in actual[200:]):
            raise ValueError('invalid packaged helper output')
        error = max(abs(a - b) for a, b in zip(actual, expected))
        if error >= 1e-4:
            raise ValueError('native output differs from original checkpoint')
        maximum = max(maximum, error)
    print(json.dumps({'target': args.target, 'cases': 3, 'maximumAbsoluteError': maximum, 'nativeConformance': True}))


if __name__ == '__main__':
    main()
