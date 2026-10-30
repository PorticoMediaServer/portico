#!/usr/bin/env node
// Records the O-159 compatibility corpus from a running Portico server: the critical reads, scrubbed
// at record time (see scrub.mjs), one JSON file per endpoint.
//
//   node scripts/compat-corpus/record.mjs --base http://127.0.0.1:8371 --username NAME \
//        --password-file PATH --out packages/client-core/test/compat-corpus/<release>
//
// The server should already have one library with a few items (the synthetic music set is enough).
import {mkdirSync, readFileSync, rmSync, writeFileSync} from 'node:fs';
import {join, resolve} from 'node:path';
import {parseArgs} from 'node:util';
import {randomUUID} from 'node:crypto';
import {encodedRoutePath, routePath} from '../../packages/contracts/src/route-paths.ts';
import {scrub} from './scrub.mjs';

const {values: args} = parseArgs({options: {base: {type: 'string'}, username: {type: 'string'}, 'password-file': {type: 'string'}, out: {type: 'string'}}});
if (!args.base || !args.username || !args['password-file'] || !args.out) throw new Error('--base, --username, --password-file and --out are required');
const base = args.base.replace(/\/$/, ''), out = resolve(args.out);
// The password file is either the fixture rig's JSON ({password}) or the bare password.
const raw = readFileSync(args['password-file'], 'utf8');
let password; try { password = JSON.parse(raw).password; } catch { password = raw.trim(); }
if (!password) throw new Error('no password in the password file');

rmSync(out, {recursive: true, force: true});
mkdirSync(out, {recursive: true});
let token = '';
const notes = [];
const enc = encodeURIComponent;

async function call(method, path, body, headers = {}) {
  const res = await fetch(base + path, {
    method, headers: {...(body !== undefined ? {'content-type': 'application/json'} : {}), ...(token ? {authorization: `Bearer ${token}`} : {}), ...headers},
    body: body !== undefined ? JSON.stringify(body) : undefined, signal: AbortSignal.timeout(30000),
  });
  const text = await res.text();
  let json = null; try { json = text ? JSON.parse(text) : null; } catch { /* not JSON */ }
  return {status: res.status, json, text};
}

const scrubPath = path => path.replace(/([?&](?:opening|cursor|after)=)[^&]+/g, '$1<scrubbed>');
function keep(name, method, path, body, r) {
  const bodyOut = r.json !== null ? scrub(r.json) : r.text ? {unparsed: scrub(r.text.slice(0, 2000))} : null;
  writeFileSync(join(out, `${name}.json`), JSON.stringify({request: {method, path: scrubPath(path), ...(body !== undefined ? {body: scrub(body)} : {})}, status: r.status, body: bodyOut}, null, 2) + '\n');
  console.log(`${r.status} ${method} ${path.split('?')[0]} -> ${name}`);
}

/** Fetch, keep (scrubbed) and return the response. `name` is the corpus file. A file-info read that
 * answers 202 "preparing" is kept as `<name>-preparing`, then polled, and the ready answer is `<name>`. */
async function record(name, method, path, body, headers) {
  let r = await call(method, path, body, headers);
  if (r.status === 202 && r.json?.kind === 'file-info-preparing' && typeof r.json.queryId === 'string') {
    keep(`${name}-preparing`, method, path, body, r);
    for (let i = 0; i < 40 && r.status === 202; i++) {
      await new Promise(done => setTimeout(done, Math.max(250, Math.min(2000, r.json?.retryAfterMs ?? 1000))));
      r = await call('GET', routePath('get_file_info_query', {queryId: r.json?.queryId ?? ''}));
    }
  }
  keep(name, method, path, body, r);
  return r;
}

/** The scan may still be moving the catalog; an opening token goes stale when it does. Read the
 * detail until its catalog revision holds still, and keep that last reading. */
async function settledDetail(name, id) {
  const path = `${encodedRoutePath('get_items_id_detail', {id: enc(id)})}?related=none`;
  let last = await record(name, 'GET', path), same = 0;
  for (let i = 0; i < 40 && same < 2; i++) {
    await new Promise(done => setTimeout(done, 1500));
    const next = await record(name, 'GET', path);
    same = next.json?.revision?.catalog === last.json?.revision?.catalog ? same + 1 : 0;
    last = next;
  }
  return last;
}

