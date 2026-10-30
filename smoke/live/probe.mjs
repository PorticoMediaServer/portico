// Rendered-live probe with a real cleanup path.
//
// The creating app's own POST /v1/playback/sessions carries its Authorization and installation
// headers. Those are captured in memory only and reused for the exact DELETE of that same session on
// cleanup. They are never printed, logged, written to evidence or reused for anything else, and no
// authority is recreated.
//
//   SMOKE_CREDENTIALS=/path/creds.json SMOKE_OUT=/path/out node smoke/live/probe.mjs [maxSeconds]
import {readFileSync, mkdirSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {loadPlaywright, launch, Evidence, settle} from '../lib/page.mjs';
import {projectSession} from '../lib/page.mjs';

const origin = 'https://demo.getportico.tv';
const out = process.env.SMOKE_OUT || join(process.cwd(), 'live-out');
const maxSeconds = Number(process.argv[2] || 50);
const creds = JSON.parse(readFileSync(process.env.SMOKE_CREDENTIALS, 'utf8'));
mkdirSync(out, {recursive: true});

const playwright = await loadPlaywright();
const {browser, context, page} = await launch(playwright, {viewport: {width: 1440, height: 900}});
const evidence = new Evidence(out);
const events = [];
let sessionId = null;
// In memory only. Never serialised.
let creatingHeaders = null;

page.on('request', request => {
  if (request.method() === 'POST' && new URL(request.url()).pathname === '/v1/playback/sessions') {
    const headers = request.headers();
    if (headers && headers.authorization) creatingHeaders = {...headers};
  }
});
page.on('response', async response => {
  const url = new URL(response.url());
  if (!/^\/v1\/(playback\/sessions|media\/linear)/.test(url.pathname)) return;
  const entry = {at: new Date().toISOString(), status: response.status(), method: response.request().method(), path: url.pathname};
  if (response.status() < 300) {
    try {
      const body = await response.json();
      if (url.pathname === '/v1/playback/sessions' && response.request().method() === 'POST') sessionId = body.sessionId || body.id || sessionId;
      if (url.pathname.startsWith('/v1/playback/sessions')) entry.session = projectSession(body);
    } catch {}
  }
  if (response.status() >= 400) { try { const body = await response.json(); entry.errorCode = body?.error?.code; } catch {} }
  events.push(entry);
});

await page.goto(origin + '/sign-in', {waitUntil: 'domcontentloaded', timeout: 45000});
await page.locator('input[name=username]').first().waitFor({timeout: 45000});
await page.locator('input[name=username]').fill(creds.username);
await page.locator('input[type=password]').fill(creds.password);
await page.getByRole('button', {name: 'Sign in', exact: true}).click();
await page.waitForTimeout(3500);
if (/\/profiles/.test(page.url())) {
  await page.locator('button').filter({hasText: /Main profile/}).first().click({timeout: 8000});
  await page.waitForTimeout(3500);
}

await page.goto(origin + '/live', {waitUntil: 'domcontentloaded'});
await settle(page, {quiet: 3000});
const tunedAt = new Date().toISOString();
const card = page.locator('article[data-card-kind] button[aria-label]').first();
const tunedLabel = (await card.count()) ? await card.getAttribute('aria-label') : null;
if (await card.count()) { await card.focus(); await page.keyboard.press('Enter'); }

const samples = [];
let decodedAt = null;
const deadline = Date.now() + maxSeconds * 1000;
let previousTime = 0;
while (Date.now() < deadline) {
  await page.waitForTimeout(2000);
  const sample = await page.evaluate(at => {
    const video = document.querySelector('video');
    return {at, readyState: video?.readyState ?? null, videoWidth: video?.videoWidth ?? null,
      paused: video?.paused ?? null, currentTime: video ? Number(video.currentTime.toFixed(2)) : null,
      error: video?.error ? {code: video.error.code} : null,
      srcPrefix: video && video.currentSrc ? new URL(video.currentSrc).pathname.split('/').slice(0, 4).join('/') : null};
  }, new Date().toISOString());
  samples.push(sample);
  if (sample.readyState >= 2 && sample.videoWidth > 0 && !sample.paused && sample.currentTime > previousTime) { decodedAt = sample.at; break; }
  previousTime = sample.currentTime ?? previousTime;
}

// Cleanup: the drawn control if it exists, otherwise the exact DELETE with the creator's own headers.
let uiStop = 'no drawn stop control';
for (const pattern of [/^(Stop|Stop stream|Stop playing)$/i, /^(Close player|Close)$/i]) {
  const control = page.locator('button,[role=button]').filter({hasText: pattern}).first();
  if (await control.count()) { await control.click({timeout: 6000}).catch(() => {}); uiStop = 'drawn control clicked'; break; }
}
let deleteResult = null;
if (sessionId) {
  if (uiStop === 'no drawn stop control' && creatingHeaders) {
    deleteResult = await context.request.delete(`${origin}/v1/playback/sessions/${sessionId}`, {headers: creatingHeaders, timeout: 20000})
      .then(response => ({status: response.status()})).catch(error => ({error: String(error).slice(0, 120)}));
  } else if (!creatingHeaders) {
    deleteResult = {skipped: 'the creating request headers were not observed'};
  } else {
    deleteResult = {skipped: 'the drawn control was used first'};
  }
}
await page.screenshot({path: `${out}/probe.png`});

// creatingHeaders is deliberately absent from this object and from disk.
const summary = {tunedAt, tunedLabel, maxSeconds, decodedAt, uiStop, sessionId, deleteResult,
  sessionEvents: events.filter(e => e.path.startsWith('/v1/playback/sessions')),
  mediaRequests: events.filter(e => e.path.startsWith('/v1/media/linear')).length,
  samples};
writeFileSync(`${out}/rendered-live.json`, JSON.stringify(summary, null, 2));
await context.close();
await browser.close();
console.log(JSON.stringify({tunedAt, tunedLabel, decodedAt, uiStop, sessionId, deleteResult,
  mediaRequests: summary.mediaRequests, sampleTail: samples.slice(-3)}, null, 2));
