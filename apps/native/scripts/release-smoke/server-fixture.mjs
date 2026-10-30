#!/usr/bin/env node
// Boots a real Portico server headlessly and sets it up with a fresh local owner, so the
// release smoke (O-158) can sign the Android app into it through the real UI.
//
//   node server-fixture.mjs --server-binary <path> --work <dir> [--port 8320]
//   node server-fixture.mjs --stop <dir>
//
// Stdout carries only baseUrl, emulatorUrl and username. The password is written to
// <dir>/fixture.json (mode 600) and is never printed. The server is left running, detached, with
// its log in <dir>/server.log.
import {spawn} from 'node:child_process';
import http from 'node:http';
import {randomBytes, randomUUID} from 'node:crypto';
import {closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync, chmodSync} from 'node:fs';
import {join, resolve} from 'node:path';
import {parseArgs} from 'node:util';

const {values: args} = parseArgs({
  options: {
    'server-binary': {type: 'string'},
    work: {type: 'string'},
    port: {type: 'string', default: '8320'},
    stop: {type: 'string'},
  },
});

const sleep = ms => new Promise(done => setTimeout(done, ms));

function alive(pid) {
  try { process.kill(pid, 0); return true; } catch { return false; }
}

async function stop(dir) {
  const file = join(resolve(dir), 'fixture.json');
  if (!existsSync(file)) throw new Error(`no fixture.json in ${dir}`);
  const {pid} = JSON.parse(readFileSync(file, 'utf8'));
  // The server was started as a process-group leader, so a negative pid reaches its supervisor,
  // the worker it supervises, and any helper processes together.
  for (const signal of ['SIGTERM', 'SIGKILL']) {
    try { process.kill(-pid, signal); } catch { try { process.kill(pid, signal); } catch {} }
    for (let i = 0; i < 50 && alive(pid); i++) await sleep(100);
    if (!alive(pid)) break;
  }
  if (alive(pid)) throw new Error(`server ${pid} did not stop`);
  console.log(`stopped ${pid}`);
}

function api(base, method, path, body) {
  // node:http, not fetch: undici's fetch adds Sec-Fetch-Mode, and /v1/setup/app refuses any
  // request that looks like a browser's. This sends JSON and nothing else, as a native client does.
  const payload = body === undefined ? null : Buffer.from(JSON.stringify(body));
  return new Promise((done, fail) => {
    const req = http.request(new URL(path, base), {
      method,
      headers: payload ? {'content-type': 'application/json', 'content-length': payload.length} : {},
      timeout: 30000,
    }, res => {
      const chunks = [];
      res.on('data', chunk => chunks.push(chunk));
      res.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        let json = null;
        try { json = text ? JSON.parse(text) : null; } catch {}
        done({status: res.statusCode, json, text});
      });
    });
    req.on('timeout', () => req.destroy(new Error('request timed out')));
    req.on('error', fail);
    req.end(payload || undefined);
  });
}