const system = await record('system', 'GET', routePath('get_system'));
const signIn = await call('POST', routePath('post_direct_sign_in'), {username: args.username, password});
if (signIn.status !== 200 && signIn.status !== 201) throw new Error(`sign-in answered ${signIn.status}`);
token = signIn.json?.session?.accessToken;
if (!token) throw new Error('sign-in carried no access token');

const home = await record('home', 'GET', routePath('get_home'));
const libraries = await record('libraries', 'GET', routePath('get_libraries'));
const library = libraries.json?.items?.[0];
if (!library) throw new Error('the server has no library to record from');

const pivot = 'albums';
const capabilities = await record('browse-capabilities', 'GET', `${encodedRoutePath('get_libraries_id_browse_capabilities', {id: enc(library.id)})}?pivot=${pivot}`);
const browseBody = {pivot, query: null, sort: [], cursor: '', limit: 5, range: null, seek: null};
await record('browse-albums', 'POST', encodedRoutePath('browse_library', {id: enc(library.id)}), browseBody);
// Songs give the item reads something playable; the album page above is the container case.
// The scan may still be settling: ask again until the library answers with songs.
let songs;
for (let i = 0; i < 30; i++) {
  songs = await record('browse-songs', 'POST', encodedRoutePath('browse_library', {id: enc(library.id)}), {...browseBody, pivot: 'songs'});
  if (songs.status === 200 && (songs.json?.entries ?? []).length) break;
  await new Promise(done => setTimeout(done, 2000));
}
const song = (songs.json?.entries ?? songs.json?.items ?? []).map(e => e.item ?? e).find(e => e && typeof e.id === 'string' && e.kind === 'song') ?? (songs.json?.entries ?? [])[0]?.item;
if (!song?.id) throw new Error('browse returned no song to record item reads from');
const itemId = song.id;

await record('item', 'GET', encodedRoutePath('get_items_id', {id: enc(itemId)}));
const detail = await settledDetail('item-detail', itemId);
await record('player-offers', 'GET', encodedRoutePath('get_items_id_playback_offers', {id: enc(itemId)}));

// File info: the flat view, then the window routes with the opening token from the item's detail.
const opening = detail.json?.fileInfoOpening;
// A release that predates the opening token (648) is asked the plain way, as its own client did.
const windowQuery = opening ? `?opening=${enc(opening)}&limit=50` : '';
const flat = await record('file-info-files', 'GET', `${encodedRoutePath('get_items_id_file_info', {id: enc(itemId)})}${windowQuery}`);
const windowRoute = (route, extra = '') => `${encodedRoutePath(route, {id: enc(itemId)})}${windowQuery}${extra ? (windowQuery ? '&' : '?') + extra : ''}`;
await record('file-info-groups', 'GET', windowRoute('get_item_file_info_groups'));
await record('file-info-sources', 'GET', windowRoute('get_item_file_info_sources'));
const groups = JSON.parse(readFileSync(join(out, 'file-info-groups.json'), 'utf8'));
const head = groups.body?.groups?.[0];
if (head?.headOccurrence) {
  for (const lane of ['audio', 'subtitles']) await record(`file-info-tracks-${lane}`, 'GET', windowRoute('get_item_file_info_tracks', `occurrence=${enc(head.headOccurrence)}&lane=${lane}`));
  await record('file-info-head-tracks-audio', 'GET', windowRoute('get_item_file_info_head_tracks', `group=${enc(head.id)}&lane=audio`));
  await record('file-info-members', 'GET', windowRoute('get_item_file_info_members', `group=${enc(head.id)}`));
} else notes.push('the item has no file-info group, so the tracks and members routes were not recorded');
void flat;

// A queue created by a plain POST, without startPlayback, starts no playback.
const created = await record('queue-create', 'POST', routePath('create_queue'), {owner: 'device', segments: [{source: {items: {ids: [itemId]}}}]}, {'Idempotency-Key': randomUUID()});
const queueId = created.json?.queue?.id;
if (queueId) await record('queue-window', 'GET', `${encodedRoutePath('get_queue_window', {id: enc(queueId)})}?around=current&before=20&after=50`);
else notes.push(`queue create answered ${created.status}; no queue window recorded`);

await record('events', 'GET', `${routePath('get_events')}?waitMs=0`);

writeFileSync(join(out, 'manifest.json'), JSON.stringify({recordedFrom: {version: system.json?.version ?? null, apiLevel: system.json?.apiLevel ?? null}, library: {id: library.id, kind: library.kind}, itemId, notes}, null, 2) + '\n');
console.log(notes.length ? `notes: ${notes.join('; ')}` : 'done');
void home; void capabilities;
