/**
 * `FakePlaybackServer` over HTTP, for native exit tests on a simulator (plan §9.6; Apple C3). Test
 * only, short-lived: start it for a test session and stop it by PID.
 *
 *   node --experimental-strip-types packages/client-core/tools/fake-playback-http.ts <port> <fixtures dir> [<extra media dir>]
 *
 * Items: `fx-<name>` plays `fixtures/audio-gapless/<name>.<ext>` with its sidecar; `long-<n>` plays
 * `<extra media dir>/long-<n>.flac` (44.1 kHz; frames in `long-<n>.json`). Everything is version 2 (the client decodes).
 * `GET /v1/items/<id>` answers song facts: `fx-album-<codec>-NN` is track NN of one album, for the same-album rule.
 * `book-<n>` is part n of one audiobook (`audiobook_file`), playing `<extra media dir>/book-<n>.flac`.
 * Test controls (JSON):
 *   GET  /__test/state                       counters, sessions (state, end reason, reports), queue
 *   POST /__test/fail {match, outcomes[]}    the next requests whose path contains `match` fail:
 *                                            a status (409, 410, 503…), "offline" (connection dropped
 *                                            before the server sees it) or "lost" (the server applies
 *                                            it, then the answer is dropped)
 *   POST /__test/outage {match, until}       every request whose path contains `match` drops until `until` (ms epoch)
 */
import {createServer, type IncomingMessage, type ServerResponse} from 'node:http';
import {readFileSync, readdirSync, existsSync} from 'node:fs';
import {join} from 'node:path';
import {FakePlaybackServer, type FakeAudioSource} from '../src/playback-v1/testing/fake-server.ts';

const [portArg, fixtures, extra] = process.argv.slice(2);
const port = Number(portArg ?? 18431);

function fixtureSource(name: string): FakeAudioSource | undefined {
  const sidecar = join(fixtures ?? '', `${name}.json`);
  if (!fixtures || !existsSync(sidecar)) return undefined;
  const side = JSON.parse(readFileSync(sidecar, 'utf8'));
  const container = side.container === 'mp4' ? 'mp4' : side.container;
  return {bytes: readFileSync(join(fixtures, side.file)), container, codec: side.codec, sampleRate: side.sampleRate, channels: side.channels, durationFrames: side.durationFrames, trim: side.trim};
}
function longSource(name: string): FakeAudioSource | undefined {
  const file = join(extra ?? '', `${name}.flac`);
  if (!extra || !existsSync(file)) return undefined;
  const side = existsSync(join(extra, `${name}.json`)) ? JSON.parse(readFileSync(join(extra, `${name}.json`), 'utf8')) : {};
  return {bytes: readFileSync(file), container: 'flac', codec: 'flac', sampleRate: 44100, channels: 2, durationFrames: side.durationFrames ?? 882000, trim: {startFrames: 0, endFrames: 0, source: 'flac'}, gain: {trackDb: -3, albumDb: -2, trackPeak: 0.3, albumPeak: 0.3, source: 'tags'}};
}
const cache = new Map<string, FakeAudioSource | undefined>();
const audioSource = (itemId: string) => {
  if (!cache.has(itemId)) cache.set(itemId, itemId.startsWith('fx-') ? fixtureSource(itemId.slice(3)) : itemId.startsWith('long-') || itemId.startsWith('book-') ? longSource(itemId) : undefined);
  return cache.get(itemId);
};

const server = new FakePlaybackServer({audioSource, audioItem: id => id.startsWith('fx-') || id.startsWith('long-') || id.startsWith('book-') || id.startsWith('song'),
  itemKind: id => id.startsWith('book-') ? 'audiobook_file' : undefined});
const failures: {match: string; outcome: number | 'offline' | 'lost'}[] = [];
const outages: {match: string; until: number}[] = [];
const log: {at: number; method: string; path: string; status: number | 'offline' | 'lost'}[] = [];

/** `GET /v1/items/<id>`: the song facts the native owner reads for the same-album rule (§18.5).
 * `fx-album-<codec>-NN` is track NN on disc 1 of album `album-<codec>`; other items have no album. */