async function start() {
  const binary = args['server-binary'] && resolve(args['server-binary']);
  if (!binary || !existsSync(binary)) throw new Error('--server-binary must name an existing server binary');
  if (!args.work) throw new Error('--work is required');
  const port = Number(args.port);
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('--port must be a TCP port');
  const work = resolve(args.work);
  if (existsSync(join(work, 'fixture.json'))) throw new Error(`${work} already holds a fixture; run --stop first and use a fresh directory`);
  mkdirSync(work, {recursive: true, mode: 0o700});
  // Fresh state every run: the data directory is created here, so the server sees a first run.
  const data = join(work, 'data');
  const home = join(work, 'home');
  rmSync(data, {recursive: true, force: true});
  mkdirSync(home, {recursive: true, mode: 0o700});

  const username = `smoke-${randomBytes(4).toString('hex')}`;
  // 24 random bytes in base64url is 32 characters, within the server's 8..72 password rule.
  const password = randomBytes(24).toString('base64url');
  const baseUrl = `http://127.0.0.1:${port}`;
  const emulatorUrl = `http://10.0.2.2:${port}`;

  const log = openSync(join(work, 'server.log'), 'a', 0o600);
  const started = Date.now();
  const child = spawn(binary, [], {
    detached: true,
    stdio: ['ignore', log, log],
    env: {
      PATH: process.env.PATH,
      // Everything (database, keys, cache, logs, temp) lives under this one directory.
      PORTICO_DATA_DIR: data,
      HOME: home,
      // 0.0.0.0 so the emulator's 10.0.2.2 alias reaches it. The server permits plain HTTP to
      // private and loopback peers on its own, so there is no TLS or insecure-HTTP switch.
      PORTICO_BIND: `0.0.0.0:${port}`,
      // No mDNS/SSDP announcements on the CI machine's network.
      PORTICO_DISCOVERY: '0',
    },
  });
  closeSync(log);
  child.unref();
  const pid = child.pid;

  const fail = async message => {
    try { process.kill(-pid, 'SIGKILL'); } catch {}
    throw new Error(`${message} (see ${join(work, 'server.log')})`);
  };

  try {
    // 2. Wait for /v1/system. While the server is still starting it answers with a progress
    // page, so wait for a JSON body that says whether setup is required.
    let system = null;
    for (let i = 0; i < 240; i++) {
      if (!alive(pid)) await fail('server exited during start');
      try {
        const r = await api(baseUrl, 'GET', '/v1/system');
        if (r.status === 200 && r.json && typeof r.json.setupRequired === 'boolean') { system = r.json; break; }
      } catch {}
      await sleep(250);
    }
    if (!system) await fail('server did not become ready in 60s');
    const coldStartMs = Date.now() - started;
    if (system.setupRequired !== true) await fail('server is not in first-run state (setupRequired is not true)');

    // 3a. Ask for the one-time setup secret exactly as the Portico app does on a server nobody
    // has set up: POST /v1/setup/app, with no Origin. (The browser path, /v1/setup/browser,
    // needs an Origin. This one refuses requests that carry one.) The server also requires the
    // caller to be a private or loopback peer; this script connects from 127.0.0.1.
    const app = await api(baseUrl, 'POST', '/v1/setup/app', {});
    if (app.status !== 200 || !app.json?.setupToken) await fail(`POST /v1/setup/app answered ${app.status}`);

    // 3b. Create the first owner with a local account (authMode "local", so no Portico Account,
    // hosted claim or browser approval is involved). This answers 201 with a session.
    const created = await api(baseUrl, 'POST', '/v1/setup', {
      authMode: 'local',
      name: 'Smoke',
      username,
      password,
      requestId: randomUUID(),
      setupToken: app.json.setupToken,
    });
    if (created.status !== 201) await fail(`POST /v1/setup answered ${created.status}: ${created.json?.error?.code ?? 'no error code'}`);

    // The browser journey's final POST /v1/setup/finish only applies to the hosted path: a
    // local setup is already ready when /v1/setup answers.
    const after = await api(baseUrl, 'GET', '/v1/system');
    if (after.json?.setupRequired !== false) await fail('setupRequired did not clear after setup');

    // 4. Prove the credentials work with the request the app makes for a local sign-in.
    const signIn = await api(baseUrl, 'POST', '/v1/direct/sign-in', {username, password});
    if (signIn.status !== 200 && signIn.status !== 201) await fail(`POST /v1/direct/sign-in answered ${signIn.status}: ${signIn.json?.error?.code ?? 'no error code'}`);
    if (signIn.json?.account?.username !== username) await fail('sign-in answered for a different account');

    // 5. Record the fixture, owner-only, then report non-secret facts.
    const file = join(work, 'fixture.json');
    writeFileSync(file, JSON.stringify({baseUrl, emulatorUrl, username, password, pid}, null, 2) + '\n', {mode: 0o600});
    chmodSync(file, 0o600);
    console.log(`baseUrl=${baseUrl}`);
    console.log(`emulatorUrl=${emulatorUrl}`);
    console.log(`username=${username}`);
    console.error(`cold start ${coldStartMs} ms`);
  } catch (error) {
    try { process.kill(-pid, 'SIGKILL'); } catch {}
    throw error;
  }
}

try {
  if (args.stop) await stop(args.stop);
  else await start();
} catch (error) {
  console.error(`server-fixture: ${error.message}`);
  process.exit(1);
}
