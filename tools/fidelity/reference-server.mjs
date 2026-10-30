import {createServer} from 'node:http';
import {readFile, realpath, stat} from 'node:fs/promises';
import {extname, resolve, sep} from 'node:path';
import {once} from 'node:events';
import {fileURLToPath} from 'node:url';

const types = {'.html': 'text/html; charset=utf-8', '.css': 'text/css; charset=utf-8', '.js': 'text/javascript; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8', '.json': 'application/json', '.svg': 'image/svg+xml', '.png': 'image/png',
  '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp', '.avif': 'image/avif', '.gif': 'image/gif',
  '.woff': 'font/woff', '.woff2': 'font/woff2', '.ttf': 'font/ttf'};

/** Reference inputs remain external to the public repo and are only read over loopback. */
export async function serveReference({root, port = 0}) {
  const approvedRoot = await realpath(root);
  const server = createServer(async (request, response) => {
    if (request.method !== 'GET' && request.method !== 'HEAD') {response.writeHead(405).end(); return;}
    try {
      const path = decodeURIComponent(new URL(request.url, 'http://127.0.0.1').pathname);
      if (path.includes('\\') || path.split('/').some(part => part.startsWith('.'))) throw new Error('Path refused');
      const requested = resolve(approvedRoot, '.' + path), actual = await realpath(requested);
      if (!actual.startsWith(approvedRoot + sep) || !types[extname(actual).toLowerCase()]) throw new Error('Path refused');
      const info = await stat(actual);
      if (!info.isFile() || info.size > 32 * 1024 * 1024) throw new Error('File refused');
      response.writeHead(200, {'Content-Type': types[extname(actual).toLowerCase()], 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff'});
      response.end(request.method === 'HEAD' ? undefined : await readFile(actual));
    } catch {response.writeHead(404).end();}
  });
  server.listen(port, '127.0.0.1'); await once(server, 'listening');
  return {origin: `http://127.0.0.1:${server.address().port}`, async close() {server.closeAllConnections(); await new Promise(resolve => server.close(resolve));}};
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = process.argv[2], port = Number(process.argv[3]);
  if (!root || !Number.isInteger(port) || port < 1024 || port > 65535) throw new Error('Usage: reference-server.mjs EXTERNAL_ROOT LOOPBACK_PORT');
  const server = await serveReference({root, port});
  console.log(JSON.stringify({referenceOrigin: server.origin}));
  for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, async () => {await server.close(); process.exit(0);});
}
