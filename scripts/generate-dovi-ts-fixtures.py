#!/usr/bin/env python3
"""Author bounded dual-PID TS from procedural HEVC, with explicit test clocks.

Actual decoded frame order maps each existing AU to its authored PTS. This is
fixture creation, never a runtime replacement for FFmpeg's own disc readers.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess

BL, EL, PMT = 0x1011, 0x1015, 0x100


def crc(data):
    value = 0xffffffff
    for byte in data:
        value ^= byte << 24
        for _ in range(8):
            value = ((value << 1) ^ (0x04c11db7 if value & 0x80000000 else 0)) & 0xffffffff
    return value.to_bytes(4, "big")


def section(table, body):
    length = len(body) + 4
    data = bytes([table, 0xb0 | (length >> 8), length & 255]) + body
    return data + crc(data)


def timestamp(value, tag):
    value &= (1 << 33) - 1
    return bytes([tag | ((value >> 29) & 14) | 1, (value >> 22) & 255,
                  ((value >> 14) & 254) | 1, (value >> 7) & 255, ((value << 1) & 254) | 1])


def pes(data, pts, dts):
    clock = timestamp(pts, 0x30) + timestamp(dts, 0x10) if pts is not None else b""
    return b"\0\0\1\xe0\0\0\x80" + bytes([0xc0 if clock else 0, len(clock)]) + clock + data


class Writer:
    def __init__(self, private_mode=None, hevc_descriptor=b""):
        self.continuity, self.packets = {}, []
        self.private_mode, self.hevc_descriptor = private_mode, hevc_descriptor

    def packetize(self, pid, data, pcr=None):
        first = True
        while data:
            needs_pcr = first and pcr is not None
            length = min(len(data), 176 if needs_pcr else 184)
            adaptation = b""
            if needs_pcr or length < 184:
                size = 183 - length
                adaptation = bytes([size])
                if size:
                    adaptation += bytes([0x10 if needs_pcr else 0])
                    if needs_pcr:
                        adaptation += ((pcr << 15) | (0x3f << 9)).to_bytes(6, "big")
                    adaptation += b"\xff" * (size + 1 - len(adaptation))
            count = self.continuity.get(pid, 0)
            header = bytes([0x47, (0x40 if first else 0) | (pid >> 8), pid & 255,
                            (0x30 if adaptation else 0x10) | count])
            self.continuity[pid] = (count + 1) & 15
            packet = header + adaptation + data[:length]
            if len(packet) != 188:
                raise ValueError("bad TS packet size")
            self.packets.append(packet)
            data, first = data[length:], False

    def tables(self, dependency=BL, version=0):
        header = b"\0\1" + bytes([0xc1 | (version << 1)]) + b"\0\0"
        self.packetize(0, b"\0" + section(0, header + b"\0\1" + (0xe000 | PMT).to_bytes(2, "big")))
        entries = bytearray()
        for pid in (BL, EL):
            descriptors = self.hevc_descriptor if self.private_mode != "no-hevc" else b""
            if pid == EL and dependency is not None:
                descriptors += ((b"\x05\x04DOVI" if self.private_mode != "no-registration" else b"") + b"\xb0\x07\x01\0" +
                               ((7 << 9) | (1 << 3) | 6).to_bytes(2, "big") +
                               (dependency << 3).to_bytes(2, "big") + b"\x60")
            stream_type = 0x06 if pid == EL and self.private_mode else 0x24
            entries += bytes([stream_type]) + (0xe000 | pid).to_bytes(2, "big") + (0xf000 | len(descriptors)).to_bytes(2, "big") + descriptors
        self.packetize(PMT, b"\0" + section(2, header + (0xe000 | BL).to_bytes(2, "big") + b"\xf0\0" + entries))

    def bytes(self):
        return b"".join(self.packets)


def access_units(probe, path):
    if path.stat().st_size > 1048576:
        raise ValueError("input exceeds 1MiB fixture bound")
    p = subprocess.run([str(probe), "-v", "error", "-show_packets", "-show_frames", "-of", "json", str(path)],
                       capture_output=True, check=True, timeout=30)
    rows = json.loads(p.stdout)["packets_and_frames"]
    packets = [r for r in rows if r["type"] == "packet"]
    frames = [r for r in rows if r["type"] == "frame"]
    if len(frames) != 24 or len(packets) != 24:
        raise ValueError("expected exactly 24 actual AUs/decoded frames")
    by_pos = {int(f["pkt_pos"]): i for i, f in enumerate(frames)}
    if len(by_pos) != 24:
        raise ValueError("ambiguous decoded frame mapping")
    raw = path.read_bytes()
    units = [(raw[int(p["pos"]):int(p["pos"]) + int(p["size"])], by_pos[int(p["pos"])]) for p in packets]
    if b"".join(u[0] for u in units) != raw:
        raise ValueError("probe AU ranges do not cover original bytes exactly")
    return units


def hevc_descriptor(path):
    """Copy the fixture's actual VPS general PTL into its video descriptor."""
    raw = path.read_bytes()
    starts = list(re.finditer(b"\x00\x00\x00\x01|\x00\x00\x01", raw))
    for i, match in enumerate(starts):
        nal = raw[match.end():starts[i + 1].start() if i + 1 < len(starts) else len(raw)]
        if (nal[0] >> 1) & 63 != 32:
            continue
        rbsp = re.sub(b"\x00\x00\x03", b"\x00\x00", nal[2:])
        if len(rbsp) < 16 or rbsp[2:4] != b"\xff\xff" or not rbsp[15]:
            raise ValueError("fixture VPS general profile/tier/level unavailable")
        return b"\x38\x0d" + rbsp[4:16] + b"\x0f"
    raise ValueError("fixture has no VPS")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ffprobe", type=Path, required=True)
    parser.add_argument("--fixtures", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    if any(args.output.iterdir()):
        raise ValueError("output must be empty")
    base = access_units(args.ffprobe, args.fixtures / "base-pq.hevc")
    results = []
    variants = [("mel-vfr", "MEL", "normal"), ("fel-vfr", "FEL", "normal"),
                ("el-first", "MEL", "el-first"), ("split-pes", "FEL", "split"),
                ("pts-mismatch", "MEL", "pts"), ("dts-mismatch", "FEL", "dts"),
                ("missing-last-el", "MEL", "missing"), ("poc-mismatch", "FEL", "poc"),
                ("wrong-dependency", "MEL", "dependency"), ("no-descriptor", "FEL", "no-descriptor"),
                ("corrupt-rpu", "FEL", "crc"), ("wrong-rpu-profile", "MEL", "profile"),
                ("missing-clock", "FEL", "clock"), ("removed-descriptor", "MEL", "remove"),
                ("pmt-version-only", "FEL", "version"), ("queue-bound", "MEL", "queue"),
                ("m2ts-fel", "FEL", "m2ts"), ("generic-private", "FEL", "private"),
                ("private-missing-hevc", "FEL", "no-hevc"),
                ("private-missing-registration", "MEL", "no-registration")]
    replacement_data = (args.fixtures / "dv-8.1.hevc").read_bytes()
    replacement_starts = list(re.finditer(b"\x00\x00\x00\x01|\x00\x00\x01", replacement_data))
    replacements = [replacement_data[m.end():replacement_starts[i + 1].start() if i + 1 < len(replacement_starts) else len(replacement_data)]
                    for i, m in enumerate(replacement_starts) if (replacement_data[m.end()] >> 1) & 63 == 62]
    if len(replacements) != 24:
        raise ValueError("expected 24 real profile8 RPU NALs")
    for name, layer, mode in variants:
        enhancement = access_units(args.ffprobe, args.fixtures / f"enhancement-{layer}.hevc")
        if [p for _, p in base] != [p for _, p in enhancement]:
            raise ValueError("actual BL/EL coded picture order differs")
        private = mode if mode in ("private", "no-hevc", "no-registration") else None
        writer = Writer(private, hevc_descriptor(args.fixtures / f"enhancement-{layer}.hevc") if private else b"")
        dependency = None if mode == "no-descriptor" else BL + 1 if mode == "dependency" else BL
        writer.tables(dependency)
        clocks = []
        if mode == "queue":
            for index in range(34):
                writer.packetize(EL, pes(enhancement[index % 24][0], 180000 + index * 3750, 168750 + index * 3750))
            data = writer.bytes()
            path = args.output / (name + ".ts")
            path.write_bytes(data)
            results.append({"path": path.name, "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                            "expected": "refused", "layer": layer, "sourcePacketClocks": []})
            continue
        for index, ((bl, display), (el, _)) in enumerate(zip(base, enhancement)):
            pts = 180000 + display * 3750 + (750 if display >= 12 else 0)
            dts = 168750 + index * 3750 + (750 if index >= 12 else 0)
            clocks.append({"pts": pts, "dts": dts, "displayIndex": display})
            if index % 6 == 0:
                writer.tables(None if mode == "remove" and index >= 12 else dependency,
                              1 if mode == "version" and index >= 12 else 0)
            if mode == "poc" and index in (2, 3):
                el = enhancement[5 - index][0]
            if mode in ("crc", "profile") and index == 5:
                starts = list(re.finditer(b"\x00\x00\x00\x01|\x00\x00\x01", el))
                modified = []
                for i, m in enumerate(starts):
                    nal = el[m.end():starts[i + 1].start() if i + 1 < len(starts) else len(el)]
                    if (nal[0] >> 1) & 63 == 62:
                        if mode == "profile":
                            nal = replacements[5]
                        else:
                            nal = nal[:-3] + bytes([nal[-3] ^ 0x40]) + nal[-2:]
                    modified.append(b"\0\0\0\1" + nal)
                el = b"".join(modified)
            streams = [(BL, bl), (EL, el)]
            if mode == "el-first":
                streams.reverse()
            for pid, data in streams:
                if mode == "missing" and pid == EL and index == 23:
                    continue
                actual_pts = pts + (1 if pid == EL and mode == "pts" and index == 12 else 0)
                actual_dts = dts + (1 if pid == EL and mode == "dts" and index == 12 else 0)
                if mode == "clock" and pid == EL and index == 10:
                    actual_pts = actual_dts = None
                if mode == "split":
                    cut = len(data) // 2
                    writer.packetize(pid, pes(data[:cut], actual_pts, actual_dts), dts if pid == BL else None)
                    writer.packetize(pid, pes(data[cut:], None, None))
                else:
                    writer.packetize(pid, pes(data, actual_pts, actual_dts), dts if pid == BL else None)
        data = writer.bytes()
        if mode == "m2ts":
            # Authored arrival clock, not measured provider or display timing.
            data = b"".join(((i * 9000) & 0x3fffffff).to_bytes(4, "big") + data[i * 188:(i + 1) * 188]
                            for i in range(len(data) // 188))
        if len(data) > 1048576:
            raise ValueError("fixture exceeds 1MiB")
        path = args.output / (name + (".m2ts" if mode == "m2ts" else ".ts"))
        path.write_bytes(data)
        results.append({"path": path.name, "sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data),
                        "expected": "joined24" if mode in ("normal", "el-first", "split", "version", "m2ts", "private") else
                                    "unjoined" if mode == "no-descriptor" else "refused",
                        "layer": layer, "sourcePacketClocks": clocks})
    version = subprocess.check_output([str(args.ffprobe), "-version"], timeout=10, text=True).splitlines()[0]
    manifest = {"schema": 1, "license": "GPL-3.0-only", "source": "procedural HDR fixtures; authored test TS/PMT/PES clocks, no real licensed media",
                "scope": "structural dual-PID transport torture inputs; no mastered FEL residual or target claim",
                "sourceManifestSha256": hashlib.sha256((args.fixtures / "manifest.json").read_bytes()).hexdigest(),
                "generatorSha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                "ffprobe": {"version": version, "sha256": hashlib.sha256(args.ffprobe.read_bytes()).hexdigest()},
                "basePid": BL, "enhancementPid": EL, "pmtPid": PMT, "fixtures": results}
    (args.output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(json.dumps({"generated": len(results), "bytes": sum(r["bytes"] for r in results)}))


if __name__ == "__main__":
    main()
