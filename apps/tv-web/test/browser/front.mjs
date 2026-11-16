// The built TV-web app (dist/) and the test server's API at one local address, so the checks
// sign in to that address as a person would type it and need no cross-site rule. A check can
// hold back answers whose path matches a pattern (a row's next page still loading) or fail them.
import {createReadStream, existsSync, statSync} from 'node:fs';
import http from 'node:http';
import path from 'node:path';

const types = {'.html': 'text/html; charset=utf-8', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.png': 'image/png',
  '.ttf': 'font/ttf', '.woff2': 'font/woff2', '.svg': 'image/svg+xml', '.map': 'application/json'};

export async function startFront({dist, server}) {
  const rules = [];
  const front = http.createServer((request, response) => {
    const url = new URL(request.url, 'http://front');
    if (url.pathname.startsWith('/v1')) {
      const rule = rules.find(entry => entry.pattern.test(request.method + ' ' + url.pathname + url.search));
      const forward = () => {
        if (rule?.fail) {response.writeHead(503, {'Content-Type': 'application/json', 'Retry-After': '1'}); response.end('{"error":{"code":"unavailable","message":"held by the check","retry":true}}'); return;}
        const target = new URL(server);
        const upstream = http.request({host: target.hostname, port: target.port, path: request.url, method: request.method, headers: {...request.headers, host: target.host}},
          answer => {response.writeHead(answer.statusCode, answer.headers); answer.pipe(response);});
        upstream.on('error', () => {if (!response.headersSent) response.writeHead(502); response.end();});
        request.pipe(upstream);
      };
      if (rule?.until) rule.until.then(forward); else forward();
      return;
    }
    let file = path.join(dist, decodeURIComponent(url.pathname));
    if (!file.startsWith(dist) || !existsSync(file) || statSync(file).isDirectory()) file = path.join(dist, 'index.html');
    response.writeHead(200, {'Content-Type': types[path.extname(file)] ?? 'application/octet-stream', 'Cache-Control': 'no-store'});
    createReadStream(file).pipe(response);
  });
  await new Promise(resolve => front.listen(0, '127.0.0.1', resolve));
  return {
    origin: `http://127.0.0.1:${front.address().port}`,
    /** Holds answers matching `pattern` (method and path) until the promise it returns is released, or fails them. */
    hold(pattern, {fail = false} = {}) {
      let release;
      const rule = {pattern, fail, until: fail ? undefined : new Promise(resolve => {release = resolve;})};
      rules.push(rule);
      return () => {rules.splice(rules.indexOf(rule), 1); release?.();};
    },
    stop: () => new Promise(resolve => {front.closeAllConnections(); front.close(resolve);}),
  };
}
