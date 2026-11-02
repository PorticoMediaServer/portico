"""A disposable loopback HLS channel and XMLTV schedule for E2E walks."""
from contextlib import AbstractContextManager
from datetime import datetime, timedelta, timezone
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import secrets
import subprocess
import tempfile
import threading
import time


class _QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


class SyntheticLive(AbstractContextManager):
    """Publish one private synthetic channel; keep its source alive for the run."""

    def __init__(self, api, media):
        self.api = api
        self.media = Path(media)
        self.tmp = None
        self.http = None
        self.thread = None
        self.encoder = None

    def __enter__(self):
        try:
            self.tmp = tempfile.TemporaryDirectory(prefix='portico-e2e-live-')
            root = Path(self.tmp.name)
            video = self.media / 'Movies/Arrival (2016)/Arrival (2016) - 1080p.mp4'
            if not video.is_file():
                raise RuntimeError('synthetic live source video is missing')
            self.http = ThreadingHTTPServer(('127.0.0.1', 0), partial(_QuietHandler, directory=str(root)))
            self.thread = threading.Thread(target=self.http.serve_forever, daemon=True)
            self.thread.start()
            origin = f'http://127.0.0.1:{self.http.server_port}'
            self.encoder = subprocess.Popen([
                'ffmpeg', '-hide_banner', '-loglevel', 'error', '-re', '-stream_loop', '-1',
                '-i', str(video), '-map', '0:v:0', '-map', '0:a:0?', '-c', 'copy',
                '-f', 'hls', '-hls_time', '2', '-hls_list_size', '5',
                '-hls_flags', 'delete_segments+independent_segments', str(root / 'channel.m3u8'),
            ], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline:
                if self.encoder.poll() is not None:
                    raise RuntimeError('synthetic live encoder exited')
                playlist = root / 'channel.m3u8'
                if playlist.exists() and '#EXTINF:' in playlist.read_text():
                    break
                time.sleep(.2)
            else:
                raise RuntimeError('synthetic live HLS did not become ready')
            now = datetime.now(timezone.utc).replace(minute=0, second=0, microsecond=0)
            stamp = lambda value: value.strftime('%Y%m%d%H%M%S +0000')
            programmes = ''.join(
                f'<programme channel="portico-test" start="{stamp(now + timedelta(hours=i))}" '
                f'stop="{stamp(now + timedelta(hours=i + 1))}"><title>Synthetic programme {i + 2}</title></programme>'
                for i in range(-1, 4))
            guide = '<tv><channel id="portico-test"><display-name>Synthetic TV</display-name></channel>' + programmes + '</tv>'
            source = f'#EXTM3U\n#EXTINF:-1 tvg-id="portico-test" tvg-chno="1",Synthetic TV\n{origin}/channel.m3u8\n'
            (root / 'channels.m3u').write_text(source)
            (root / 'guide.xml').write_text(guide)
            # Use the real owner preview/save flow: it explicitly authorizes the
            # loopback origin for subsequent playlist and segment requests.
            preview = self.api.post('/v1/admin/live-sources/remote/preview', {
                'id': secrets.token_hex(24), 'expectedRevision': 0,
                'name': 'Synthetic Live TV', 'kind': 'm3u',
                'locator': f'{origin}/channels.m3u', 'guideUrl': f'{origin}/guide.xml',
                'username': '', 'password': '', 'confirmedLanRoots': [f'{origin}/'],
                'mappings': [], 'refreshMs': 3600000, 'ownerLimit': 1,
                'useDiscoveredCapacity': False, 'viewerAccess': 'server-members',
            })
            self.api.post('/v1/admin/live-sources/remote/save', {
                'previewId': preview['remotePreview']['previewId'], 'requestId': secrets.token_hex(24),
            })
            print('Synthetic live source ready', flush=True)
            return self
        except BaseException:
            self.__exit__(None, None, None)
            raise

    def __exit__(self, *_):
        if self.encoder is not None:
            self.encoder.terminate()
            try:
                self.encoder.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.encoder.kill()
                self.encoder.wait()
        if self.http is not None:
            self.http.shutdown()
            self.http.server_close()
        if self.thread is not None:
            self.thread.join(timeout=5)
        if self.tmp is not None:
            self.tmp.cleanup()
