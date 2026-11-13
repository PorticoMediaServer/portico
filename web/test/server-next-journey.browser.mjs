// The real web app against the rebuilt server (server-next), by script. It starts the server on a
// fresh data folder and the web development server itself, on localhost, and walks the journeys of
// the areas that have moved: set up the owner, sign in, choose a profile with a PIN, stay signed in
// across a server restart, go back to Who's watching when a PIN changes elsewhere, browse a seeded
// library (Home, a list and its filter, a film, a show), change what is watched, change settings on
// one device and see which follow on another, play real films (made here with ffmpeg and added to a
// library through the API), sign out.
//
//   cd server-next && go build -o /path/to/portico-server-next ./cmd/portico-server
//   cd server-next && go build -o /path/to/portico-seed ./cmd/portico-seed   (for the browse steps)
//   cd web && PORTICO_SERVER_NEXT_BIN=/path/to/portico-server-next [PORTICO_SEED_BIN=/path/to/portico-seed] \
//     [CHROMIUM_PATH=…] [PLAYWRIGHT=/path/to/playwright/index.mjs] node test/server-next-journey.browser.mjs
//
// The playback steps need ffmpeg on the PATH, and a Chromium that plays H.264 and AAC.
//
// Each step prints "pass" or "FAIL" and the script carries on where it can, so one run says exactly
// which steps hold. It exits 1 when any step failed.
import {execFileSync, spawn} from 'node:child_process';
import http from 'node:http';
import {mkdtemp, readFile, rm, writeFile} from 'node:fs/promises';
import {createServer as netServer} from 'node:net';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';

const serverBin = process.env.PORTICO_SERVER_NEXT_BIN;
if (!serverBin) throw new Error('Set PORTICO_SERVER_NEXT_BIN to a build of server-next/cmd/portico-server.');
const {chromium} = await import(process.env.PLAYWRIGHT ?? 'playwright');
const web = dirname(dirname(fileURLToPath(import.meta.url)));
const password = ['journey', 'owner', 'Passphrase', '7'].join('-'), pin = '4821';
const cleanups = [], results = [];

const freePort = () => new Promise((resolve, reject) => {
  const probe = netServer().once('error', reject).listen(0, '127.0.0.1', () => {const {port} = probe.address(); probe.close(() => resolve(port));});
});
const until = async (what, check, tries = 100) => {
  for (let n = 0; n < tries * Number(process.env.JOURNEY_PATIENCE ?? 1); n++) {if (await check().catch(() => false)) return; await new Promise(resolve => setTimeout(resolve, 100));}
  throw new Error(what + ' did not happen.');
};

const data = await mkdtemp(join(tmpdir(), 'portico-next-journey-'));
cleanups.push(() => rm(data, {recursive: true, force: true}));
// The library is made before the server first starts, the way the load tests make theirs.
const seedBin = process.env.PORTICO_SEED_BIN;
if (seedBin) execFileSync(seedBin, ['-db', join(data, 'main.db'), '-items', '3000', '-phase', 'import', '-pictures', '600'], {stdio: 'ignore'});
const serverPort = await freePort(), serverOrigin = `http://127.0.0.1:${serverPort}`;
let server;
async function startServer() {
  // The server would look titles up at the real TMDB; a check never does. Port 9 on this machine answers nobody.
  server = spawn(serverBin, ['-data', data, '-listen', `127.0.0.1:${serverPort}`], {stdio: 'ignore', env: {...process.env, PORTICO_TMDB_ORIGIN: process.env.PORTICO_TMDB_ORIGIN ?? 'http://127.0.0.1:9'}});
  await until('The server starting', async () => (await fetch(serverOrigin + '/v1/system')).ok);
}
async function stopServer() {
  const gone = new Promise(resolve => server.once('exit', resolve));
  server.kill('SIGTERM'); await gone;
}
await startServer();
cleanups.push(() => stopServer());
process.env.PORTICO_DEV_SERVER_URL = serverOrigin;
const {createServer} = await import('vite');
const vite = await createServer({root: web, logLevel: 'error', server: {host: '127.0.0.1', port: await freePort(), strictPort: true}});
await vite.listen();
cleanups.push(() => vite.close());
const origin = `http://127.0.0.1:${vite.config.server.port}`;

const browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || undefined, args: ['--no-sandbox']});
cleanups.push(() => browser.close());
// On a busy machine the unbundled app loads slowly: JOURNEY_PATIENCE=4 waits four times as long everywhere.
const patience = Number(process.env.JOURNEY_PATIENCE ?? 1), newContext = browser.newContext.bind(browser);
browser.newContext = async options => {const context = await newContext(options); context.setDefaultTimeout(30000 * patience); return context;};

/** What the server was asked on this page, so a step can say which request it waited for. */
function watch(page) {
  const seen = [];
  const lists = [];
  page.on('response', answer => {
    const url = new URL(answer.url());
    if (url.pathname.startsWith('/v1/')) seen.push(`${answer.request().method()} ${url.pathname} ${answer.status()}`);
    if (url.pathname === '/v1/items' && answer.ok()) lists.push(url.searchParams);
  });
  return {seen, lists, has: prefix => seen.some(line => line.startsWith(prefix))};
}
/** The page's own words for a failure. Areas that have not moved yet show them; a moved area must not. */
const failureWords = /Something went wrong|Something didn.t load|couldn.t load|ran into a problem/i;
async function step(name, run) {
  try {await run(); results.push([name, true]); console.log('pass  ' + name);}
  catch (error) {results.push([name, false]); console.log('FAIL  ' + name + '\n      ' + String(error?.message ?? error).split('\n')[0]);}
}
const ownerToken = async () => {
  const post = (path, body, token) => fetch(serverOrigin + path, {method: 'POST', headers: {'Content-Type': 'application/json', ...(token ? {Authorization: 'Bearer ' + token} : {})}, body: JSON.stringify(body)}).then(answer => answer.json());
  const signIn = await post('/v1/auth/sign-in', {username: 'owner', password, device: {installationId: 'journey-script-installation-000000001', name: 'Journey script'}});
  const chosen = await post(`/v1/profiles/${signIn.profiles.find(profile => profile.isPrimary).id}/select`, {}, signIn.session.accessToken);
  return {token: chosen.accessToken, profiles: signIn.profiles};
};
async function signIn(page) {
  await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
  await page.getByLabel('Username').fill('owner');
  await page.getByLabel('Password', {exact: true}).fill(password);
  await page.getByRole('button', {name: 'Sign in', exact: true}).click();
}
const shell = page => page.getByRole('button', {name: /account menu/});

/** Two short real films: one the browser plays as it is (H.264 and AAC in MP4, with a subtitle
 * file beside it), one it cannot (MPEG-2 and AC-3 in Matroska), which the server converts. */
async function makeFilms() {
  const folder = await mkdtemp(join(tmpdir(), 'portico-next-films-'));
  cleanups.push(() => rm(folder, {recursive: true, force: true}));
  const film = (name, args) => {
    const dir = join(folder, name);
    execFileSync('mkdir', ['-p', dir]);
    execFileSync('ffmpeg', ['-loglevel', 'error', '-y', '-f', 'lavfi', '-i', 'testsrc2=size=640x360:rate=24', '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000', '-t', '60', ...args(join(dir, name))]);
    return dir;
  };
  const direct = film('Journey Film (2024)', out => ['-ac', '2', '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-g', '48', '-c:a', 'aac', '-b:a', '128k', '-movflags', '+faststart', out + '.mp4']);
  await writeFile(join(direct, 'Journey Film (2024).en.srt'), '1\n00:00:00,500 --> 00:00:59,000\nHello from the subtitle file\n');
  film('Converted Film (2023)', out => ['-ac', '2', '-c:v', 'mpeg2video', '-b:v', '2M', '-c:a', 'ac3', '-b:a', '192k', out + '.mkv']);
  return folder;
}

/** The video element's own clock and state, as the page plays it. */
const videoState = page => page.evaluate(() => {
  const video = document.querySelector('video');
  return video ? {time: video.currentTime, paused: video.paused, src: video.currentSrc || video.src, ready: video.readyState} : null;
});
async function advances(page, what, from = 0) {
  let start;
  await until(what + ' playing', async () => {const v = await videoState(page); if (v && v.time > from + 0.5 && !v.paused) {start ??= v.time; return true;} return false;}, 300);
  await until(what + ' moving on', async () => ((await videoState(page))?.time ?? 0) > start + 1.5, 100);
}

