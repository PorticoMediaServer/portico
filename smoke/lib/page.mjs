// Page helpers shared by the web and Hosted smoke flows.
import {writeFileSync} from 'node:fs';
import {join} from 'node:path';

const PLAYWRIGHT_CANDIDATES = [
  'playwright',
  'playwright-core',
  '/home/justin/.local/lib/node_modules/playwright/index.mjs',
  '/usr/lib/node_modules/playwright/index.mjs',
];

export async function loadPlaywright() {
  const override = process.env.SMOKE_PLAYWRIGHT;
  const candidates = override ? [override, ...PLAYWRIGHT_CANDIDATES] : PLAYWRIGHT_CANDIDATES;
  let lastError;
  for (const candidate of candidates) {
    try {
      return await import(candidate);
    } catch (error) {
      lastError = error;
    }
  }
  throw new Error(`playwright is not importable (tried ${candidates.join(', ')}): ${lastError}`);
}

export async function launch(playwright, {viewport} = {}) {
  const browser = await playwright.chromium.launch({
    channel: process.env.SMOKE_CHROMIUM ? undefined : (process.env.SMOKE_CHANNEL || undefined),
    executablePath: process.env.SMOKE_CHROMIUM || undefined,
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--autoplay-policy=no-user-gesture-required'],
  });
  const context = await browser.newContext({
    viewport: viewport || {width: 1440, height: 900},
    ignoreHTTPSErrors: false,
  });
  const page = await context.newPage();
  const events = {consoleErrors: [], pageErrors: [], failedRequests: [], api: []};
  page.on('console', message => {
    if (message.type() === 'error') events.consoleErrors.push(message.text().slice(0, 400));
  });
  page.on('pageerror', error => events.pageErrors.push(String(error).slice(0, 400)));
  page.on('requestfailed', request => {
    const failure = request.failure();
    events.failedRequests.push(`${request.method()} ${new URL(request.url()).pathname} ${failure ? failure.errorText : ''}`.slice(0, 300));
  });
  page.on('response', response => {
    const url = new URL(response.url());
    if (url.pathname.startsWith('/v1/')) {
      events.api.push(`${response.status()} ${response.request().method()} ${url.pathname}`);
      if (events.api.length > 400) events.api.shift();
    }
  });
  return {browser, context, page, events, viewport: viewport || {width: 1440, height: 900}};
}

export class Evidence {
  constructor(out) { this.out = out; this.sequence = 0; }
  path(name) {
    const safe = String(name).replace(/[^a-z0-9._-]+/gi, '-').slice(0, 80);
    return join(this.out, `${String(++this.sequence).padStart(2, '0')}-${safe}.png`);
  }
  async shot(page, name) {
    const target = this.path(name);
    try { await page.screenshot({path: target, fullPage: false}); } catch {}
    return target;
  }
  write(name, value) {
    const target = join(this.out, String(name).replace(/[^a-z0-9._-]+/gi, '-').slice(0, 80));
    writeFileSync(target, typeof value === 'string' ? value : JSON.stringify(value, null, 2) + '\n');
    return target;
  }
}

export async function settle(page, {quiet = 700, timeout = 20000} = {}) {
  try { await page.waitForLoadState('domcontentloaded', {timeout}); } catch {}
  try { await page.waitForLoadState('networkidle', {timeout: 6000}); } catch {}
  await page.waitForTimeout(quiet);
}

/** Clicks the first candidate that resolves. Candidates are ordered, cheapest first. */
export async function clickAny(page, candidates, {timeout = 4000, label = ''} = {}) {
  const tried = [];
  for (const candidate of candidates) {
    let locator;
    try {
      if (typeof candidate === 'string') locator = page.locator(candidate);
      else if (candidate.testid) locator = page.getByTestId(candidate.testid);
      else if (candidate.role) locator = page.getByRole(candidate.role, {name: candidate.name, exact: candidate.exact !== false});
      else if (candidate.text) locator = page.getByText(candidate.text, {exact: candidate.exact !== false});
      else if (candidate.css) locator = page.locator(candidate.css);
      else continue;
      const first = locator.first();
      if (await first.count() === 0) { tried.push(`none:${JSON.stringify(candidate)}`); continue; }
      await first.click({timeout});
      await settle(page, {quiet: 350, timeout: 8000});
      return {candidate, tried};
    } catch (error) {
      tried.push(`fail:${JSON.stringify(candidate)}:${String(error.message).split('\n')[0].slice(0, 120)}`);
    }
  }
  throw new Error(`could not click ${label || 'control'} (tried ${tried.join(' ; ')})`);
}

