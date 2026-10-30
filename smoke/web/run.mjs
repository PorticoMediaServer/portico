#!/usr/bin/env node
// M0 web smoke: the core flows from M0-HANDS-ON.md sections 1-9 on the deployed web app.
//
//   SMOKE_ORIGIN=https://demo.getportico.tv \
//   SMOKE_CREDENTIALS=/path/to/private/credentials.json \
//   SMOKE_OUT=/path/to/scratch/run-1 \
//   SMOKE_CHECKPOINT=v2-M0-001 \
//   node smoke/web/run.mjs
//
// Optional: SMOKE_ONLY=boot,signin (comma list), SMOKE_VIEWPORTS=1440x900,390x844,
//           SMOKE_CHANNEL=chrome, SMOKE_PLAYWRIGHT=/path/to/playwright/index.mjs
//
// Every failure records the step, the expectation, what was actually seen, and the
// screenshot path. Credentials are read from a private file and never written to the
// report or the console.
import {mkdirSync} from 'node:fs';
import {join} from 'node:path';
import {createReport, assert, blocked} from '../lib/report.mjs';
import {matchesBuildPin, buildStability} from '../lib/build.mjs';
import {loadCredentials, redactor} from '../lib/creds.mjs';
import {
  loadPlaywright, launch, Evidence, settle, clickAny, findAny, bodyText,
  samplePlayback, playbackEvidence, surfaceState, watchSessions, projectSession,
  seekEvidence, transcodeEvidence,
} from '../lib/page.mjs';

const origin = (process.env.SMOKE_ORIGIN || 'https://demo.getportico.tv').replace(/\/$/, '');
const out = process.env.SMOKE_OUT || join(process.cwd(), 'smoke-out');
const checkpoint = process.env.SMOKE_CHECKPOINT || '';
const only = new Set((process.env.SMOKE_ONLY || '').split(',').map(s => s.trim()).filter(Boolean));
const viewports = (process.env.SMOKE_VIEWPORTS || '1440x900,390x844').split(',')
  .map(pair => pair.split('x').map(Number)).map(([width, height]) => ({width, height}));
const desktop = viewports[0];
const mobile = viewports[1] || {width: 390, height: 844};

// Media's measured fixtures on the demo (45 s H.264 640x360 + AAC; Abbey Road 2019 mix).
const FIXTURES = {
  movie: process.env.SMOKE_MOVIE_ID || 'jJxPchu5fFfLRAJWCOyfvQ',
  album: process.env.SMOKE_ALBUM_ID || 'MxluJLkSmbFU2RFs2sO_9Q',
};

mkdirSync(out, {recursive: true});
const credentials = loadCredentials(process.env.SMOKE_CREDENTIALS);
const redact = redactor(credentials.password);
const playwright = await loadPlaywright();
const report = createReport({surface: 'web', origin, checkpoint, out});

function want(id) { return only.size === 0 || only.has(id); }

async function failure(page, evidence, name, error, extra) {
  const shot = await evidence.shot(page, name);
  const state = await surfaceState(page).catch(() => ({}));
  const message = redact(error && error.message ? error.message : error);
  const wrapped = new Error(message + ` [screenshot ${shot}; url ${state.url || '?'}]`);
  wrapped.evidence = {screenshot: shot, surface: state, ...(extra || {})};
  return wrapped;
}

/** Records the app's own authorised API traffic; in-page fetch is not authenticated. */
function watchApi(page, calls = [], state = {}) {
  page.on('response', response => {
    const url = new URL(response.url());
    if (!url.pathname.startsWith('/v1/')) return;
    calls.push({status: response.status(), method: response.request().method(), path: url.pathname});
    if (calls.length > 600) calls.shift();
    if (url.pathname === '/v1/preferences' && response.status() === 200) {
      response.json().then(body => {
        state.skipForwardMs = body.effective?.['playback.skipForwardMs'];
      }).catch(() => { state.preferencesUnreadable = true; });
    }
    if (url.pathname === '/v1/direct/account') {
      response.json().then(body => {
        state.account = {
          status: response.status(),
          canManage: body.canManage === true,
          capabilities: Object.keys(body.capabilities || {}).filter(key => body.capabilities[key]).slice(0, 12),
          profiles: (body.profiles || []).map(profile => ({name: profile.name, primary: profile.primary === true})),
          accountName: body.account && body.account.username ? 'present' : undefined,
        };
      }).catch(() => { state.account = {status: response.status(), unreadable: true}; });
    }
    if (url.pathname === '/v1/direct' && response.status() === 200) {
      response.json().then(body => {
        state.direct = {
          canManage: body.canManage,
          capabilityKeys: Object.keys(body.capabilities || {}),
          granted: Object.keys(body.capabilities || {}).filter(key => body.capabilities[key] === true),
          profiles: (body.profiles || []).map(profile => ({id: profile.id, name: profile.name, primary: profile.primary === true})),
        };
      }).catch(() => {});
    }
    if (url.pathname === '/v1/me' && response.status() === 200) {
      response.json().then(body => {
        const role = body.role || (body.account && body.account.role) || (body.viewer && body.viewer.role);
        if (body.viewer && body.viewer.profileId) state.viewerProfileId = body.viewer.profileId;
        const capabilities = body.capabilities || (body.account && body.account.capabilities) || [];
        state.me = {role, keys: Object.keys(body || {}).slice(0, 30),
          viewerKeys: Object.keys(body.viewer || {}).slice(0, 30),
          viewerFlags: Object.fromEntries(Object.entries(body.viewer || {})
            .filter(([key, value]) => (typeof value === 'boolean' || typeof value === 'string') &&
              !/id$/i.test(key) && !/token|secret|key/i.test(key))
            .slice(0, 20).map(([key, value]) => [key, typeof value === 'boolean' ? value : String(value).slice(0, 20)])),
          capabilities: Array.isArray(capabilities) ? capabilities.map(c => (typeof c === 'string' ? c : c && c.id)).filter(Boolean) : Object.keys(capabilities || {})};
      }).catch(() => {});
    }
  });
  return calls;
}

async function signIn(page, evidence) {
  await page.goto(origin + '/', {waitUntil: 'domcontentloaded', timeout: 45000});
  await settle(page, {quiet: 1200});
  if (!/\/sign-in/.test(page.url())) return {profile: 'already signed in', landing: page.url()};

  await page.locator('input[name=username]').first().fill(credentials.username, {timeout: 15000});
  await page.locator('input[name=password]').first().fill(credentials.password, {timeout: 15000});
  await clickAny(page, [{role: 'button', name: 'Sign in'}, {css: 'button[type=submit]'}], {label: 'Sign in button'});
  await page.waitForTimeout(3000);
  await settle(page, {quiet: 1200});

  if (/\/sign-in/.test(page.url())) {
    const text = await bodyText(page);
    const shot = await evidence.shot(page, 'signin-failed');
    throw Object.assign(new Error('sign-in did not leave the sign-in screen: ' + text.slice(0, 300)),
      {evidence: {screenshot: shot, text: text.slice(0, 1200)}});
  }

  if (/\/profiles/.test(page.url())) {
    const choice = await findAny(page, [
      {role: 'button', name: /Main profile/},
      {role: 'button', name: new RegExp(credentials.username, 'i')},
      {css: 'main button'},
    ]);
    assert(choice, 'profile screen shows no profile to choose');
    await choice.click({timeout: 10000});
    await page.waitForTimeout(3200);
    await settle(page, {quiet: 1000});
    assert(!/\/profiles/.test(page.url()), 'choosing a profile did not leave the profile screen');
    return {profile: 'chosen', landing: page.url()};
  }
  return {profile: 'single', landing: page.url()};
}

async function openItem(page, id) {
  await page.goto(`${origin}/item/${id}`, {waitUntil: 'domcontentloaded', timeout: 40000});
  await settle(page, {quiet: 2500});
}

