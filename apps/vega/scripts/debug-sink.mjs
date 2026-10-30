#!/usr/bin/env node
// Receives the app's debug lines and appends them to a file.
//
//   node scripts/debug-sink.mjs [file] [port]        (defaults: vega-debug.log, 5799)
//
// In app.json set portico.debugConsole to true (or "errors") and portico.debugSink to
// "http://10.0.2.2:<port>" (the virtual device's address for this machine), and build. Some
// screens (the player) cannot be photographed on the virtual device; their trace still arrives.
import fs from 'node:fs';
import http from 'node:http';

const [file = 'vega-debug.log', port = '5799'] = process.argv.slice(2);
http.createServer((request, response) => {
  if (request.method !== 'POST') { response.writeHead(405).end(); return; }
  let body = '';
  request.on('data', chunk => { body += chunk; if (body.length > 1e6) request.destroy(); });
  request.on('end', () => { fs.appendFileSync(file, body.endsWith('\n') ? body : body + '\n'); response.writeHead(204).end(); });
}).listen(Number(port), '127.0.0.1', () => console.log(`Writing debug lines to ${file} (port ${port}).`));
