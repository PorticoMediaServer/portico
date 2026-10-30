#!/usr/bin/env python3
"""Compare the real pinned CPU helper with an emulated baseline CPU.

General-job conformance only: requires separately verified model/runtime/tools.
Does not install dependencies, publish analysis or assert physical acceptance.
"""
import argparse
import hashlib
import json
import math
from pathlib import Path
import struct
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--helper", required=True, type=Path)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--qemu", required=True, type=Path)
    parser.add_argument("--arm-helper", type=Path)
    parser.add_argument("--sysroot", type=Path)
    args = parser.parse_args()
    if bool(args.arm_helper) != bool(args.sysroot):
        parser.error("--arm-helper and --sysroot must be provided together")
    root = Path(__file__).resolve().parent.parent
    lock = json.loads((root / "third_party/musicnn/model.lock.json").read_text())
    model = args.model.read_bytes()
    assert len(model) == lock["onnx"]["bytes"]
    assert hashlib.sha256(model).hexdigest() == lock["onnx"]["sha256"]
    native = [str(args.helper), "--model", str(args.model)]
    if args.arm_helper:
        emulated = [str(args.qemu), "-L", str(args.sysroot), "-cpu", "cortex-a53", str(args.arm_helper), "--model", str(args.model)]
    else:
        emulated = [str(args.qemu), "-cpu", "Nehalem", *native]
    maximum = 0.0
    for case in range(3):
        values = [0.0 if case == 0 else 2 + math.sin(index * .013 * case) + math.cos(index * .003) for index in range(187 * 96)]
        assert all(0 <= value <= 16 for value in values), "fixture must satisfy the actual log-mel input contract"
        payload = struct.pack("<" + "f" * len(values), *values)
        outputs = []
        for command in (native, emulated):
            result = subprocess.run(command, input=payload, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=180)
            if result.returncode != 0:
                raise RuntimeError(f"CPU helper exited {result.returncode}: {result.stderr.decode(errors='replace')[:2048]}")
            assert len(result.stdout) == 8 + 250 * 4 and result.stdout[:8] == b"PSNC0001"
            numeric = struct.unpack("<250f", result.stdout[8:])
            assert all(math.isfinite(value) for value in numeric)
            # PSNC0001 carries the original dense activations, not unit vectors.
            # Runtime.Run averages patches and normalizes before publication.
            energy = sum(value * value for value in numeric[:200])
            assert math.isfinite(energy) and energy > 0, (case, energy)
            assert all(0 <= value <= 1 for value in numeric[200:])
            outputs.append(numeric)
        error = max(abs(a - b) for a, b in zip(*outputs))
        assert error < 1e-4, (case, error)
        maximum = max(maximum, error)
    facts = {"cases": 3, "emulatedCPU": "Cortex-A53" if args.arm_helper else "Nehalem", "architecture": "aarch64" if args.arm_helper else "x86_64", "maximumAbsoluteError": maximum, "physicalAcceptance": False}
    if not args.arm_helper:
        facts.update(avx=False, avx2=False)
    print(json.dumps(facts))


if __name__ == "__main__":
    main()
