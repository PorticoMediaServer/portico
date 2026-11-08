"""Bounded gzip observer outside the measured engine cgroup; no SQLite."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import threading
import time
import zlib

import qual_fixture as q

MEMORY_BYTES = 768 << 20
COMPRESSED_BYTES = MEMORY_BYTES - (1 << 20)  # leave room for socket/receipts
RAW_BYTES = 16 << 30


def cgroup(pid='self'):
    values = [s.split(':', 2)[2] for s in Path(f'/proc/{pid}/cgroup').read_text().splitlines() if s.startswith('0::')]
    q.require(len(values) == 1 and values[0].startswith('/') and '..' not in Path(values[0]).parts,
              'observer/engine cgroup evidence missing')
    return values[0]


class CappedWriter:
    def __init__(self, file, limit):
        self.file, self.limit, self.size = file, limit, 0

    def write(self, data):
        q.require(self.size + len(data) <= self.limit, 'compressed observer spool cap exceeded; cell failed, prefix retained')
        n = self.file.write(data)
        self.size += n
        q.require(n == len(data), 'short compressed observer write')
        return n

    def flush(self):
        self.file.flush()


def decoded_digest(path):
    digest, size = hashlib.sha256(), 0
    with gzip.open(path, 'rb') as source:
        for block in iter(lambda: source.read(1 << 20), b''):
            size += len(block)
            q.require(size <= RAW_BYTES, 'decompressed raw bound exceeded')
            digest.update(block)
    return size, digest.hexdigest()


def capture(connection, root, limit=COMPRESSED_BYTES):
    """EOF -> close gzip -> independent round-trip -> completion receipt."""
    raw = root/'raw.ndjson.gz'
    result = dict(complete=False, compressed_limit_bytes=limit, raw_limit_bytes=RAW_BYTES,
                  method='external-gzip-tmpfs-v1', observer_pid=os.getpid(), observer_cgroup=cgroup(), accounting='external observer; excluded from EC5 engine accounting')
    digest, size = hashlib.sha256(), 0
    cpu_started, wall_started = time.process_time_ns(), time.monotonic_ns()
    try:
        with raw.open('xb') as file:
            capped = CappedWriter(file, limit)
            compressor = zlib.compressobj(level=1, wbits=31)  # gzip framing/trailer
            while block := connection.recv(1 << 16):
                size += len(block)
                q.require(size <= RAW_BYTES, 'raw observer stream cap exceeded')
                digest.update(block)
                capped.write(compressor.compress(block))
            capped.write(compressor.flush())  # closes gzip including CRC/length
            file.flush()  # memory only; trailer is closed before verification
        restored_size, restored_digest = decoded_digest(raw)
        q.require((size, digest.hexdigest()) == (restored_size, restored_digest), 'observer gzip round-trip mismatch')
        result.update(complete=True, verified_roundtrip=True)
    except (OSError, ValueError, EOFError) as error:
        result['error'] = f'{type(error).__name__}: {error}'
    finally:
        connection.close()  # broken pipe makes any later producer write fail
        result.update(original_bytes=size, original_sha256=digest.hexdigest(),
                      stored_bytes=raw.stat().st_size if raw.exists() else 0,
                      peak_compressed_spool_bytes=raw.stat().st_size if raw.exists() else 0,
                      stored_sha256=q.file_hash(raw) if raw.exists() else None,
                      completed_monotonic_ns=time.monotonic_ns(),
                      observer_cpu_ns=time.process_time_ns()-cpu_started,
                      observer_wall_ns=time.monotonic_ns()-wall_started)
        q.atomic_json(root/'capture.json', result)
    return result


def observe(root, invoke):
    """Run the engine while this caller owns all compression/shmem allocations."""
    path = root/'raw.sock'
    stop = threading.Event()
    listener = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    listener.bind(str(path)); listener.listen(1); listener.settimeout(1)
    def receive():
        try:
            while True:
                try:
                    connection, _ = listener.accept()
                    connection.settimeout(80*60)
                    connection.setsockopt(socket.SOL_SOCKET,socket.SO_RCVBUF,1<<16)
                    capture(connection, root)
                    return
                except socket.timeout:
                    if stop.is_set(): return
        finally:
            listener.close()
    thread = threading.Thread(target=receive, daemon=True)
    thread.start()
    try:
        code = invoke()
    finally:
        stop.set()
        thread.join(timeout=180)
        q.require(not thread.is_alive(), 'observer did not join; retain memory spool')
        path.unlink()
    q.require((root/'capture.json').is_file(), 'producer never connected to gzip observer')
    return code


def wait_capture(root):
    deadline = time.monotonic() + 180
    while not (root/'capture.json').exists():
        q.require(time.monotonic() < deadline, 'gzip observer failed to close/join before engine retirement')
        time.sleep(0.1)
    result = json.loads((root/'capture.json').read_text(), object_pairs_hook=q.no_duplicates)
    engine, observer = cgroup(), cgroup(result['observer_pid'])
    q.require(observer == result['observer_cgroup'] and observer != engine and not observer.startswith(engine.rstrip('/')+'/'),
              'observer must be outside engine cgroup')
    q.require(result['complete'] is True and result.get('verified_roundtrip') is True, result.get('error', 'gzip observer incomplete'))
    result['engine_cgroup'] = engine
    return result


def engine_memory_peak():
    path=Path('/sys/fs/cgroup')/cgroup().lstrip('/')/'memory.peak'
    value=path.read_text().strip()
    q.require(value.isdecimal(), 'engine memory.peak malformed')
    return int(value)


def timeline(root, stop):
    """External scoped raw counters, actual page units and missing values; no zeros.

    The gzip parts roll at a bounded size so a long checkpoint run never hits a
    single-file cap; every part is closed and independently round-trip verified.
    """
    result=dict(complete=False,origin_utc_ns=time.time_ns(),origin_monotonic_ns=time.monotonic_ns(),interval_requested_ns=10000000,page_bytes=os.sysconf('SC_PAGE_SIZE'),scope='host plus actual engine cgroup; observer outside engine',parts=[])
    samples=0;part=0
    try:
        while True:
            path=root/f'timeline-{part:03d}.jsonl.gz'
            part_samples=0;rotate=False
            with path.open('xb') as file:
                capped=CappedWriter(file,128<<20)
                compressor=zlib.compressobj(level=1,wbits=31)
                while True:
                    if capped.size>=(128<<20)-(1<<20):
                        rotate=True;break
                    before=time.monotonic_ns();values={};unavailable={}
                    sources={name:Path('/proc')/name for name in ('vmstat','meminfo','pressure/io')}
                    engine=root/'engine-path.json'
                    if engine.exists():
                        group=json.loads(engine.read_text())['path']
                        q.require(group.startswith('/') and group!='/' and '..' not in Path(group).parts,'invalid engine timeline scope')
                        result['engine_cgroup']=group
                        for name in ('memory.stat','io.stat','io.pressure'):
                            sources['cgroup/'+name]=Path('/sys/fs/cgroup')/group.lstrip('/')/name
                    else: unavailable['engine_cgroup']='not yet observed'
                    for name,source in sources.items():
                        try:values[name]=source.read_text()
                        except OSError as error:values[name]=None;unavailable[name]=str(error)
                    row=dict(started_utc_ns=time.time_ns(),started_monotonic_ns=before,returned_monotonic_ns=time.monotonic_ns(),raw=values,unavailable_reasons=unavailable)
                    capped.write(compressor.compress((json.dumps(row,separators=(',',':'))+'\n').encode()))
                    samples+=1;part_samples+=1
                    if stop.wait(.01):break
                capped.write(compressor.flush());file.flush()
            decoded_digest(path)
            result['parts'].append(dict(path=path.name,samples=part_samples,compressed_bytes=path.stat().st_size if path.exists() else 0))
            if not rotate:break
            part+=1
            q.require(part<64,'timeline part bound exceeded')
        result['complete']=True
    except (OSError,ValueError,EOFError) as error:result['error']=str(error)
    finally:
        result.update(samples=samples,compressed_bytes=sum(p['compressed_bytes'] for p in result['parts']))
        q.atomic_json(root/'timeline.json',result)


def run(root, cwd, argv, timeout=3300):
    """Own the external gzip observer, then launch the engine against raw.sock.

    The engine writes records to the socket only: the caller owns every
    compression allocation, and no plain stream file is created or fsynced on
    the measured device. The engine exit code is returned; the observer's
    round-trip proof is required before the cell can be treated as complete.
    """
    root, cwd = Path(root), Path(cwd)
    q.require(root.is_absolute() and root == root.resolve() and cwd.is_dir(), 'absolute observer root and real engine cwd required')
    root.mkdir(parents=True, exist_ok=True)
    q.require(not (root/'capture.json').exists(), 'fresh observer root required')
    if argv and argv[0] == '--':
        argv = argv[1:]
    q.require(len(argv) > 0, 'engine argv required')
    env = dict(os.environ, PORTICO_CANONICAL_HELD_STREAM='unix-gzip-v1')
    def invoke():
        return subprocess.run(argv, cwd=cwd, env=env, check=False, timeout=timeout).returncode
    code = observe(root, invoke)
    capture = json.loads((root/'capture.json').read_text(), object_pairs_hook=q.no_duplicates)
    q.require(capture.get('complete') is True and capture.get('verified_roundtrip') is True,
              'observer capture incomplete: ' + str(capture.get('error')))
    capture['engine_exit_code'] = code
    q.atomic_json(root/'capture.json', capture)
    print(json.dumps(dict(method=capture['method'], engine_exit_code=code,
                          original_bytes=capture['original_bytes'],
                          original_sha256=capture['original_sha256'],
                          stored_bytes=capture['stored_bytes'],
                          observer_cgroup=capture['observer_cgroup'])))
    return code


def main(argv=None):
    parser = argparse.ArgumentParser(description='Bounded external gzip observer for EC2/EC3 raw streams')
    commands = parser.add_subparsers(dest='command', required=True)
    launch = commands.add_parser('run', help='observe one engine run that streams to raw.sock')
    launch.add_argument('--root', required=True, help='output root; receives raw.sock, raw.ndjson.gz and capture.json')
    launch.add_argument('--cwd', required=True, help='engine working directory')
    launch.add_argument('--timeout', type=int, default=3300, help='engine wall limit in seconds')
    launch.add_argument('engine', nargs=argparse.REMAINDER, help='engine argv after --')
    args = parser.parse_args(argv)
    if args.command == 'run':
        return run(args.root, args.cwd, args.engine, args.timeout)
    parser.error('unknown command')


if __name__ == '__main__':
    sys.exit(main())
