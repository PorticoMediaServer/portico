"""Generate a freely authored 320x180 PGS composition and clear. No licensed samples."""
from pathlib import Path
import struct
import sys

out = bytearray()
def segment(ms, kind, body):
    out.extend(b"PG" + struct.pack(">IIBH", ms * 90, 0, kind, len(body)) + body)
def pcs(ms, number, objects):
    body = struct.pack(">HHBHBBBB", 320, 180, 0x10, number, 0x80 if number == 0 else 0, 0, 0, len(objects))
    for x, y in objects:
        body += struct.pack(">HBBHH", 0, 0, 0, x, y)
    segment(ms, 0x16, body)
pcs(1000, 0, [(10, 20)])
segment(1000, 0x17, struct.pack(">BBHHHH", 1, 0, 0, 0, 320, 180))
segment(1000, 0x14, bytes([0, 0, 1, 81, 240, 90, 255]))
rle = bytes([1, 1, 0, 0, 1, 1, 0, 0])
segment(1000, 0x15, struct.pack(">HBB", 0, 0, 0xC0) + (len(rle) + 4).to_bytes(3, "big") + struct.pack(">HH", 2, 2) + rle)
segment(1000, 0x80, b"")
pcs(2000, 1, [])
segment(2000, 0x80, b"")
Path(sys.argv[1]).write_bytes(out)