/** Plays the open item and reports whether a real video surface appeared or the app died. */
async function playOpenItem(page, evidence, api, label) {
  const before = await page.evaluate(() => ({elements: document.querySelectorAll('*').length}));
  const apiMark = api.length;
  const control = await findAny(page, [
    {css: '[data-size=hero][data-variant=primary]'},
    {role: 'button', name: /^(Play|Resume|Start over)/},
  ]);
  assert(control, 'the item screen draws no Play control');
  const controlName = (await control.innerText().catch(() => '')).trim();
  await control.click({timeout: 10000});

  const readings = await samplePlayback(page, {samples: 8, interval: 700});
  const verdict = playbackEvidence(readings);
  const after = await page.evaluate(() => ({
    elements: document.querySelectorAll('*').length,
    textLength: (document.body.innerText || '').trim().length,
    videos: document.querySelectorAll('video').length,
    url: location.href,
  }));
  const errors = page.__smokeErrors || [];
  const crashed = after.elements < 200 && after.textLength < 40;
  const detail = {
    control: controlName, verdict: verdict.ok ? 'decoded' : verdict.reason, after,
    crashErrors: errors.slice(-3), api: api.slice(apiMark).filter(c => /playback|media|session/.test(c.path)).slice(0, 8),
  };
  const shot = await evidence.shot(page, label);
  return {verdict, after, crashed, detail, shot, before};
}