function itemView(id: string) {
  const album = /^fx-album-([a-z0-9]+)-(\d+)$/.exec(id), source = audioSource(id), book = /^book-(\d+)$/.exec(id);
  const durationMs = source ? Math.round(source.durationFrames / source.sampleRate * 1000) : undefined;
  if (book) return {id, kind: 'audiobook_file', title: `Part ${book[1]}`, ...(durationMs ? {durationMs} : {}), bookFile: {bookId: 'book-fixture', bookTitle: 'Fixture Book', partNumber: Number(book[1])}};
  return {id, kind: 'song', title: id, ...(durationMs ? {durationMs} : {}),
    song: album ? {albumId: `album-${album[1]}`, albumTitle: `Album ${album[1]}`, artist: 'Fixture', discNumber: 1, trackNumber: Number(album[2])} : {artist: 'Fixture'}};
}

function body(req: IncomingMessage): Promise<unknown> {
  return new Promise(resolve => {
    const chunks: Buffer[] = [];
    req.on('data', c => chunks.push(c));
    req.on('end', () => { const text = Buffer.concat(chunks).toString('utf8'); try { resolve(text ? JSON.parse(text) : undefined); } catch { resolve(undefined); } });
  });
}
function send(res: ServerResponse, status: number, headers: Record<string, string>, payload: unknown) {
  const binary = payload instanceof Uint8Array;
  const bytes = payload === undefined ? undefined : binary ? Buffer.from(payload) : Buffer.from(JSON.stringify(payload));
  res.writeHead(status, {...headers, ...(bytes && !binary ? {'Content-Type': 'application/json'} : {}), ...(bytes ? {'Content-Length': String(bytes.length)} : {})});
  res.end(bytes);
}

createServer(async (req, res) => {
  const path = req.url ?? '/', method = req.method ?? 'GET';
  const payload = await body(req);
  if (path === '/__test/state') {
    const sessions = [...Array(200).keys()].map(i => server.session(`s${i}`)).filter(Boolean).map(s => ({id: s!.id, itemId: s!.itemId, state: s!.state, ended: s!.ended, endReason: s!.endReason, positionMs: s!.positionMs, reports: s!.reports.map(r => ({seq: r.seq, state: r.state, positionMs: r.positionMs, rate: r.rate}))}));
    send(res, 200, {}, {preparations: server.preparations, commits: server.commits, sessions, log: log.slice(-400)});
    return;
  }
  if (path === '/__test/fail' && method === 'POST') { const p = payload as {match: string; outcomes: (number | 'offline' | 'lost')[]}; for (const o of p.outcomes) failures.push({match: p.match, outcome: o}); send(res, 204, {}, undefined); return; }
  if (path === '/__test/outage' && method === 'POST') { outages.push(payload as {match: string; until: number}); send(res, 204, {}, undefined); return; }
  if (path === '/__test/reset' && method === 'POST') { failures.length = 0; outages.length = 0; send(res, 204, {}, undefined); return; }
  const at = Date.now();
  const failure = failures.findIndex(f => path.includes(f.match));
  const outage = outages.some(o => path.includes(o.match) && at < o.until);
  if (outage || failure >= 0) {
    const outcome = outage ? 'offline' : failures.splice(failure, 1)[0]!.outcome;
    if (outcome === 'lost') {
      const headers: Record<string, string> = {};
      for (const [k, v] of Object.entries(req.headers)) if (typeof v === 'string') headers[k] = v;
      const applied = await server.handle({method, path, headers, body: payload});
      log.push({at, method, path, status: applied.status});
      req.socket.destroy();
      return;
    }
    log.push({at, method, path, status: outcome});
    if (outcome === 'offline') { req.socket.destroy(); return; }
    send(res, outcome, {}, {error: {code: outcome === 410 ? 'prepared_expired' : outcome === 503 ? 'queue_building' : outcome === 409 ? (path.includes('commit') ? 'prepared_canceled' : 'prepare_not_allowed') : 'request_failed', reason: 'replaced', retry: 'never', requestId: 'test'}});
    return;
  }
  const item = method === 'GET' ? /^\/v1\/items\/([^/?]+)$/.exec(path) : null;
  if (item) { log.push({at, method, path, status: 200}); send(res, 200, {}, itemView(decodeURIComponent(item[1]!))); return; }
  const headers: Record<string, string> = {};
  for (const [k, v] of Object.entries(req.headers)) if (typeof v === 'string') headers[k] = v;
  const r = await server.handle({method, path, headers, body: payload});
  log.push({at, method, path, status: r.status});
  send(res, r.status, r.headers as Record<string, string>, r.body);
}).listen(port, '127.0.0.1', () => { process.stdout.write(`fake playback server on http://127.0.0.1:${port} (pid ${process.pid})\n`); });