/** Returns the first locator that has a match, without clicking it. Polls while the screen loads. */
export async function findAny(page, candidates, {timeout = 8000} = {}) {
  const deadline = Date.now() + timeout;
  do {
    for (const candidate of candidates) {
      let locator;
      if (typeof candidate === 'string') locator = page.locator(candidate);
      else if (candidate.role) locator = page.getByRole(candidate.role, {name: candidate.name, exact: candidate.exact !== false});
      else if (candidate.text) locator = page.getByText(candidate.text, {exact: candidate.exact !== false});
      else if (candidate.css) locator = page.locator(candidate.css);
      else continue;
      const first = locator.first();
      try { if (await first.count() > 0 && await first.isVisible()) return first; } catch { /* retry until the deadline */ }
    }
    await page.waitForTimeout(300);
  } while (Date.now() < deadline);
  return null;
}

export async function bodyText(page) {
  return page.evaluate(() => (document.body.innerText || '').replace(/\n{3,}/g, '\n\n'));
}

/** Authenticated same-origin API call from the page's own browser session. */
export async function api(page, path, {method = 'GET', body} = {}) {
  const result = await page.evaluate(async ({path, method, body}) => {
    const csrfKey = Object.keys(localStorage).find(key => key.startsWith('portico.account.csrf'));
    const headers = {'Accept': 'application/json'};
    if (method !== 'GET') headers['Content-Type'] = 'application/json';
    const csrf = csrfKey ? localStorage.getItem(csrfKey) : null;
    if (csrf) headers['X-CSRF-Token'] = csrf;
    const response = await fetch(path, {method, headers, credentials: 'same-origin', body: body === undefined ? undefined : JSON.stringify(body)});
    const text = await response.text();
    let json;
    try { json = JSON.parse(text); } catch { json = undefined; }
    return {status: response.status, json, text: text.slice(0, 2000)};
  }, {path, method, body});
  return result;
}

/** Waits until predicate(state) is true, sampling the video element's live state. */
export async function samplePlayback(page, {samples = 6, interval = 500} = {}) {
  const readings = [];
  for (let index = 0; index < samples; index++) {
    const reading = await page.evaluate(() => {
      const videos = [...document.querySelectorAll('video')];
      const video = videos.sort((a, b) => (b.videoWidth * b.videoHeight) - (a.videoWidth * a.videoHeight))[0];
      if (!video) return {present: false};
      return {
        present: true,
        currentTime: Number(video.currentTime.toFixed(3)),
        duration: Number.isFinite(video.duration) ? Number(video.duration.toFixed(3)) : null,
        paused: video.paused,
        ended: video.ended,
        readyState: video.readyState,
        networkState: video.networkState,
        videoWidth: video.videoWidth,
        videoHeight: video.videoHeight,
        error: video.error ? {code: video.error.code, message: video.error.message} : null,
        buffered: video.buffered.length ? Number(video.buffered.end(video.buffered.length - 1).toFixed(2)) : 0,
        textTracks: [...video.textTracks].map(track => ({mode: track.mode, kind: track.kind, label: track.label, cues: track.cues ? track.cues.length : null})),
      };
    });
    readings.push(reading);
    if (index < samples - 1) await page.waitForTimeout(interval);
  }
  return readings;
}

/** True when the sampled video actually decoded and advanced, not merely requested. */
export function playbackEvidence(readings) {
  const live = readings.filter(r => r.present);
  if (!live.length) return {ok: false, reason: 'no <video> element in the document'};
  const withFrame = live.filter(r => r.videoWidth > 0 && r.videoHeight > 0);
  if (!withFrame.length) return {ok: false, reason: `video has no decoded frame (widths ${live.map(r => r.videoWidth).join(',')})`};
  const errored = live.find(r => r.error);
  if (errored) return {ok: false, reason: `video element error ${errored.error.code}: ${errored.error.message}`};
  const ready = live.filter(r => r.readyState >= 2);
  if (!ready.length) return {ok: false, reason: `readyState never reached HAVE_CURRENT_DATA (saw ${live.map(r => r.readyState).join(',')})`};
  const times = live.map(r => r.currentTime);
  if (times.some(time => !Number.isFinite(time))) {
    return {ok: false, reason: 'currentTime contains a nonfinite measurement'};
  }
  const advanced = Math.max(...times) - Math.min(...times);
  if (!Number.isFinite(advanced)) {
    return {ok: false, reason: 'currentTime advancement is nonfinite'};
  }
  const playing = live.some(r => !r.paused);
  if (!playing) return {ok: false, reason: `video stayed paused (${times.join(', ')})`};
  if (advanced < 0.5) return {ok: false, reason: `currentTime did not advance (${times.join(', ')})`};
  return {ok: true, advanced, width: withFrame[0].videoWidth, height: withFrame[0].videoHeight,
    duration: live[live.length - 1].duration, readings: live};
}

