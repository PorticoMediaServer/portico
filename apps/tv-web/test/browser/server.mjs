// A Portico server (server-next) for the TV-web browser checks, on a fresh data folder:
// - a seeded library (portico-seed: films, shows, music and books, with posters), so Home has full
//   rows to move through, and five films part-watched, so it has Continue Watching;
// - one real film, made here with ffmpeg in a library that uses local metadata only, to play;
// - an owner set up with the setup code from the data folder, with a password made for this run.
// The server is told to look titles, updates and title data up at a closed local port: nothing
// here reaches the internet. Everything it writes goes under `scratch` and is removed by `stop`.
import {spawn, execFileSync} from 'node:child_process';
import {randomBytes} from 'node:crypto';
import {mkdirSync, mkdtempSync, readFileSync, rmSync} from 'node:fs';
import {createServer} from 'node:net';
import path from 'node:path';

const nowhere = 'http://127.0.0.1:9';
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const freePort = () => new Promise((resolve, reject) => {
  const probe = createServer().once('error', reject).listen(0, '127.0.0.1', () => {const {port} = probe.address(); probe.close(() => resolve(port));});
});

export async function startServer({serverBin, seedBin, scratch, seedItems = 400, log = () => {}}) {
  const root = mkdtempSync(path.join(scratch, 'tvweb-server-'));
  const data = path.join(root, 'data'), media = path.join(root, 'media', 'Portico Test Pattern (2026)');
  mkdirSync(data, {recursive: true});
  mkdirSync(media, {recursive: true});
  execFileSync(seedBin, ['-db', path.join(data, 'main.db'), '-items', String(seedItems), '-phase', 'import', '-pictures', String(seedItems)], {stdio: 'ignore'});
  // Twenty seconds of a test pattern and a tone, H.264 and AAC, as a film.
  execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-f', 'lavfi', '-i', 'testsrc2=size=640x360:rate=24', '-f', 'lavfi', '-i', 'sine=frequency=440',
    '-t', '20', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-c:a', 'aac', '-movflags', '+faststart', path.join(media, 'Portico Test Pattern (2026).mp4')]);
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  const server = spawn(serverBin, ['-data', data, '-listen', `127.0.0.1:${port}`], {stdio: 'ignore',
    env: {...process.env, PORTICO_TMDB_ORIGIN: nowhere, PORTICO_UPDATES_ORIGIN: nowhere, PORTICO_TITLE_DATA_ADDRESS: nowhere}});
  const stop = async () => {
    if (server.exitCode === null) {const gone = new Promise(resolve => server.once('exit', resolve)); server.kill('SIGTERM'); await gone;}
    rmSync(root, {recursive: true, force: true});
  };
  try {
    for (let n = 0; ; n++) {
      if (await fetch(origin + '/v1/system').then(answer => answer.ok, () => false)) break;
      if (n > 300) throw new Error('the server did not start');
      await sleep(100);
    }
    const call = async (method, route, body, token) => {
      const answer = await fetch(origin + route, {method, headers: {'Content-Type': 'application/json', ...(token ? {Authorization: 'Bearer ' + token} : {})},
        body: body === undefined ? undefined : JSON.stringify(body)});
      const text = await answer.text();
      if (!answer.ok) throw new Error(`${method} ${route}: ${answer.status} ${text.slice(0, 200)}`);
      return text ? JSON.parse(text) : undefined;
    };
    const username = 'owner', password = randomBytes(18).toString('base64url');
    const device = {installationId: 'tvweb-check-installation-0000000001', name: 'TV-web check'};
    const setupCode = readFileSync(path.join(data, 'setup-code.txt'), 'utf8').trim();
    await call('POST', '/v1/setup/owner', {setupCode, serverName: 'Portico Check', username, password, device});
    const signedIn = await call('POST', '/v1/auth/sign-in', {username, password, device});
    const primary = signedIn.profiles.find(profile => profile.isPrimary);
    const token = (await call('POST', `/v1/profiles/${primary.id}/select`, {}, signedIn.session.accessToken)).accessToken;
    await call('POST', '/v1/admin/libraries', {name: 'Test pattern', kind: 'movies', folders: [path.dirname(media)], metadataProvider: 'local'}, token);
    const libraries = (await call('GET', '/v1/libraries', undefined, token)).items;
    const films = libraries.find(library => library.name === 'Films');
    const some = (await call('GET', `/v1/items?library=${encodeURIComponent(films.id)}&limit=5`, undefined, token)).items;
    for (const [index, item] of some.entries()) await call('PUT', `/v1/items/${encodeURIComponent(item.id)}/state`, {positionMs: (index + 2) * 60_000}, token);
    // A third row, as the focus map draws one: tracks part-played make Continue Listening.
    const music = libraries.find(library => library.name === 'Music');
    const tracks = (await call('GET', `/v1/items?library=${encodeURIComponent(music.id)}&kind=track&limit=6`, undefined, token)).items;
    for (const track of tracks) await call('PUT', `/v1/items/${encodeURIComponent(track.id)}/state`, {positionMs: 30_000}, token);
    // A second profile, so the profile menu offers Switch profile, as in the focus map.
    await call('POST', '/v1/profiles', {name: 'Kids'}, token);
    const testPattern = libraries.find(library => library.name === 'Test pattern');
    for (let n = 0; ; n++) {
      const found = (await call('GET', `/v1/items?library=${encodeURIComponent(testPattern.id)}&limit=1`, undefined, token)).items;
      if (found.length) {
        // Part-watched last, so it is Continue Watching's first card: a card that really plays.
        await call('PUT', `/v1/items/${encodeURIComponent(found[0].id)}/state`, {positionMs: 4_000}, token);
        log(`test film: ${found[0].id}`);
        break;
      }
      if (n > 300) throw new Error('the test film was never scanned');
      await sleep(200);
    }
    return {origin, username, password, call, token, stop};
  } catch (error) {
    await stop();
    throw error;
  }
}