async function playback(page, requests) {
  const sessions = [];
  page.on('response', async answer => {
    const url = new URL(answer.url());
    if (url.pathname === '/v1/playback/sessions' && answer.request().method() === 'POST' || /^\/v1\/playback\/sessions\/[^/]+$/.test(url.pathname) && answer.request().method() === 'PATCH')
      sessions.push(await answer.json().catch(() => null));
  });
  const owner = () => ownerToken().then(o => o.token);
  const api = async (method, path, body) => {
    const answer = await fetch(serverOrigin + path, {method, headers: {'Content-Type': 'application/json', Authorization: 'Bearer ' + await owner(), 'Idempotency-Key': 'journey-' + Math.random().toString(36).slice(2, 14)}, body: body === undefined ? undefined : JSON.stringify(body)});
    return {status: answer.status, body: await answer.json().catch(() => undefined)};
  };
  let films = {};
  await step('two real films are added to a library through the API and scanned', async () => {
    const folder = await makeFilms();
    const made = await api('POST', '/v1/admin/libraries', {name: 'Real films', kind: 'movies', folders: [folder]});
    if (made.status !== 200 && made.status !== 201) throw new Error('The library was not made: ' + made.status + ' ' + JSON.stringify(made.body));
    await until('The scan', async () => {const scan = await api('GET', `/v1/admin/libraries/${made.body.id}/scan`); return scan.body?.state === 'idle' && !!scan.body.lastScanFinishedAt;}, 300);
    const listed = await api('GET', `/v1/items?library=${made.body.id}`);
    for (const item of listed.body.items) films[/Converted/.test(item.title) ? 'converted' : 'direct'] = item.id;
    if (!films.direct || !films.converted) throw new Error('The scan did not list both films: ' + JSON.stringify(listed.body));
  });
  // Opening an address on an account with more than one profile asks Who's watching first.
  const openTitle = async (window, id) => {
    await window.goto(`${origin}/item/${id}`, {waitUntil: 'domcontentloaded'});
    if (await window.getByText(/Who.s watching/).waitFor({timeout: 8000}).then(() => true, () => false)) await window.getByRole('button', {name: /owner/}).first().click();
  };
  // The player hides its controls while playing; moving the pointer brings them back.
  const controls = async () => {await page.mouse.move(600, 400); await page.mouse.move(620 + Math.random() * 40, 420); await page.waitForTimeout(300);};
  const press = async name => {await controls(); await page.getByRole('button', {name, exact: true}).first().click();};
  const choose = async (menu, row) => {await controls(); await page.getByRole('button', {name: menu}).first().click(); await page.getByRole('radio', {name: row}).first().dispatchEvent('click'); await page.keyboard.press('Escape');};
  const account = async () => (await api('GET', '/v1/playback/sessions')).body.items;
  let stoppedAt = 0, stoppedUrl = '';
  await step('Play on a film the browser can play plays the file itself', async () => {
    await openTitle(page, films.direct);
    await page.getByRole('button', {name: 'Play', exact: true}).first().click();
    await advances(page, 'The film');
    const started = sessions.at(-1);
    if (started?.presentation?.mode !== 'direct' || started.presentation.route !== 'original') throw new Error('It was not played as the file itself: ' + JSON.stringify(started?.presentation));
    if (!requests.seen.some(line => /^GET \/v1\/media\/[^/]+\/original 20[06]/.test(line))) throw new Error('The file was not read from its media address.');
  });
  await step('Pause holds the picture and the server hears it; Play goes on', async () => {
    await press('Pause');
    await until('The pause', async () => (await videoState(page))?.paused === true);
    const held = (await videoState(page)).time;
    await until('The server hearing of the pause', async () => (await account())[0]?.state === 'paused', 100);
    await page.waitForTimeout(1500);
    if (Math.abs((await videoState(page)).time - held) > 0.1) throw new Error('The picture moved while paused.');
    await press('Play');
    await advances(page, 'The film after Play', held);
  });
  await step('Forward 30 seconds seeks and playing goes on from there', async () => {
    const before = (await videoState(page)).time;
    await press('Forward 30 seconds');
    await until('The seek', async () => ((await videoState(page))?.time ?? 0) > before + 25, 100);
    await advances(page, 'The film after the seek', before + 25);
  });
  await step('Choosing 240p while playing moves to a converted stream at the same place', async () => {
    const before = (await videoState(page)).time;
    await choose(/^Original$/, /240p/);
    await until('The change of quality', async () => sessions.at(-1)?.presentation?.quality?.id === '240p', 100);
    const changed = sessions.at(-1).presentation;
    if (changed.mode !== 'stream' || changed.route !== 'converted' || !/master\.m3u8$/.test(changed.url)) throw new Error('The new quality is not a converted stream: ' + JSON.stringify(changed));
    if (Math.abs(changed.startPositionMs / 1000 - before) > 5) throw new Error(`The stream starts at ${changed.startPositionMs} ms, the picture was at ${before} s.`);
    await advances(page, 'The converted stream', before - 1);
  });
  await step('Subtitles chosen in the player are drawn by the page', async () => {
    await choose('Subtitles and audio', /English/);
    await until('The subtitle on screen', async () => (await page.evaluate(() => [...document.querySelector('video').textTracks].some(t => t.mode === 'showing' && [...(t.activeCues ?? [])].some(c => /Hello from the subtitle file/.test(c.text))))), 150);
  });
  await step('Closing the player stops the session and leaves nothing playing on the server', async () => {
    stoppedAt = (await videoState(page)).time;
    stoppedUrl = sessions.at(-1).presentation.url;
    await press('Close player');
    await until('The stop', async () => requests.seen.some(line => /^DELETE \/v1\/playback\/sessions\/[^ ]+ 204/.test(line)), 100);
    if ((await account()).length) throw new Error('The server still lists a session: ' + JSON.stringify(await account()));
    const media = await fetch(serverOrigin + stoppedUrl);
    if (media.status !== 404) throw new Error('The stopped stream still answers ' + media.status);
  });
  await step('Continue Watching in another window resumes where it stopped', async () => {
    const other = await browser.newContext({viewport: {width: 1440, height: 900}}), second = await other.newPage();
    try {
      const resumed = [];
      second.on('response', async answer => {if (new URL(answer.url()).pathname === '/v1/playback/sessions' && answer.request().method() === 'POST') resumed.push(await answer.json().catch(() => null));});
      await signIn(second);
      await second.getByRole('button', {name: /owner/}).first().click();
      await second.getByRole('heading', {name: 'Continue Watching'}).waitFor();
      await second.getByRole('button', {name: /Journey Film/}).first().click();
      await second.getByRole('button', {name: /^Resume/}).first().click();
      await until('The resumed start', async () => resumed.length > 0, 100);
      const at = resumed[0].presentation.startPositionMs / 1000;
      if (Math.abs(at - stoppedAt) > 3) throw new Error(`It resumed at ${at} s; it was stopped at ${stoppedAt} s.`);
      await advances(second, 'The resumed film', at - 1);
      await second.getByRole('button', {name: 'Close player'}).first().click({force: true}).catch(() => {});
    } finally {await other.close();}
  });
  await step('Play on a film the browser cannot play plays a converted stream', async () => {
    await openTitle(page, films.converted);
    await page.getByRole('button', {name: 'Play', exact: true}).first().click();
    await advances(page, 'The converted film');
    const started = sessions.at(-1)?.presentation;
    if (started?.mode !== 'stream' || started.route !== 'converted') throw new Error('It was not converted: ' + JSON.stringify(started));
  });
  await step('A film played to its end is marked watched', async () => {
    await press('Forward 30 seconds');
    await page.waitForTimeout(1500);
    await press('Forward 30 seconds');
    await until('The end of the film', async () => (await api('GET', `/v1/items/${films.converted}`)).body?.state?.watched === true, 300);
    await press('Close player');
  });
}

