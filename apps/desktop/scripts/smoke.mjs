// Starts the debug build once and checks that the web app got past its boot screen to the server
// chooser with no page errors (src-tauri/src/main.rs, mod smoke). Used by CI on each system.
import {spawn} from 'node:child_process';
import {existsSync, readFileSync, rmSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';

const binary = process.argv[2], wrapper = process.argv.slice(3);
const report = join(process.env.RUNNER_TEMP ?? tmpdir(), 'portico-desktop-smoke.json');
rmSync(report, {force: true});
const [command, ...args] = wrapper.length ? [...wrapper, binary] : [binary];
const child = spawn(command, args, {stdio: 'inherit', env: {...process.env, PORTICO_DESKTOP_SMOKE: report}});
const timer = setTimeout(() => child.kill('SIGKILL'), 120_000);
child.on('exit', code => {
  clearTimeout(timer);
  if (!existsSync(report)) { console.error(`no report (exit ${code})`); process.exit(1); }
  const result = JSON.parse(readFileSync(report, 'utf8'));
  console.log(JSON.stringify(result, null, 2));
  // An installed build must find the mpv it ships (PORTICO_EXPECT_MPV).
  const mpv = !process.env.PORTICO_EXPECT_MPV || result.mpv === true;
  process.exit(result.ready && result.path === '/servers' && result.errors.length === 0 && mpv ? 0 : 1);
});