/** A blank or black screen is a P0, so check the painted surface, not just the title. */
export async function surfaceState(page) {
  return page.evaluate(() => {
    const text = (document.body.innerText || '').trim();
    const canvases = [...document.querySelectorAll('canvas')].map(canvas => ({w: canvas.width, h: canvas.height}));
    return {
      url: location.href,
      title: document.title,
      textLength: text.length,
      textHead: text.slice(0, 240),
      elementCount: document.querySelectorAll('*').length,
      canvases,
      background: getComputedStyle(document.body).backgroundColor,
    };
  });
}

/**
 * The safe projection of a playback session body: what the player was given, without the grant,
 * the locator or any query. Used by the live and transcode checks so a later analysis has the
 * presentation fields instead of only a status code.
 */
export function projectSession(body) {
  if (!body || typeof body !== 'object') return null;
  const presentation = body.presentation || {};
  const pathPrefix = value => {
    if (typeof value !== 'string' || !value) return null;
    try { return new URL(value, 'http://x').pathname.split('/').slice(0, 4).join('/'); } catch { return 'unparsable'; }
  };
  const linear = presentation.linear;
  return {
    state: body.state,
    generation: body.generation,
    mode: presentation.mode,
    presentationGeneration: presentation.generation,
    qualityId: presentation.quality?.id ?? null,
    videoAction: presentation.decision?.video?.action ?? null,
    videoHeight: presentation.videoHeight ?? null,
    urlPresent: typeof presentation.url === 'string' && presentation.url.length > 0,
    urlPathPrefix: pathPrefix(presentation.url),
    linearPresent: !!linear,
    linear: linear ? {
      state: linear.state,
      errorCode: linear.errorCode ?? linear.error_code ?? null,
      windowStartMs: linear.windowStartMs ?? null,
      windowEndMs: linear.windowEndMs ?? null,
      liveEdgeMs: linear.liveEdgeMs ?? null,
    } : null,
  };
}

/** Records playback-session responses with the projected presentation and a UTC timestamp. */
export function watchSessions(page, sink = []) {
  page.on('response', async response => {
    const url = new URL(response.url());
    if (!url.pathname.startsWith('/v1/playback/sessions')) return;
    const entry = {at: new Date().toISOString(), status: response.status(), method: response.request().method(), path: url.pathname};
    if ([200, 201, 202].includes(response.status())) {
      try { entry.session = projectSession(await response.json()); } catch { entry.bodyUnreadable = true; }
    }
    sink.push(entry);
    if (sink.length > 20) sink.shift();
  });
  return sink;
}

/** A paused key seek must move by the configured increment (or the remaining range). */
export function seekEvidence(before, after, seconds) {
  if (before?.paused !== true || after?.paused !== true) return {ok: false, reason: 'seek was not measured while paused'};
  if (![before.currentTime, before.duration, after.currentTime, seconds].every(Number.isFinite)
    || seconds <= 0 || before.duration - before.currentTime < 1) return {ok: false, reason: 'seek has no valid expected range'};
  const target = Math.min(before.currentTime + seconds, before.duration);
  return Math.abs(after.currentTime - target) <= 0.75
    ? {ok: true, target}
    : {ok: false, reason: `seek missed its expected target (${before.currentTime} -> ${after.currentTime}, expected ${target})`};
}

/** Decoding alone does not establish conversion: require the committed new plan. */
export function transcodeEvidence(before, after) {
  const presentation = after?.presentation;
  if (!before?.id || after?.id !== before.id) return {ok: false, reason: 'quality change did not retain the playback session'};
  if (!Number.isInteger(before.presentation?.generation) || !Number.isInteger(presentation?.generation)
    || presentation.generation <= before.presentation.generation) return {ok: false, reason: 'quality change did not publish a new generation'};
  if (presentation.mode !== 'stream' || presentation.decision?.video?.action !== 'transcode'
    || presentation.quality?.id !== '240p' || !Number.isInteger(presentation.videoHeight)
    || presentation.videoHeight <= 0 || presentation.videoHeight > 240) return {ok: false, reason: 'quality change did not commit a 240p video transcode'};
  return {ok: true};
}
