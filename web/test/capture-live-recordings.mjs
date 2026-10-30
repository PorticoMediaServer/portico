// Remote capture pairs for desktop-live.html#live-upcoming and #live-recorded.
// The DEV specimen carries the drawn synthetic fixture; the reference comes from
// the exact published design source. No server data or credentials.
import {spawn, spawnSync} from 'node:child_process';
import {existsSync} from 'node:fs';
import {mkdir, readFile, writeFile} from 'node:fs/promises';
import {join, resolve} from 'node:path';
import {connect} from 'node:net';

const output = process.env.PORTICO_RUNNER_OUTPUT, reference = process.env.PORTICO_RUNNER_SECONDARY_SOURCE;
if (!output || !reference || !process.env.TMPDIR) throw new Error('Run on the external runner with --browser and the exact design secondary source.');
const origin = 'http://127.0.0.1:5188', referenceOrigin = 'http://127.0.0.1:5199';
const chromium = ['/usr/bin/chromium-browser', '/usr/bin/chromium', '/snap/bin/chromium', '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable'].find(path => existsSync(path));
if (!chromium) throw new Error('Chromium is not installed on this runner.');
const sleep = ms => new Promise(done => setTimeout(done, ms));
const ready = port => new Promise(done => {const socket = connect({port, host: '127.0.0.1'});socket.once('connect', () => {socket.destroy();done(true);});socket.once('error', () => done(false));});
const server = spawn(process.execPath, [resolve('node_modules/vite/bin/vite.js'), '--host', '127.0.0.1', '--port', '5188'],
  {cwd: resolve('web'), detached: true, stdio: ['ignore', 'pipe', 'pipe']});
let log = '';
server.stdout.on('data', data => {log += data;});server.stderr.on('data', data => {log += data;});
let http;
try {
  for (let attempt = 0; ; attempt++) {
    if (await ready(5188)) break;
    if (attempt > 150 || server.exitCode !== null) throw new Error(`Vite did not start: ${log}`);
    await sleep(100);
  }
  const root = join(process.env.TMPDIR, 'live-recordings-reference');await mkdir(root, {recursive: true});
  const source = await readFile(`${reference}/design/ui-v1/desktop-live.html`, 'utf8');
  const kit = await readFile(`${reference}/design/ui-v1/kit.css`, 'utf8');
  const css = source.match(/<style>([\s\S]*?)<\/style>/)[1];
  const sprite = [...source.matchAll(/<svg width="0" height="0" style="position:absolute">[\s\S]*?<\/svg>/g)].map(match => match[0]).join('');
  const frames = {
    upcoming: source.slice(source.indexOf('<div class="screen" id="live-upcoming"'), source.indexOf('<p class="cap"><b>Recordings: Recorded')),
    recorded: source.slice(source.indexOf('<div class="screen" id="live-recorded"'), source.indexOf('<p class="cap"><b>Series rules')),
  };
  for (const [name, frame] of Object.entries(frames)) {
    await writeFile(join(root, `${name}.html`), `<!doctype html><html><head><meta charset="utf-8"><link rel="stylesheet" href="${origin}/src/kit/fonts.css"><style>${kit}\n${css}\nbody{margin:0;padding:0}</style></head><body class="p">${sprite}${frame.replaceAll('assets/wordmark.svg', origin + '/src/shell/wordmark.svg')}</body></html>`);
  }
  http = spawn('python3', ['-m', 'http.server', '5199', '--bind', '127.0.0.1', '--directory', root], {stdio: ['ignore', 'ignore', 'pipe']});
  for (let attempt = 0; ; attempt++) {
    if (await ready(5199)) break;
    if (attempt > 100 || http.exitCode !== null) throw new Error('Reference server did not start');
    await sleep(100);
  }
  const shot = (url, file) => {
    const result = spawnSync(chromium, ['--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--hide-scrollbars',
      '--run-all-compositor-stages-before-draw', '--virtual-time-budget=8000', '--window-size=1440,1230', `--screenshot=${file}`, url],
      {encoding: 'utf8', timeout: 60_000, env: {...process.env, TZ: 'America/Halifax'}});
    if (result.status !== 0) throw new Error(`Chromium failed for ${url}: ${result.stderr?.slice(-2000) ?? ''}`);
  };
  for (const name of ['upcoming', 'recorded']) {
    shot(`${origin}/src/dev/entries/live-recordings.html?view=${name}`, `${output}/${name}-implementation.png`);
    shot(`${referenceOrigin}/${name}.html`, `${output}/${name}-frame.png`);
    const pair = spawnSync('ffmpeg', ['-y', '-loglevel', 'error', '-i', `${output}/${name}-implementation.png`, '-i', `${output}/${name}-frame.png`,
      '-filter_complex', 'hstack=inputs=2', `${output}/${name}-comparison.jpg`], {encoding: 'utf8', timeout: 60_000, env: {...process.env, TZ: 'America/Halifax'}});
    if (pair.status !== 0) throw new Error(`ffmpeg failed: ${pair.stderr?.slice(-1000) ?? ''}`);
  }
  await writeFile(`${output}/capture.log`, JSON.stringify({origin, referenceOrigin, screens: ['upcoming', 'recorded'], viewport: '1440x1230'}, null, 2));
  console.log('LIVE_RECORDINGS_CAPTURE_OK');
} finally {
  http?.kill('SIGTERM');
  try {process.kill(-server.pid, 'SIGTERM');} catch {}
  await new Promise(done => {if (server.exitCode !== null) done();else server.once('exit', done);});
  await writeFile(`${output}/vite.log`, log);
}
