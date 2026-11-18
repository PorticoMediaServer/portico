# Serves one folder over HTTP with byte ranges, as the Portico server does for media, for the mpv test in CI.
import http.server, os, re, sys
class Ranged(http.server.SimpleHTTPRequestHandler):
    def send_head(self):
        path = self.translate_path(self.path)
        if not os.path.isfile(path): return super().send_head()
        size = os.path.getsize(path); start, end = 0, size - 1
        match = re.fullmatch(r'bytes=(\d*)-(\d*)', self.headers.get('Range', ''))
        if match and match.group(1): start = int(match.group(1)); end = int(match.group(2) or end)
        f = open(path, 'rb'); f.seek(start); self.remaining = end - start + 1
        self.send_response(206 if match else 200)
        if match: self.send_header('Content-Range', f'bytes {start}-{end}/{size}')
        self.send_header('Accept-Ranges', 'bytes'); self.send_header('Content-Length', str(self.remaining)); self.end_headers()
        return f
    def copyfile(self, source, out):
        while self.remaining > 0:
            chunk = source.read(min(65536, self.remaining))
            if not chunk: break
            out.write(chunk); self.remaining -= len(chunk)
    def log_message(self, *args): pass
http.server.ThreadingHTTPServer.handle_error = lambda *_: None  # mpv drops ranges it no longer needs
os.chdir(sys.argv[1]); http.server.ThreadingHTTPServer(('127.0.0.1', int(sys.argv[2])), Ranged).serve_forever()