try {
  const setup = await browser.newContext({viewport: {width: 1440, height: 900}});
  await step('set up the owner with the setup code from the data folder', async () => {
    const page = await setup.newPage(), requests = watch(page);
    await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
    // The first page of a run can wait for the development server to build the app, and reload once.
    await page.getByRole('radio', {name: /Local sign-in/}).check({timeout: 120000});
    await page.getByLabel('Username').fill('owner');
    await page.getByLabel('Password', {exact: true}).fill(password);
    await page.getByLabel('Confirm password').fill(password);
    await page.getByRole('button', {name: 'Create owner'}).click();
    await page.getByLabel('Setup code').fill('AAAA-AAAA');
    await page.getByRole('button', {name: 'Continue'}).click();
    await until('The wrong setup code being refused', async () => requests.has('POST /v1/setup/owner 403'));
    await page.getByLabel('Setup code').fill((await readFile(join(data, 'setup-code.txt'), 'utf8')).trim());
    await page.getByRole('button', {name: 'Continue'}).click();
    await page.getByText('Add your first library').waitFor();
    if (!requests.has('POST /v1/setup/owner 200')) throw new Error('The owner was not created.');
    if (failureWords.test(await page.locator('body').innerText())) throw new Error('The page shows a failure.');
  });
  await setup.close();

  // A second profile with a PIN, made the way another device would make it.
  const made = await fetch(serverOrigin + '/v1/profiles', {method: 'POST', headers: {'Content-Type': 'application/json', Authorization: 'Bearer ' + (await ownerToken()).token}, body: JSON.stringify({name: 'Kid', color: 'coral', pin})});
  if (!made.ok) throw new Error('The script could not add a profile: ' + made.status);

  const context = await browser.newContext({viewport: {width: 1440, height: 900}});
  const page = await context.newPage(), requests = watch(page);
  await step('sign in with the password and reach Who\'s watching', async () => {
    await signIn(page);
    await page.getByRole('button', {name: /Kid/}).waitFor();
    if (!requests.has('POST /v1/auth/sign-in 200')) throw new Error('Sign-in was not accepted.');
  });
  await step('a wrong PIN is refused in plain words', async () => {
    await page.getByRole('button', {name: /Kid/}).click();
    await page.getByLabel('Four-digit PIN').fill('0000');
    await page.getByText('That PIN isn’t right').waitFor();
  });
  await step('the right PIN opens the profile and the app', async () => {
    // A wrong PIN makes that profile wait two seconds on this device before the next try.
    await page.waitForTimeout(2500);
    await page.getByLabel('Four-digit PIN').fill(pin);
    await shell(page).waitFor();
    if (!/Kid/.test(await shell(page).getAttribute('aria-label') ?? '')) throw new Error('The app opened as someone else.');
  });
  await step('after a server restart nobody is signed out', async () => {
    await stopServer(); await startServer();
    await page.reload({waitUntil: 'domcontentloaded'});
    // The access token from before the restart still works, so nothing has to be renewed.
    await page.getByRole('button', {name: /Kid/}).waitFor();
    if (await page.getByLabel('Username').count()) throw new Error('The device was signed out.');
    await page.getByRole('button', {name: /Kid/}).click();
    await page.getByLabel('Four-digit PIN').fill(pin);
    await shell(page).waitFor();
  });
  await step('a PIN changed on another device sends this one back to Who\'s watching, still signed in', async () => {
    const owner = await ownerToken(), kid = owner.profiles.find(profile => profile.name === 'Kid');
    const changed = await fetch(`${serverOrigin}/v1/profiles/${kid.id}/pin`, {method: 'PUT', headers: {'Content-Type': 'application/json', Authorization: 'Bearer ' + owner.token}, body: JSON.stringify({pin})});
    if (!changed.ok) throw new Error('The PIN could not be changed: ' + changed.status);
    const before = requests.seen.length;
    await page.reload({waitUntil: 'domcontentloaded'});
    await page.getByRole('button', {name: /Kid/}).waitFor();
    if (!requests.seen.slice(before).some(line => line.startsWith('POST /v1/auth/refresh 200'))) throw new Error('The sign-in was not renewed.');
    if (await page.getByLabel('Username').count()) throw new Error('The device was signed out.');
  });
  await step('the main profile opens without a PIN', async () => {
    await page.getByRole('button', {name: /owner/}).click();
    await shell(page).waitFor();
  });
  // A profile with a PIN asks for it again after a reload (the restart step above); one without stays open.
  await step('a reload keeps the profile that was open', async () => {
    await page.reload({waitUntil: 'domcontentloaded'});
    await shell(page).waitFor();
    if (!/owner/.test(await shell(page).getAttribute('aria-label') ?? '')) throw new Error('The reload opened someone else.');
    if (await page.getByRole('button', {name: /Kid/}).count()) throw new Error('The reload asked Who\'s watching.');
  });
  if (seedBin) {
    const pressed = name => page.getByRole('button', {name, exact: true}).getAttribute('aria-pressed');
    const stateOf = async id => (await (await fetch(`${serverOrigin}/v1/items/${id}`, {headers: {Authorization: 'Bearer ' + (await ownerToken()).token}})).json()).state;
    let film = '';
    await step('Home shows rows of the library', async () => {
      await page.getByRole('heading', {name: 'Recently Added'}).waitFor();
      await page.locator('article[data-card-kind]').first().waitFor();
      if (!requests.has('GET /v1/home 200')) throw new Error('Home was not read from the server.');
      // The seed wrote plain pictures for some titles: a card draws the one the server named.
      await until('A card drawing its picture', async () => requests.has('GET /v1/images/')
        && page.evaluate(() => [...document.querySelectorAll('article[data-card-kind] img')].some(picture => picture.complete && picture.naturalWidth > 0)));
    });
    await step('a library lists its films with a count, and a genre filter narrows it', async () => {
      await page.getByRole('link', {name: 'Films'}).first().click();
      await page.getByRole('tab', {name: 'Movies'}).click();
      const count = async () => Number((await page.getByText(/^[\d,]+ movies$/).first().innerText()).replace(/\D/g, ''));
      await page.locator('article[data-card-kind]').first().waitFor();
      const all = await count();
      // A page never asks for the count; the count is its own small request.
      if (!requests.lists.some(query => query.get('library') && !query.has('total') && Number(query.get('limit')) > 1)) throw new Error('No page was read without a count.');
      if (!requests.lists.some(query => query.get('total') === 'true' && query.get('limit') === '1')) throw new Error('The count was not asked for by itself.');
      // The end of the list is opened at its position, not reached page by page.
      await until('The end of the list opening at its position', async () => {
        await page.evaluate(() => {for (const box of [document.scrollingElement, ...document.querySelectorAll('main, main *')]) if (box && box.scrollHeight > box.clientHeight + 200) box.scrollTop = box.scrollHeight;});
        return requests.lists.some(query => Number(query.get('at')) > 200);
      }, 50);
      await page.evaluate(() => {for (const box of [document.scrollingElement, ...document.querySelectorAll('main, main *')]) if (box) box.scrollTop = 0;});
      // The letter rail comes from its own request; a letter opens the list where that letter starts.
      const letter = page.getByRole('button', {name: 'Jump to M', exact: true});
      await letter.waitFor();
      if (!requests.seen.some(line => line.startsWith('GET /v1/facets 200'))) throw new Error('The letters were not read.');
      const jumps = requests.lists.filter(query => query.has('at')).length;
      await letter.focus();
      await page.keyboard.press('Enter');
      await until('The list opening at M', async () => requests.lists.filter(query => query.has('at')).length > jumps
        && (await page.locator('article[data-card-kind]').allInnerTexts()).some(text => /^(The |A )?M/.test(text.trim())));
      await page.evaluate(() => {for (const box of [document.scrollingElement, ...document.querySelectorAll('main, main *')]) if (box) box.scrollTop = 0;});
      await page.getByRole('button', {name: 'Filters', exact: true}).click();
      await page.getByText('Genre', {exact: true}).click();
      await page.getByRole('checkbox').first().click();
      await page.getByRole('button', {name: 'Done'}).click();
      await until('The filtered list', async () => (await count()) < all);
      if (!requests.seen.some(line => /^GET \/v1\/items 200/.test(line))) throw new Error('The list was not read from the server.');
      if (!await count()) throw new Error('The filter left nothing.');
    });
    await step('My List, watched, Favorites and a rating on a film reach the server and are still there after a reload', async () => {
      await page.locator('article[data-card-kind] button').first().click();
      await page.getByRole('button', {name: 'Mark as watched'}).waitFor();
      film = new URL(page.url()).pathname.split('/').pop();
      await page.getByRole('button', {name: 'My List', exact: true}).click();
      await until('My List', async () => (await stateOf(film)).inList);
      await page.getByRole('button', {name: 'Mark as watched'}).click();
      await until('Watched', async () => (await stateOf(film)).watched);
      await page.getByRole('button', {name: 'More', exact: true}).click();
      await page.getByRole('menuitemradio', {name: 'Add to Favorites'}).click();
      await until('Favorites', async () => (await stateOf(film)).favorite);
      await page.getByRole('button', {name: 'More', exact: true}).click();
      await page.getByRole('menuitem', {name: 'Rate…'}).click();
      // Typing a digit on the rating control chooses that rating.
      const rating = page.getByRole('combobox', {name: 'Your rating'});
      await rating.focus();
      await page.keyboard.type('7');
      await until('A rating being chosen', async () => /7 out of 10/.test(await rating.innerText()));
      await page.getByRole('button', {name: 'Save'}).click();
      await until('The rating', async () => (await stateOf(film)).rating === 7);
      await page.reload({waitUntil: 'domcontentloaded'});
      await page.getByRole('button', {name: 'Mark as unwatched'}).waitFor();
      if (await pressed('My List') !== 'true') throw new Error('My List was not kept.');
    });
    await step('a change made in one window shows in another without a reload', async () => {
      const other = await browser.newContext({viewport: {width: 1440, height: 900}}), second = await other.newPage();
      try {
        const openFilm = async window => {
          await window.goto(`${origin}/item/${film}`, {waitUntil: 'domcontentloaded'});
          await window.getByRole('button', {name: 'Mark as unwatched'}).waitFor();
        };
        // A new window signs in and chooses its profile once; after that an address opens straight to its page.
        await signIn(second);
        await second.getByRole('button', {name: /owner/}).click();
        await shell(second).waitFor();
        await openFilm(second);
        await openFilm(page);
        await page.getByRole('button', {name: 'Mark as unwatched'}).click();
        await second.getByRole('button', {name: 'Mark as watched'}).waitFor({timeout: 15000});
      } finally {await other.close();}
    });
    await step('marking a season watched can be undone', async () => {
      await page.getByRole('link', {name: 'Shows'}).first().click();
      await page.getByRole('tab', {name: 'Shows'}).click();
      await page.locator('article[data-card-kind] button').first().click();
      await page.getByText(/^0 of \d+ watched$/).waitFor();
      await page.getByRole('button', {name: 'Season 1 options'}).click();
      await page.getByRole('menuitem', {name: 'Mark season as watched'}).click();
      await page.getByText(/^(\d+) of \1 watched$/).waitFor();
      await page.getByRole('button', {name: 'Undo'}).click();
      await page.getByText(/^0 of \d+ watched$/).waitFor();
      if (!requests.has('POST /v1/items/state:undo 204')) throw new Error('The server was not asked to undo.');
    });
    await step('a show, a season and an album opened by their address land on their own pages, once', async () => {
      const token = (await ownerToken()).token, get = async path => (await fetch(serverOrigin + path, {headers: {Authorization: 'Bearer ' + token}})).json();
      const libraries = (await get('/v1/libraries')).items ?? [];
      const first = async (kind, query) => (await get(`/v1/items?${query}&kind=${kind}&limit=1`)).items?.[0];
      const tv = libraries.find(library => library.kind === 'shows'), music = libraries.find(library => library.kind === 'music');
      const show = tv && await first('show', 'library=' + tv.id), season = show && await first('season', 'parent=' + show.id);
      const album = music && await first('album', 'library=' + music.id);
      for (const [view, item] of [['show', show], ['season', season], ['album', album]]) {
        if (!item) throw new Error(`The seed has no ${view}.`);
        let navigations = 0; const count = frame => {if (frame === page.mainFrame()) navigations++;};
        page.on('framenavigated', count);
        try {
          await page.goto(`${origin}/item/${item.id}`, {waitUntil: 'domcontentloaded'});
          await until(`The ${view}'s own page`, async () => new URL(page.url()).searchParams.get('view') === view && new URL(page.url()).searchParams.get('entityId') === item.id, 150);
          await until(`The ${view} "${item.title}" drawn`, async () => (await page.locator('main').innerText()).includes(item.title), 200);
          const settled = navigations; await new Promise(resolve => setTimeout(resolve, 3000));
          if (navigations !== settled) throw new Error(`The ${view} page went on navigating (${navigations - settled} more).`);
        } finally {page.off('framenavigated', count);}
      }
    });
  }
  // Settings. A second browser is a second device of the same kind (web) on the same profile.
  const other = await browser.newContext({viewport: {width: 1440, height: 900}}), second = await other.newPage();
  cleanups.push(() => other.close());
  /** Opens a settings page; a window that has chosen its profile opens the address straight away. */
  const openSettings = async (window, section, title) => {
    await window.goto(`${origin}/settings?scope=account&section=${section}`, {waitUntil: 'domcontentloaded'});
    await window.getByRole('heading', {name: title, level: 1}).waitFor();
  };
  const atHome = window => window.getByRole('combobox', {name: 'At home'});
  await step('quality chosen on one device stays on it: another device on the same profile keeps its own', async () => {
    await signIn(second);
    await second.getByRole('button', {name: /owner/}).click();
    await shell(second).waitFor();
    await openSettings(page, 'quality', 'Quality & data');
    await atHome(page).click();
    await page.getByRole('option', {name: /^720p High/}).click();
    await until('The quality being saved', async () => requests.has('PATCH /v1/preferences 200') && /720p High/.test(await atHome(page).innerText()));
    await openSettings(second, 'quality', 'Quality & data');
    if (!/Original/.test(await atHome(second).innerText())) throw new Error('The other device took this device\'s quality.');
    await openSettings(page, 'quality', 'Quality & data');
    if (!/720p High/.test(await atHome(page).innerText())) throw new Error('This device did not keep its quality after a reload.');
  });
  await step('a setting for the whole profile changed on one device shows on the other without a reload', async () => {
    await openSettings(page, 'playback', 'Playback');
    await openSettings(second, 'playback', 'Playback');
    const autoplay = window => window.getByRole('switch', {name: 'Autoplay next episode'});
    await until('Autoplay showing on', async () => await autoplay(second).isChecked() && await autoplay(page).isChecked());
    await autoplay(page).click();
    await until('The other device following', async () => !await autoplay(second).isChecked(), 150);
  });
  await step('a setting changed while the server is out of reach is kept and saved when it is back', async () => {
    const skipBack = page.getByRole('combobox', {name: 'Skip back'});
    await stopServer();
    await skipBack.click();
    await page.getByRole('option', {name: /^30 seconds/}).click();
    await until('The change showing at once', async () => /30 seconds/.test(await skipBack.innerText()));
    await startServer();
    const saved = async () => {
      const answer = await fetch(serverOrigin + '/v1/preferences?deviceClass=web', {headers: {Authorization: 'Bearer ' + (await ownerToken()).token}});
      return (await answer.json()).effective['playback.skipBackMs'] === 30000;
    };
    await until('The kept change reaching the server', saved, 300);
  });
  await playback(page, requests);
  await step('sign out returns to sign-in, and stays signed out after a reload', async () => {
    await shell(page).click();
    await page.getByText('Sign out', {exact: true}).click();
    await page.getByLabel('Username').waitFor();
    if (!requests.has('DELETE /v1/auth/session 204')) throw new Error('The server was not told.');
    await page.reload({waitUntil: 'domcontentloaded'});
    await page.getByLabel('Username').waitFor();
  });
} finally {
  for (const cleanup of cleanups.reverse()) await Promise.resolve().then(cleanup).catch(() => {});
}
const failed = results.filter(([, ok]) => !ok).length;
console.log(`${results.length - failed} of ${results.length} steps passed.`);
process.exit(failed ? 1 : 0);
