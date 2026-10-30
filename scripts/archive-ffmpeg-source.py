#!/usr/bin/env python3
"""Make a deterministic patched source tarball without host IDs or timestamps."""
import gzip
from pathlib import Path
import sys
import tarfile

source, output, epoch = Path(sys.argv[1]), Path(sys.argv[2]), int(sys.argv[3])
def normalize(info):
    info.uid = info.gid = 0
    info.uname = info.gname = ''
    info.mtime = epoch
    return info
if output.name.endswith('.gz'):
    with output.open('wb') as raw:
        with gzip.GzipFile(fileobj=raw, mode='wb', filename='', mtime=epoch) as zipped:
            with tarfile.open(fileobj=zipped, mode='w') as archive:
                archive.add(source, arcname='ffmpeg', filter=normalize)
else:
    with tarfile.open(output, 'w:xz', preset=3) as archive:
        archive.add(source, arcname='ffmpeg', filter=normalize)