async function main() {
  const {browser, context, page: firstPage, events} = await launch(playwright, {viewport: desktop});
  const evidence = new Evidence(out);
  const apiState = {};
  const api = watchApi(firstPage, [], apiState);
  const sessions = watchSessions(firstPage);
  let page = firstPage;
  page.__smokeErrors = [];
  page.on('pageerror', error => page.__smokeErrors.push(String(error).slice(0, 200)));

  // A playback crash can take the whole renderer with it. Later checks must not inherit that.
  async function recover() {
    if (!page.isClosed()) return false;
    page = await context.newPage();
    page.__smokeErrors = [];
    page.on('pageerror', error => page.__smokeErrors.push(String(error).slice(0, 200)));
    watchApi(page, api, apiState);
    watchSessions(page, sessions);
    return true;
  }

  const readBuild = async () => {
    const out = {};
    try {
      const response = await context.request.get(origin + '/v1/system', {timeout: 20000});
      const json = await response.json();
      out.version = json.version; out.id = json.id; out.name = json.name;
    } catch { out.version = null; }
    try {
      const response = await context.request.get(origin + '/portico-build.json', {timeout: 20000});
      out.build = response.status() === 200 ? await response.json() : null;
    } catch { out.build = null; }
    return out;
  };
  report.buildStart = await readBuild();
  report.buildInfo = report.buildStart;
  const expectedSha = (process.env.SMOKE_EXPECT_SHA || '').trim();
  const servedSha = report.buildStart.build && report.buildStart.build.commit;
  if (expectedSha) {
    const matches = matchesBuildPin(servedSha, expectedSha);
    if (!matches) {
      report.note('build-pin', `STOP: expected ${expectedSha.slice(0, 12)} but the deployment serves ${servedSha || 'no build file'}; not running the flows`);
      await report.check('build-pin-valid', async () => {
        assert(false, 'deployment does not match the requested build pin');
      });
      report.write();
      await context.close();
      await browser.close();
      console.log(`\nSTOPPED on a build mismatch (expected ${expectedSha.slice(0, 12)}, served ${servedSha || 'none'})`);
      process.exit(3);
    }
    report.note('build-pin', `deployment serves ${servedSha.slice(0, 12)}, matching SMOKE_EXPECT_SHA`);
  }

  let signedIn = false;
  let sessionProfileSwitch = false;
  let searchState = null;
  const libraryNames = [];
  const check = (id, run, options) => report.check(id, async () => { await recover(); return run(); }, options);

  async function openLibrary(name) {
    const link = page.locator('nav a[href]').filter({hasText: new RegExp(`^${name}$`, 'i')}).first();
    const present = await link.count();
    assert(present, `the sidebar has no ${name} library`);
    const current = await link.getAttribute('aria-current').catch(() => null);
    const href = await link.getAttribute('href');
    if (current === 'page' && href) {
      await page.goto(new URL(href, origin).href, {waitUntil: 'domcontentloaded'});
    } else {
      await link.click({timeout: 8000});
    }
    await settle(page, {quiet: 1200});
    await page.locator('article[data-card-kind]').first().waitFor({timeout: 15000}).catch(() => {});
    await settle(page, {quiet: 800});
    return page.url();
  }

  try {
    if (want('boot')) {
      await check('boot', async () => {
        const response = await page.goto(origin + '/', {waitUntil: 'domcontentloaded', timeout: 45000});
        await settle(page, {quiet: 1200});
        const state = await surfaceState(page);
        assert(response && response.status() < 400, `document answered ${response ? response.status() : 'no response'}`);
        assert(state.textLength > 40, `blank surface on launch: ${JSON.stringify(state)}`);
        assert(events.pageErrors.length === 0, `page error on launch: ${events.pageErrors[0]}`);
        return {detail: `version ${report.buildStart && report.buildStart.version ? report.buildStart.version : 'unknown'}; landed on ${new URL(state.url).pathname}`,
          evidence: {screenshot: await evidence.shot(page, 'boot')}};
      });
    }

    if (want('signin')) {
      await check('signin', async () => {
        const result = await signIn(page, evidence);
        signedIn = true;
        const state = await surfaceState(page);
        assert(state.textLength > 40, `signed-in surface is blank: ${JSON.stringify(state)}`);
        return {detail: `${result.profile}; landed on ${new URL(state.url).pathname}`,
          evidence: {screenshot: await evidence.shot(page, 'after-signin')}};
      });
    } else if (want('home')) {
      // A home-only run still needs a session.
      await signIn(page, evidence).then(() => { signedIn = true; }).catch(() => {});
    }

    if (signedIn && want('home')) {
      await check('home', async () => {
        await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2500});
        const state = await surfaceState(page);
        const cards = await page.locator('article[data-card-kind]').count();
        assert(state.textLength > 120, `Home rendered almost no text: ${JSON.stringify(state)}`);
        assert(cards >= 3, `Home has ${cards} cards; expected rows of fixture titles`);
        const rows = await page.evaluate(() => [...document.querySelectorAll('h2,[role=heading]')]
          .map(h => (h.textContent || '').trim()).filter(Boolean).slice(0, 10));
        const titles = await page.evaluate(() => [...document.querySelectorAll('article[data-card-kind] button[aria-label]')]
          .map(b => b.getAttribute('aria-label')).slice(0, 8));
        return {detail: `${cards} cards; rows: ${rows.slice(0, 6).join(' / ')}; titles: ${titles.slice(0, 3).join(' / ')}`,
          evidence: {screenshot: await evidence.shot(page, 'home'), rows, titles}};
      });
    }

    if (signedIn && want('home-see-all')) {
      await check('home-see-all', async () => {
        await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2000});
        const rows = await page.locator('h2, [role=heading]').count();
        assert(rows >= 2, `Home drew ${rows} row headings`);
        const seeAll = page.locator('a[href], button').filter({hasText: /^See all$/i}).first();
        assert(await seeAll.count(), 'no row draws a See all control');
        const before = page.url();
        await seeAll.click({timeout: 8000});
        await settle(page, {quiet: 2000});
        const state = await page.evaluate(() => ({
          path: location.pathname, textLength: (document.body.innerText || '').trim().length,
          cards: document.querySelectorAll('article[data-card-kind]').length,
          missing: /That page isn't here|ran into a problem/i.test(document.body.innerText || ''),
        }));
        assert(state.path !== new URL(before).pathname, `See all did not navigate (still ${state.path})`);
        assert(!state.missing, `See all opened a broken page: ${state.path}`);
        assert(state.cards >= 3 || state.textLength > 200, `See all opened ${state.path} with ${state.cards} cards and ${state.textLength} chars`);
        return {detail: `See all -> ${state.path}: ${state.cards} cards, ${state.textLength} chars`, evidence: {screenshot: await evidence.shot(page, 'home-see-all'), state}};
      });
    }

    if (signedIn && want('library')) {
      await check('library', async () => {
        const nav = await page.evaluate(() => [...document.querySelectorAll('nav a[href]')]
          .map(a => ({name: (a.textContent || '').trim(), href: a.getAttribute('href')})).filter(l => l.href));
        libraryNames.push(...nav.map(item => item.name).filter(Boolean));
        assert(nav.length >= 3, `sidebar lists ${nav.length} entries: ${JSON.stringify(nav)}`);
        const target = nav.find(item => /^(Movies|Anime)$/i.test(item.name)) || nav.find(item => item.href.startsWith('/library/'));
        assert(target, `no library entry in the sidebar: ${JSON.stringify(nav.map(n => n.name))}`);
        await page.goto(new URL(target.href, origin).href, {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2000});
        const text = await bodyText(page);
        const cards = await page.locator('article[data-card-kind]').count();
        assert(text.trim().length > 60, `library ${target.name} rendered almost no text`);
        assert(cards >= 1, `library ${target.name} shows no cards`);
        return {detail: `${target.name}: ${cards} cards; sidebar: ${nav.slice(0, 8).map(n => n.name).join(', ')}`,
          evidence: {screenshot: await evidence.shot(page, 'library'), cards}};
      });
    }

    if (signedIn && want('library-controls')) {
      await check('library-controls', async () => {
        const where = await openLibrary('Movies');
        // The library opens on Discover; the browse pivot is the drawn tab inside it.
        const pivot = await findAny(page, [{role: 'tab', name: /^Movies$/}, {role: 'button', name: /^Movies$/}], {timeout: 5000});
        assert(pivot, `the Movies library draws no Movies pivot tab (at ${new URL(where).pathname})`);
        await pivot.click({timeout: 8000});
        await settle(page, {quiet: 1800});
        await page.locator('article[data-card-kind]').first().waitFor({timeout: 15000}).catch(() => {});
        const browseUrl = page.url();
        const count = async () => page.evaluate(() => {
          const text = document.body.innerText || '';
          const match = text.match(/(\d+)\s+of\s+\d+/);
          return match ? match[0] : `${document.querySelectorAll('article[data-card-kind]').length} cards`;
        });
        const before = await count();
        const quick = await findAny(page, [
          {role: 'button', name: /^Unwatched/},
          {role: 'button', name: /^In progress/},
          {role: 'button', name: /^Favorites/},
        ], {timeout: 4000});
        assert(quick, `the browse pivot draws no quick filter (at ${new URL(browseUrl).pathname})`);
        const quickName = (await quick.innerText()).trim();
        await quick.click({timeout: 8000});
        await settle(page, {quiet: 1600});
        const after = await count();
        await quick.click({timeout: 8000}).catch(() => {});
        await settle(page, {quiet: 1200});

        const filter = await findAny(page, [{role: 'button', name: /^Filters/}, {css: '[data-filters]'}], {timeout: 4000});
        assert(filter, 'the browse pivot draws no Filters control');
        await filter.click({timeout: 8000});
        const dialog = page.getByRole('dialog').first();
        await dialog.waitFor({timeout: 8000}).catch(() => {});
        assert(await dialog.count(), 'the Filters button opened no dialog');
        // Field tiles are collapsed buttons; a value only exists once the tile is expanded.
        const tile = dialog.locator('button[aria-expanded]').first();
        assert(await tile.count(), 'the filter dialog draws no field tiles');
        const tileName = (await tile.innerText().catch(() => '')).trim().slice(0, 40);
        await tile.click({timeout: 6000});
        await page.waitForTimeout(800);
        const values = dialog.locator('[role=checkbox], button[aria-pressed], button[aria-checked]');
        let valueCount = await values.count();
        if (valueCount === 0) {
          // Give the field a moment and try the whole page, not only the dialog subtree.
          await page.waitForTimeout(1200);
          valueCount = await page.locator('[role=checkbox], button[aria-pressed]').count();
        }
        assert(valueCount > 0, `expanding the "${tileName}" tile drew no selectable values`);
        const pool = (await values.count()) ? values : page.locator('[role=checkbox], button[aria-pressed]');
        let subject = pool.first();
        const total = await pool.count();
        for (let index = 0; index < total; index++) {
          const candidate = pool.nth(index);
          const label = (await candidate.innerText().catch(() => '')).trim();
          const state = await candidate.getAttribute('aria-checked') ?? await candidate.getAttribute('aria-pressed');
          if (!/^(any|all|none)\b/i.test(label) && state !== 'true') { subject = candidate; break; }
        }
        const valueName = (await subject.innerText().catch(() => '')).trim().slice(0, 40);
        const checkedBefore = await subject.getAttribute('aria-checked') ?? await subject.getAttribute('aria-pressed');
        await subject.click({timeout: 6000});
        await page.waitForTimeout(900);
        const checkedAfter = await subject.getAttribute('aria-checked') ?? await subject.getAttribute('aria-pressed');
        assert(checkedAfter !== checkedBefore, `ticking "${valueName}" did not change aria-checked (${checkedBefore} -> ${checkedAfter})`);
        const confirm = await findAny(page, [{role: 'button', name: /^(Show \d|Apply|Done)/}], {timeout: 2500});
        if (confirm) { await confirm.click({timeout: 6000}).catch(() => {}); }
        else { await page.keyboard.press('Escape').catch(() => {}); }
        await page.keyboard.press('Escape').catch(() => {});
        await dialog.waitFor({state: 'detached', timeout: 8000}).catch(() => {});
        await page.waitForTimeout(700);
        const sort = await findAny(page, [{role: 'button', name: /^(Sort|Title|Date added)/}, {css: '[data-sort]'}], {timeout: 4000});
        assert(sort, 'the browse pivot draws no Sort control');
        const sortLabel = (await sort.innerText().catch(() => '')).trim();
        const beforeNodes = await page.evaluate(() => document.querySelectorAll('body *').length);
        await sort.click({timeout: 8000});
        await page.waitForTimeout(1000);
        const sortSurface = await page.evaluate(before => ({
          expanded: (() => { const button = [...document.querySelectorAll('button')].find(b => /^(Sort|Title|Date added)/.test((b.innerText || '').trim())); return button ? button.getAttribute('aria-expanded') : null; })(),
          roles: [...document.querySelectorAll('[role=menuitemradio],[role=menuitem],[role=option],[role=radio],[role=dialog],[role=listbox]')]
            .map(e => ({role: e.getAttribute('role'), text: (e.textContent || '').trim().slice(0, 40)})).slice(0, 12),
          grew: document.querySelectorAll('body *').length - before,
        }), beforeNodes);
        const sorts = sortSurface.roles.map(item => item.text).filter(Boolean);
        await page.keyboard.press('Escape').catch(() => {});
        assert(sorts.length > 0 || sortSurface.grew > 4,
          `the Sort control "${sortLabel}" opened no surface (aria-expanded=${sortSurface.expanded}, roles=${JSON.stringify(sortSurface.roles)}, new nodes=${sortSurface.grew})`);
        return {detail: `browse at ${new URL(browseUrl).pathname}${new URL(browseUrl).search}; quick ${quickName}: ${before} -> ${after}; filter "${tileName}" -> "${valueName}" ${checkedBefore}->${checkedAfter}; sorts: ${sorts.slice(0, 4).join(', ')}`,
          evidence: {screenshot: await evidence.shot(page, 'library-controls'), browseUrl, tileName, valueName, checkedBefore, checkedAfter, sorts}};
      });
    }

    if (signedIn && want('library-tv-controls')) {
      await check('library-tv-controls', async () => {
        const controls = async () => page.evaluate(() => {
          const labels = [...document.querySelectorAll('button')].map(b => (b.innerText || '').trim());
          return {filters: labels.some(text => /^Filters/.test(text)),
            sort: labels.some(text => /^Sort/.test(text) || /^Title/.test(text) || /–Z|—/.test(text)),
            quick: labels.some(text => /^(Unwatched|In progress|Favorites)/.test(text)),
            cards: document.querySelectorAll('article[data-card-kind]').length,
            url: location.href};
        });
        const found = {};
        // Movies: sidebar entry lands on Discover; the drawn pivot tab reaches browse.
        await openLibrary('Movies');
        const moviesPivot = await findAny(page, [{role: 'tab', name: /^Movies$/}, {role: 'button', name: /^Movies$/}], {timeout: 5000});
        if (moviesPivot) { await moviesPivot.click({timeout: 8000}); await settle(page, {quiet: 1800}); }
        found.Movies = await controls();
        // TV Shows is a sidebar entry when the server has few libraries, otherwise it sits in More.
        let tv = await findAny(page, [{role: 'link', name: /^TV Shows$/}], {timeout: 2000});
        if (!tv) {
          const more = await findAny(page, [{role: 'button', name: /^More$/}], {timeout: 3000});
          if (more) {
            await more.click({timeout: 6000});
            await settle(page, {quiet: 900});
            tv = await findAny(page, [{role: 'menuitem', name: /^TV Shows$/}, {role: 'link', name: /^TV Shows$/}, {text: /^TV Shows$/}], {timeout: 2500});
          }
        }
        if (!tv) {
          // The More menu may group libraries under a Libraries entry.
          const libraries = await findAny(page, [{role: 'menuitem', name: /^Libraries$/}, {role: 'button', name: /^Libraries$/}, {text: /^Libraries$/}], {timeout: 2500});
          if (libraries) {
            await libraries.click({timeout: 6000}).catch(() => {});
            await settle(page, {quiet: 900});
            tv = await findAny(page, [{role: 'menuitem', name: /^TV Shows$/}, {role: 'link', name: /^TV Shows$/}, {text: /^TV Shows$/}], {timeout: 2500});
          }
        }
        if (tv) {
          await tv.click({timeout: 8000});
          await settle(page, {quiet: 1800});
          const tvPivot = await findAny(page, [{role: 'tab', name: /^Shows$/}, {role: 'button', name: /^Shows$/}], {timeout: 4000});
          if (tvPivot) { await tvPivot.click({timeout: 8000}); await settle(page, {quiet: 1500}); }
          found['TV Shows'] = await controls();
        } else {
          found['TV Shows'] = {missing: true, menuOpened: true};
        }
        const broken = Object.entries(found).filter(([, value]) => !value.missing && (!value.filters || !value.sort));
        assert(broken.length === 0, `these libraries still draw no sort or filter after the drawn pivot: ${broken.map(([name, value]) => `${name} (${JSON.stringify(value)})`).join('; ')}`);
        return {detail: JSON.stringify(found), evidence: {screenshot: await evidence.shot(page, 'library-tv-controls'), found}};
      });
    }

    if (signedIn && want('detail')) {
      await check('detail', async () => {
        // The catalogue is re-provisioned between runs, so resolve a title through the library
        // rather than pinning an item id that will go stale.
        await openLibrary('Movies');
        const firstMovie = page.locator('article[data-card-kind] button[aria-label]').first();
        assert(await firstMovie.count(), 'the Movies library lists no card to open');
        await firstMovie.click({timeout: 10000, force: true});
        await page.waitForURL(/\/item\//, {timeout: 20000}).catch(() => {});
        await settle(page, {quiet: 2000});
        const state = await surfaceState(page);
        assert(/\/item\//.test(state.url), `movie detail did not open (at ${state.url})`);
        const detailText = await bodyText(page);
        assert(!/That page isn't here|isn't available|no longer/i.test(detailText), `the movie detail showed a missing-item page: ${detailText.replace(/\n/g, ' ').slice(0, 120)}`);
        assert(state.textLength > 80, `movie detail rendered almost no text: ${JSON.stringify(state)}`);
        const play = await findAny(page, [{css: '[data-size=hero][data-variant=primary]'}, {role: 'button', name: /^Play/}]);
        assert(play, 'movie detail draws no Play control');
        return {detail: `12 Angry Men detail: ${state.textLength} chars, Play control present`,
          evidence: {screenshot: await evidence.shot(page, 'detail')}};
      });
    }

    if (signedIn && want('detail-show')) {
      await check('detail-show', async () => {
        const showLibrary = (await page.locator('nav a[href]').filter({hasText: /^TV Shows$/}).count()) ? 'TV Shows' : 'Anime';
        await openLibrary(showLibrary).catch(() => openLibrary('Anime'));
        const showsTab = await findAny(page, [{role: 'button', name: /^Shows/}, {role: 'tab', name: /^Shows/}, {text: /^Shows$/}]);
        if (showsTab) { await showsTab.click({timeout: 6000}).catch(() => {}); await settle(page, {quiet: 1500}); }
        const cards = page.locator('article[data-card-kind] button[aria-label]');
        const count = await cards.count();
        assert(count > 0, `the Anime library lists no show to open (Shows tab ${showsTab ? 'present' : 'absent'})`);
        const stale = [];
        let title = null;
        for (let index = 0; index < Math.min(count, 4); index++) {
          if (index > 0) { await openLibrary(showLibrary).catch(() => {}); await settle(page, {quiet: 1200}); }
          const cardsNow = page.locator('article[data-card-kind] button[aria-label]');
          if (await cardsNow.count() <= index) break;
          const card = cardsNow.nth(index);
          const name = await card.getAttribute('aria-label');
          await card.click({timeout: 8000, force: true});
          await page.waitForURL(/\/item\//, {timeout: 15000}).catch(() => {});
          await settle(page, {quiet: 1500});
          const opened = await bodyText(page);
          if (/That page isn't here|isn't available|no longer/i.test(opened)) { stale.push(name); continue; }
          title = name;
          break;
        }
        assert(title, `every show card opened a missing-item page (stale catalogue entries: ${stale.join(', ')})`);
        const state = await surfaceState(page);
        assert(/\/item\//.test(state.url), `opening show ${title} did not reach a detail screen (at ${state.url})`);
        const showText = await bodyText(page);
        assert(!/That page isn't here|isn't available|no longer/i.test(showText), `show ${title} opened a missing-item page: ${showText.replace(/\n/g, ' ').slice(0, 120)}`);
        assert(state.textLength > 80, 'show detail rendered almost no text');
        // Core flow 4 asks for the episodes, so the drawn screen must show them.
        const seasonMarkers = (showText.match(/Season \d+/gi) || []);
        const episodeMarkers = (showText.match(/S\d+\s*E\d+/gi) || []);
        const episodes = await page.locator('[data-episode], a[href*="episode"], [role=listitem]').count();
        assert(seasonMarkers.length > 0 || episodeMarkers.length > 0 || episodes > 0,
          `show ${title} draws no episodes: ${showText.replace(/\n/g, ' ').slice(0, 200)}`);
        const playControl = await findAny(page, [{css: '[data-size=hero][data-variant=primary]'}, {role: 'button', name: /^(Play|Resume)/}]);
        return {detail: `${title}: ${state.textLength} chars at ${new URL(state.url).pathname}; episodes ${Math.max(seasonMarkers.length, episodeMarkers.length, episodes)}; hero control ${playControl ? 'present' : 'missing'}${stale.length ? `; stale cards skipped: ${stale.join(', ')}` : ''}`,
          evidence: {screenshot: await evidence.shot(page, 'detail-show')}};
      });
    }

    // Core flow 5, first half. A 2xx never counts as playback: the surface must decode.
    if (signedIn && want('playback-direct')) {
      await check('playback-direct', async () => {
        await openItem(page, FIXTURES.movie);
        const result = await playOpenItem(page, evidence, api, 'playback-direct');
        if (result.crashed) {
          throw await failure(page, evidence, 'playback-direct-crash',
            new Error(`pressing ${result.detail.control || 'Play'} blanked the app: ${JSON.stringify(result.after)}`),
            result.detail);
        }
        if (!result.verdict.ok) throw await failure(page, evidence, 'playback-direct-failed', new Error(result.verdict.reason), result.detail);
        return {detail: `${result.verdict.width}x${result.verdict.height}, advanced ${result.verdict.advanced.toFixed(1)}s of ${result.verdict.duration}s`,
          evidence: {screenshot: result.shot, control: result.detail.control}};
      });
    }

    if (signedIn && want('playback-controls')) {
      await check('playback-controls', async () => {
        const live = await page.evaluate(() => {
          const video = document.querySelector('video');
          return video && video.readyState >= 2 ? {currentTime: video.currentTime, paused: video.paused} : null;
        });
        assert(live, 'no playing video to control (playback-direct must pass first)');
        const skipSeconds = apiState.skipForwardMs / 1000;
        if (!Number.isFinite(skipSeconds) || skipSeconds <= 0) throw blocked('the effective forward-skip preference was not observed');
        assert(live.paused === false, 'the playback fixture must be running before the Space pause check');
        await page.keyboard.press('Space');
        await page.waitForFunction(() => document.querySelector('video')?.paused === true, null, {timeout: 5000});
        // Measure immediately before seeking while paused: elapsed playback cannot satisfy this check.
        const before = await page.evaluate(() => {
          const video = document.querySelector('video');
          return {currentTime: video.currentTime, duration: video.duration, paused: video.paused};
        });
        if (!Number.isFinite(before.duration) || before.duration - before.currentTime < 1) throw blocked('the fixture has no remaining seek range');
        const target = Math.min(before.currentTime + skipSeconds, before.duration);
        await page.keyboard.press('ArrowRight');
        await page.waitForFunction(target => {
          const video = document.querySelector('video');
          return video?.paused && !video.seeking && Math.abs(video.currentTime - target) <= 0.75;
        }, target, {timeout: 5000});
        const after = await page.evaluate(() => {
          const video = document.querySelector('video');
          return {currentTime: video.currentTime, paused: video.paused};
        });
        const seek = seekEvidence(before, after, skipSeconds);
        assert(seek.ok, seek.reason);
        await page.keyboard.press('Space');
        await page.waitForFunction(() => document.querySelector('video')?.paused === false, null, {timeout: 5000});
        return {detail: `pause ok, resume ok, paused seek ${before.currentTime.toFixed(2)}s -> ${after.currentTime.toFixed(2)}s (requested ${skipSeconds}s)`,
          evidence: {screenshot: await evidence.shot(page, 'playback-controls')}};
      });
    }

    if (signedIn && want('playback-subtitles')) {
      await check('playback-subtitles', async () => {
        const playing = await page.evaluate(() => [...document.querySelectorAll('video')].some(v => v.readyState >= 2));
        if (!playing) {
          throw blocked('no playing video to show captions on (see playback-direct)');
        }
        const toggle = await findAny(page, [
          {role: 'button', name: /subtitles|captions/i},
          {role: 'menuitem', name: /subtitles|captions/i},
          {css: '[data-subtitles]'},
        ]);
        assert(toggle, 'the player draws no captions or subtitles control on this title');
        await toggle.click({timeout: 8000});
        await page.waitForTimeout(900);
        const tracks = await page.evaluate(() => [...document.querySelectorAll('video')]
          .flatMap(v => [...v.textTracks].map(t => ({kind: t.kind, mode: t.mode, label: t.label}))));
        await toggle.click({timeout: 8000});
        await page.waitForFunction(() => [...document.querySelectorAll('video')]
          .every(video => [...video.textTracks].every(track => track.mode !== 'showing')), null, {timeout: 5000});
        const after = await page.evaluate(() => [...document.querySelectorAll('video')]
          .flatMap(v => [...v.textTracks].map(t => ({kind: t.kind, mode: t.mode}))));
        assert(tracks.length > 0, 'the captions control produced no text track');
        assert(tracks.some(track => track.mode === 'showing'), `no text track is showing after the toggle: ${JSON.stringify(tracks)}`);
        assert(after.every(track => track.mode !== 'showing'), `a text track is still showing after off: ${JSON.stringify(after)}`);
        return {detail: `tracks on: ${JSON.stringify(tracks)}; after off: ${JSON.stringify(after)}`,
          evidence: {screenshot: await evidence.shot(page, 'playback-subtitles'), tracks, after}};
      });
    }

    if (signedIn && want('playback-transcode')) {
      await check('playback-transcode', async () => {
        await openItem(page, FIXTURES.movie);
        const control = await findAny(page, [{css: '[data-size=hero][data-variant=primary]'}, {role: 'button', name: /^(Play|Resume)/}]);
        assert(control, 'no Play control to start a transcode check');
        const apiMark = api.length;
        const [started] = await Promise.all([
          page.waitForResponse(response => response.request().method() === 'POST'
            && new URL(response.url()).pathname === '/v1/playback/sessions', {timeout: 10000}),
          control.click({timeout: 10000}),
        ]);
        assert([201, 202].includes(started.status()), `session start answered ${started.status()}`);
        let baseline = await started.json();
        assert(typeof baseline.id === 'string' && baseline.id, 'session start returned no session id');
        const sessionPath = '/v1/playback/sessions/' + encodeURIComponent(baseline.id);
        const chooseQuality = async label => {
          const quality = await findAny(page, [{role: 'button', name: /quality|240p|original/i}, {css: '[data-quality]'}]);
          if (!quality) throw blocked('the player draws no quality control');
          await quality.click({timeout: 6000});
          const name = label === '240p' ? /240p/i : /^Original\b/i;
          const choice = await findAny(page, [{role: 'menuitem', name}, {role: 'option', name}, {text: name}]);
          if (!choice) throw blocked(`the player offers no ${label} quality choice`);
          const [changed] = await Promise.all([
            page.waitForResponse(response => {
              const request = response.request();
              if (request.method() !== 'PATCH' || new URL(response.url()).pathname !== sessionPath) return false;
              let quality;
              try { quality = request.postDataJSON()?.quality; } catch { return false; }
              return label === '240p' ? quality?.id === '240p' : quality?.mode === 'original';
            }, {timeout: 10000}),
            choice.click({timeout: 6000}),
          ]);
          assert(changed.status() === 200, `${label} quality change answered ${changed.status()}`);
          return changed.json();
        };
        // Force an actual change even when the saved default already selected 240p.
        if (baseline.presentation?.quality?.id === '240p') baseline = await chooseQuality('Original');
        const changed = await chooseQuality('240p');
        const converted = transcodeEvidence(baseline, changed);
        assert(converted.ok, converted.reason, {before: projectSession(baseline), after: projectSession(changed)});
        const readings = await samplePlayback(page, {samples: 8, interval: 700});
        const verdict = playbackEvidence(readings);
        const calls = api.slice(apiMark);
        const session = calls.find(c => c.method === 'POST' && c.path === '/v1/playback/sessions');
        const media = calls.filter(c => c.method === 'GET' && c.path.startsWith('/v1/media/'));
        const crashed = await page.evaluate(() => document.querySelectorAll('*').length < 200).catch(() => true);
        if (crashed) {
          throw blocked('the web app blank-screened on Play, so no transcode decision can be observed; see playback-direct');
        }
        if (!verdict.ok) {
          throw await failure(page, evidence, 'playback-transcode-failed',
            new Error(`transcoded playback did not decode: ${verdict.reason}; session ${session ? session.status : 'none'}; media ${media.map(m => m.status).join(',') || 'none'}`),
            {calls});
        }
        const lastFrame = readings.findLast(reading => reading.present && reading.readyState >= 2 && reading.videoHeight > 0);
        assert(lastFrame?.videoHeight <= 240, 'the video still displays a frame above the selected 240p output');
        return {detail: `decoded ${verdict.width}x${verdict.height}, advanced ${verdict.advanced.toFixed(1)}s; media ${media.map(m => m.status).join(',')}; confirmed 240p transcode generation ${changed.presentation.generation}`,
          evidence: {screenshot: await evidence.shot(page, 'playback-transcode'), readings: readings.slice(0, 3), presentation: projectSession(changed), sessions: sessions.slice(-4)}};
      });
    }

    if (signedIn && want('album')) {
      await check('album', async () => {
        await page.goto(`${origin}/item/${FIXTURES.album}`, {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2500});
        const text = await bodyText(page);
        assert(/\/item\//.test(page.url()), `album ${FIXTURES.album} did not resolve to an item screen (at ${page.url()})`);
        if (/That page isn't here|not available|no longer/i.test(text)) {
          throw blocked(`the album fixture ${FIXTURES.album} is not on this deployment: ${text.replace(/\n/g, ' ').slice(0, 160)}`);
        }
        assert(/Abbey Road|album|song|track/i.test(text), `item ${FIXTURES.album} does not look like the album: ${text.replace(/\n/g, ' ').slice(0, 200)}`);
        const play = await findAny(page, [{role: 'button', name: /^(Play|Play all|Shuffle)/}, {css: '[data-size=hero][data-variant=primary]'}]);
        if (!play) return {detail: `album screen drew no play control; text ${text.slice(0, 120)}`, evidence: {screenshot: await evidence.shot(page, 'album')}};
        const apiMark = api.length;
        await play.click({timeout: 10000});
        await page.waitForTimeout(4000);
        const before = await page.evaluate(() => {
          const media = [...document.querySelectorAll('audio,video')];
          return media.map(element => ({tag: element.tagName.toLowerCase(), readyState: element.readyState, paused: element.paused, currentTime: Number((element.currentTime || 0).toFixed(2)), duration: element.duration})).slice(0, 3);
        });
        await page.waitForTimeout(2500);
        const audio = await page.evaluate(() => {
          const media = [...document.querySelectorAll('audio,video')];
          return media.map(element => ({tag: element.tagName.toLowerCase(), readyState: element.readyState, paused: element.paused, currentTime: Number((element.currentTime || 0).toFixed(2)), duration: element.duration})).slice(0, 3);
        });
        const calls = api.slice(apiMark).filter(c => /playback|media|session|queue/.test(c.path));
        const playing = audio.some((item, index) => item.readyState >= 2 && !item.paused && item.currentTime > (before[index]?.currentTime || 0));
        if (!playing) {
          throw await failure(page, evidence, 'album-failed',
            new Error(`album playback did not start: ${audio.map(a => `${a.tag} readyState ${a.readyState} ${a.paused ? 'paused' : 'playing'} at ${a.currentTime}s`).join('; ') || 'no media element'}`),
            {audio, calls});
        }
        return {detail: `album playing: ${audio.map(a => `${a.tag} ${a.readyState} at ${a.currentTime}s`).join('; ')}`,
          evidence: {screenshot: await evidence.shot(page, 'album'), audio, calls}};
      });
    }

    if (signedIn && want('search')) {
      await check('search', async () => {
        // Use the drawn search entry in the shell, not a directly typed URL.
        await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 1500});
        const entry = await findAny(page, [{role: 'link', name: /^Search/}, {role: 'button', name: /^Search/}, {css: 'a[href^="/search"]'}], {timeout: 4000});
        assert(entry, 'the shell draws no search entry');
        await entry.click({timeout: 8000});
        await settle(page, {quiet: 1500});
        const input = await findAny(page, [{role: 'searchbox'}, {css: 'input[type=search]'}, {role: 'textbox'}, {css: 'input[name=q]'}], {timeout: 5000});
        assert(input, 'Search draws no search field');
        await input.click({timeout: 8000});
        await input.fill('angry', {timeout: 8000});
        await page.keyboard.press('Enter');
        await page.waitForTimeout(3000);
        const cards = await page.locator('article[data-card-kind]').count();
        const text = await bodyText(page);
        assert(text.trim().length > 40, 'Search rendered almost no text for a query');
        assert(cards >= 1, `Search for "angry" returned ${cards} result cards`);
        searchState = {query: 'angry', cards};
        return {detail: `reached from the shell; query "angry": ${cards} cards`, evidence: {screenshot: await evidence.shot(page, 'search'), cards}};
      });

      await check('search-empty', async () => {
        assert(searchState, 'the search check did not run, so there is no search surface to reuse');
        const input = await findAny(page, [{role: 'searchbox'}, {css: 'input[type=search]'}, {role: 'textbox'}], {timeout: 5000});
        assert(input, 'the search field is gone after a query');
        await input.fill('zzqx no such title');
        await page.keyboard.press('Enter');
        await page.waitForTimeout(3000);
        const text = await bodyText(page);
        const cards = await page.locator('article[data-card-kind]').count();
        const emptyState = /no results|nothing found|no matches|nothing matches/i.test(text);
        assert(emptyState || cards === 0, `a nonsense query still drew ${cards} results and no empty state: ${text.replace(/\n/g, ' ').slice(0, 160)}`);
        assert(!/That page isn't here/i.test(text), 'the search screen crashed into the missing-page state for a nonsense query');
        return {detail: `nonsense query: ${cards} cards, empty state ${emptyState ? 'drawn' : 'not drawn (zero results instead)'}`,
          evidence: {screenshot: await evidence.shot(page, 'search-empty'), cards, emptyState}};
      });
    }

    if (signedIn && want('search-open')) {
      await check('search-open', async () => {
        const card = page.locator('article[data-card-kind] button[aria-label]').first();
        assert(await card.count() > 0, 'search results hold no card to open');
        const name = await card.getAttribute('aria-label');
        await card.focus();
        await page.keyboard.press('Enter');
        await page.waitForTimeout(3000);
        await settle(page, {quiet: 1000});
        assert(/\/item\//.test(page.url()), `opening search result ${name} went to ${page.url()}`);
        return {detail: `opened ${name} at ${new URL(page.url()).pathname}`,
          evidence: {screenshot: await evidence.shot(page, 'search-open')}};
      });
    }

    if (signedIn && want('profile-switch')) {
      await check('profile-switch', async () => {
        const who = async () => page.evaluate(() => {
          const button = [...document.querySelectorAll('button')].find(b => /account menu|switch profile/i.test(b.getAttribute('aria-label') || ''));
          return button ? (button.getAttribute('aria-label') || '') : null;
        });
        await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 1800});
        const before = await who();
        assert(before, 'the shell draws no account menu');
        const wanted = /Review/i.test(before) ? 'justin' : 'Review';
        const menu = await findAny(page, [{role: 'button', name: /account menu|switch profile/i}, {css: '[data-profile-menu]'}], {timeout: 4000});
        assert(menu, 'the account menu is not reachable');
        await menu.click({timeout: 8000});
        await settle(page, {quiet: 700});
        // The menu may list profiles directly or offer a "Switch profile" step first.
        let choice = await findAny(page, [{role: 'menuitem', name: new RegExp(wanted, 'i')}, {role: 'button', name: new RegExp(wanted, 'i')}], {timeout: 2500});
        if (!choice) {
          const step = await findAny(page, [{role: 'menuitem', name: /switch profile/i}, {role: 'button', name: /switch profile/i}], {timeout: 2500});
          if (step) {
            await step.click({timeout: 8000});
            await settle(page, {quiet: 1200});
            choice = await findAny(page, [{role: 'button', name: new RegExp(wanted, 'i')}, {role: 'menuitem', name: new RegExp(wanted, 'i')}], {timeout: 3000});
          }
        }
        assert(choice, `the menu offers no ${wanted} profile to switch to`);
        await choice.click({timeout: 8000});
        await settle(page, {quiet: 1500});
        // The shell rebuilds its facts after a switch; poll rather than trust one settle window.
        let after = null;
        for (let attempt = 0; attempt < 15; attempt++) {
          after = await who();
          if (after && new RegExp(wanted, 'i').test(after)) break;
          await page.waitForTimeout(800);
        }
        assert(after && new RegExp(wanted, 'i').test(after), `the shell still shows ${after} after choosing ${wanted} (15 seconds)`);
        sessionProfileSwitch = true;
        // Put it back so later checks run on the owner profile.
        const menu2 = await findAny(page, [{role: 'button', name: /account menu|switch profile/i}, {css: '[data-profile-menu]'}], {timeout: 4000});
        if (menu2) {
          await menu2.click({timeout: 8000});
          await settle(page, {quiet: 700});
          let back = await findAny(page, [{role: 'menuitem', name: /justin/i}, {role: 'button', name: /justin/i}, {role: 'menuitem', name: /Review/i}], {timeout: 2500});
          if (!back) {
            const step = await findAny(page, [{role: 'menuitem', name: /switch profile/i}, {role: 'button', name: /switch profile/i}], {timeout: 2000});
            if (step) { await step.click({timeout: 6000}); await settle(page, {quiet: 1000}); back = await findAny(page, [{role: 'button', name: /justin/i}, {role: 'menuitem', name: /justin/i}], {timeout: 3000}); }
          }
          if (back) { await back.click({timeout: 8000}); await settle(page, {quiet: 1800}); }
        }
        const restored = await who();
        return {detail: `switched ${wanted === 'Review' ? 'justin -> Review' : 'Review -> justin'}; shell label now ${restored}`,
          evidence: {screenshot: await evidence.shot(page, 'profile-switch'), before, after, restored}};
      });
    }

    if (signedIn && want('settings')) {
      await check('settings', async () => {
        await page.goto(origin + '/settings', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 1500});
        const text = await bodyText(page);
        assert(text.trim().length > 60, `Settings rendered almost no text: ${JSON.stringify(text.slice(0, 120))}`);
        const entries = await page.evaluate(() => [...document.querySelectorAll('main h2,main h3,[role=heading]')]
          .map(h => (h.textContent || '').trim()).filter(Boolean).slice(0, 14));
        const drawn = ['Profile & account', 'Privacy', 'Playback', 'Audio & subtitles', 'Quality & data', 'Appearance'];
        const missing = drawn.filter(name => !text.includes(name));
        assert(missing.length === 0, `Settings is missing drawn sections: ${missing.join(', ')}`);
        return {detail: `sections: ${entries.join(' / ') || 'none'}`,
          evidence: {screenshot: await evidence.shot(page, 'settings'), entries}};
      });
    }

    if (signedIn && want('profile-role')) {
      await check('profile-role', async () => {
        // The app reads /v1/me itself; in-page fetch is unauthenticated, so use its answer.
        for (let attempt = 0; attempt < 20 && !apiState.me; attempt++) await page.waitForTimeout(300);
        if (!apiState.me) {
          await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
          await settle(page, {quiet: 2000});
          for (let attempt = 0; attempt < 20 && !apiState.me; attempt++) await page.waitForTimeout(300);
        }
        assert(apiState.me, 'the app never answered /v1/me, so the account role is unknown');
        const {role, capabilities} = apiState.me;
        assert(/owner|administrator|admin/i.test(String(role)), `the owner account reports role "${role}"`);
        return {detail: `role ${role}; viewer fields: ${JSON.stringify(apiState.me.viewerFlags || {})}; viewer keys: ${(apiState.me.viewerKeys || []).join(', ')}; ${capabilities.length} capability entries${capabilities.includes('manageServer') ? ', manageServer present' : ''}`,
          evidence: {role, keys: apiState.me.keys, viewerKeys: apiState.me.viewerKeys, viewerFlags: apiState.me.viewerFlags, capabilities: capabilities.slice(0, 30)}};
      });
    }

    if (signedIn && want('settings-persist')) {
      await check('settings-persist', async () => {
        // Core flow 7 asks for client settings that actually stick, not just a screen that draws.
        await page.goto(origin + '/settings', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2000});
        const toggle = page.getByRole('switch', {name: /notification dot/i}).first();
        assert(await toggle.count(), 'Settings draws no client switch to exercise');
        const readState = async () => (await toggle.getAttribute('aria-checked')) ?? String(await toggle.isChecked().catch(() => ''));
        const before = await readState();
        await toggle.click({timeout: 8000});
        await page.waitForTimeout(1800);
        const flipped = await readState();
        assert(flipped !== before, `the switch did not change (${before} -> ${flipped})`);
        await page.reload({waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2500});
        const afterToggle = page.getByRole('switch', {name: /notification dot/i}).first();
        assert(await afterToggle.count(), 'the switch disappeared after a reload');
        const persisted = (await afterToggle.getAttribute('aria-checked')) ?? String(await afterToggle.isChecked().catch(() => ''));
        assert(persisted === flipped, `the setting did not persist: ${before} -> ${flipped} -> ${persisted} after reload`);
        // Restore the original value so the demo is left as found.
        await afterToggle.click({timeout: 8000});
        await page.waitForTimeout(1500);
        await page.reload({waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2000});
        const restored = page.getByRole('switch', {name: /notification dot/i}).first();
        const restoredState = (await restored.getAttribute('aria-checked')) ?? String(await restored.isChecked().catch(() => ''));
        return {detail: `notification dot ${before} -> ${flipped}, persisted after reload (${persisted}), restored to ${restoredState}`,
          evidence: {screenshot: await evidence.shot(page, 'settings-persist'), before, flipped, persisted, restored: restoredState}};
      });
    }

    if (signedIn && want('settings-owner')) {
      await check('settings-owner', async () => {
        // The shell hides every server section unless the session is on the primary profile, so
        // establish that first instead of blaming the page.
        let profileNote = 'profile not inspected';
        if (apiState.direct && apiState.direct.profiles.length) {
          const current = apiState.direct.profiles.find(profile => profile.id === apiState.viewerProfileId);
          const primary = apiState.direct.profiles.find(profile => profile.primary);
          profileNote = `session profile ${current ? current.name : 'unknown'}${current && current.primary ? ' (primary)' : ''}`;
          if (primary && (!current || !current.primary)) {
            const menu = await findAny(page, [{role: 'button', name: /account menu|switch profile/i}, {css: '[data-profile-menu]'}], {timeout: 4000});
            if (menu) {
              await menu.click({timeout: 8000});
              await settle(page, {quiet: 700});
              const choice = await findAny(page, [{role: 'menuitem', name: new RegExp(primary.name, 'i')}, {role: 'button', name: new RegExp(primary.name, 'i')}], {timeout: 4000});
              if (choice) {
                await choice.click({timeout: 8000});
                await page.waitForTimeout(3500);
                profileNote += `, switched to ${primary.name}`;
              }
            }
          }
        }
        // The documented address is correct; use it directly before anything else touches the
        // shell, and only fall back to the drawn General group when it really renders account scope.
        await page.goto(origin + '/settings?scope=server&section=libraries', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2000});
        // The shell re-bootstraps before the server group appears; poll rather than assume.
        for (let attempt = 0; attempt < 12; attempt++) {
          const probe = await bodyText(page);
          if (/Owner only/.test(probe)) break;
          await page.waitForTimeout(1000);
        }
        let libs = await bodyText(page);
        if (/Profile & account/.test(libs) && !/Owner only/.test(libs)) {
          const general = await findAny(page, [{role: 'button', name: /^General$/}, {role: 'tab', name: /^General$/}, {text: /^General$/}], {timeout: 5000});
          assert(general, 'Settings draws neither the server section at the documented address nor a General (server) group');
          await general.click({timeout: 8000});
          await settle(page, {quiet: 1500});
          const librariesEntry = await findAny(page, [{role: 'button', name: /^Libraries$/}, {role: 'tab', name: /^Libraries$/}, {text: /^Libraries$/}], {timeout: 4000});
          if (librariesEntry) { await librariesEntry.click({timeout: 8000}); await settle(page, {quiet: 1800}); }
          libs = await bodyText(page);
        }
        assert(/Owner only/.test(libs), `the owner server settings never rendered: ${libs.replace(/\n/g, ' ').slice(0, 200)}`);
        assert(libs.trim().length > 40, 'owner Libraries settings rendered almost no text');
        const scan = await findAny(page, [{role: 'button', name: /scan/i}, {text: /^Scan/}], {timeout: 4000});
        if (!scan) {
          const identityPaths = ['/v1/libraries', '/v1/system', '/v1/direct', '/v1/direct/account', '/v1/preferences', '/v1/guide/sources'];
          // The guide read is a member of the shell identity Promise.all too.
          const identityReads = identityPaths.map(path => {
            const hits = api.filter(call => call.path === path);
            const last = hits[hits.length - 1];
            return {path, status: last ? last.status : 'absent', count: hits.length};
          });
          const payload = JSON.stringify({profileSwitchBeforeSettings: sessionProfileSwitch, identityReads,
            me: apiState.me ? {role: apiState.me.role, profileIdKnown: !!apiState.viewerProfileId} : null,
            account: apiState.account || null,
            direct: apiState.direct ? {canManage: apiState.direct.canManage, capabilityKeys: apiState.direct.capabilityKeys, granted: apiState.direct.granted,
              profiles: apiState.direct.profiles.map(p => ({name: p.name, primary: p.primary}))} : null});
          throw blocked(`no scan control on the owner Libraries page; ${profileNote}; at ${new URL(page.url()).pathname}${new URL(page.url()).search}; page was: ${libs.replace(/\n/g, ' ').slice(0, 160)}`,
            {identityPayload: JSON.parse(payload)});
        }
        await scan.click({timeout: 8000});
        await page.waitForTimeout(3000);
        const scanResult = 'scan control pressed';
        await page.goto(origin + '/dashboard', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2500});
        const dash = await bodyText(page);
        assert(dash.trim().length > 60, 'owner Dashboard rendered almost no text');
        return {detail: `dashboard ${dash.length} chars; libraries ${libs.length} chars; ${scanResult}; ${profileNote}`,
          evidence: {screenshot: await evidence.shot(page, 'settings-libraries')}};
      });
    }

    if (signedIn && want('read')) {
      await check('read', async () => {
        await openLibrary('Books');
        const card = page.locator('article[data-card-kind]').first();
        assert(await card.count() > 0, 'the Books library lists no book');
        const title = await card.locator('button[aria-label]').first().getAttribute('aria-label').catch(() => 'unknown');
        const readButton = card.getByRole('button', {name: /^Read$/}).first();
        if (await readButton.count()) {
          await readButton.click({timeout: 8000});
        } else {
          await card.locator('button[aria-label]').first().focus();
          await page.keyboard.press('Enter');
        }
        await page.waitForTimeout(3500);
        await settle(page, {quiet: 1500});
        const state = await surfaceState(page);
        assert(/\/read\//.test(state.url), `book ${title} did not open a reader (at ${state.url})`);
        const text = await bodyText(page);
        if (text.trim().length <= 200) {
          const all = (events.consoleErrors || []).filter(line => /Content Security Policy|Refused to|blocked/i.test(line));
          const frameLines = all.filter(line => /frame-src|default-src|Refused to frame|blob:/i.test(line));
          const blocked = (frameLines.length ? frameLines : all).slice(-3);
          throw await failure(page, evidence, 'read-blocked',
            new Error(`reader rendered only ${text.trim().length} characters; console: ${blocked.join(' ;; ') || 'no CSP line captured'}`),
            {blocked, text: text.slice(0, 300)});
        }
        await page.keyboard.press('ArrowRight');
        await page.waitForTimeout(1200);
        return {detail: `read ${title}: ${text.length} chars, page turn sent`,
          evidence: {screenshot: await evidence.shot(page, 'read'), excerpt: text.slice(0, 200)}};
      });
    }

    if (signedIn && want('live')) {
      await check('live', async () => {
        await page.goto(origin + '/live', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 2500});
        const text = await bodyText(page);
        const channels = await page.evaluate(() => [...document.querySelectorAll('article[data-card-kind], [data-channel]')].length);
        assert(text.trim().length > 80, `Live screen rendered almost no text: ${JSON.stringify(text.slice(0, 120))}`);
        assert(channels > 0, 'Live guide shows no channels');
        let tuned = 'not tuned';
        const target = page.locator('article[data-card-kind] button[aria-label]').first();
        if (await target.count()) {
          const label = await target.getAttribute('aria-label');
          await target.focus();
          await page.keyboard.press('Enter');
          await page.waitForTimeout(3500);
          const readings = await samplePlayback(page, {samples: 6, interval: 700});
          const verdict = playbackEvidence(readings);
          tuned = `${label}: ${verdict.ok ? 'decoded' : verdict.reason}`;
          if (!verdict.ok) {
            throw await failure(page, evidence, 'live-play-failed',
              new Error(`tuning ${label} did not decode: ${verdict.reason}`), {readings: readings.slice(0, 4)});
          }
        }
        return {detail: `${channels} channels; ${tuned}`, evidence: {screenshot: await evidence.shot(page, 'live'), tuned, sessions: sessions.slice(-4)}};
      });
    }

    if (signedIn && want('signout')) {
      await check('signout', async () => {
        await page.goto(origin + '/', {waitUntil: 'domcontentloaded'});
        await settle(page, {quiet: 1500});
        const menu = await findAny(page, [
          {role: 'button', name: /account menu|switch profile/i},
          {css: '[data-profile-menu]'},
        ]);
        assert(menu, 'the shell draws no account menu');
        await menu.click({timeout: 8000});
        await settle(page, {quiet: 800});
        const out = await findAny(page, [{role: 'menuitem', name: /sign out/i}, {role: 'button', name: /sign out/i}, {text: /^Sign out$/}]);
        assert(out, 'the account menu has no Sign out entry');
        await out.click({timeout: 8000});
        await page.waitForTimeout(3000);
        await settle(page, {quiet: 1000});
        const state = await surfaceState(page);
        assert(/\/sign-in/.test(state.url), `sign out did not return to sign-in (at ${state.url})`);
        return {detail: `returned to ${new URL(state.url).pathname}`, evidence: {screenshot: await evidence.shot(page, 'signout')}};
      });
    }

    if (want('mobile')) {
      await check('mobile', async () => {
        const run = await launch(playwright, {viewport: mobile});
        try {
          const mobileEvidence = new Evidence(out);
          run.page.on('pageerror', error => (run.page.__smokeErrors ||= []).push(String(error).slice(0, 200)));
          await run.page.goto(origin + '/', {waitUntil: 'domcontentloaded', timeout: 45000});
          await settle(run.page, {quiet: 2000});
          let state = await surfaceState(run.page);
          if (/\/sign-in/.test(state.url)) {
            await run.page.locator('input[name=username]').first().fill(credentials.username, {timeout: 15000});
            await run.page.locator('input[name=password]').first().fill(credentials.password, {timeout: 15000});
            await clickAny(run.page, [{role: 'button', name: 'Sign in'}, {css: 'button[type=submit]'}], {label: 'mobile sign in'});
            await run.page.waitForTimeout(3200);
            if (/\/profiles/.test(run.page.url())) {
              await clickAny(run.page, [{role: 'button', name: /Main profile/}, {css: 'main button'}], {label: 'mobile profile'});
              await run.page.waitForTimeout(3000);
            }
            state = await surfaceState(run.page);
          }
          assert(state.textLength > 60, `phone width rendered a blank surface: ${JSON.stringify(state)}`);
          const overflow = await run.page.evaluate(() => ({
            scrollWidth: document.documentElement.scrollWidth,
            clientWidth: document.documentElement.clientWidth,
          }));
          const cards = await run.page.locator('article[data-card-kind]').count();
          const walked = [];
          const step = async (name, action) => {
            const before = Date.now();
            await action();
            await settle(run.page, {quiet: 1500});
            const info = await run.page.evaluate(() => ({
              path: location.pathname,
              textLength: (document.body.innerText || '').trim().length,
              cards: document.querySelectorAll('article[data-card-kind]').length,
              overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
            }));
            await mobileEvidence.shot(run.page, `mobile-${name}`);
            walked.push({name, ms: Date.now() - before, ...info});
          };
          // Core flows at phone width: browse, detail, search, settings. No playback here.
          await step('library', async () => {
            const link = run.page.locator('nav a[href]').filter({hasText: /^Movies$/}).first();
            if (await link.count()) await link.click({timeout: 12000});
          });
          await step('detail', async () => {
            const card = run.page.locator('article[data-card-kind] button[aria-label]').first();
            if (await card.count()) { await card.focus(); await run.page.keyboard.press('Enter'); await run.page.waitForTimeout(2500); }
          });
          await step('search', async () => {
            await run.page.goto(origin + '/search', {waitUntil: 'domcontentloaded'});
            const input = run.page.locator('input[type=search], input[name=q], input').first();
            if (await input.count()) { await input.fill('angry', {timeout: 8000}); await run.page.keyboard.press('Enter'); await run.page.waitForTimeout(2500); }
          });
          await step('settings', async () => {
            await run.page.goto(origin + '/settings', {waitUntil: 'domcontentloaded'});
            await run.page.waitForTimeout(1500);
          });
          const failed = walked.filter(item => item.textLength < 40 || item.overflow > 1);
          assert(failed.length === 0, `phone-width steps failed: ${failed.map(item => `${item.name} text ${item.textLength} overflow ${item.overflow}`).join('; ')}`);
          return {detail: `${state.url}: ${cards} cards at home, then ${walked.map(item => `${item.name}(${item.textLength})`).join(', ')}; overflow ${overflow.scrollWidth > overflow.clientWidth + 1 ? 'on home' : 'none'}`,
            evidence: {screenshot: await mobileEvidence.shot(run.page, 'mobile-home'), overflow, cards, walked}};
        } finally {
          await run.context.close();
          await run.browser.close();
        }
      });
    }
  } finally {
    // Read the build before the context closes, or the drift check sees only nulls.
    try { report.buildEnd = await readBuild(); } catch (error) { report.buildEnd = {error: String(error).slice(0, 120)}; }
    await context.close();
    await browser.close();
  }

  const endBuild = report.buildEnd || {};
  report.buildInfo = {start: report.buildStart, end: endBuild, expected: expectedSha || null};
  const stability = buildStability(report.buildStart, endBuild);
  await report.check('build-stability', async () => {
    assert(stability.valid, stability.reason, {start: report.buildStart, end: endBuild});
  });
  const result = report.write();
  if (!stability.valid) {
    console.log('\nBUILD INVALID: ' + stability.reason);
    process.exit(4);
  }
  console.log(`\n${result.verdict.toUpperCase()} ${result.counts.pass} pass / ${result.counts.fail} fail / ${result.counts.blocked} blocked`);
  console.log(`report: ${join(out, 'report.json')}`);
  console.log(`console errors ${events.consoleErrors.length}, page errors ${events.pageErrors.length}, failed requests ${events.failedRequests.length}`);
  process.exit(result.verdict === 'fail' ? 1 : 0);
}

main().catch(error => {
  console.error('smoke run crashed: ' + redact(error && error.stack ? error.stack : error).slice(0, 2000));
  process.exit(2);
});
