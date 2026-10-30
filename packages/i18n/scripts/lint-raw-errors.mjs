#!/usr/bin/env node
// X-04 ratchet: raw core, server and browser error text must never reach the
// screen. Every failure goes through the shared presenter (`presentError` in
// client-core, `errorText` on web, `say` on Apple), which says catalogue copy
// by code and status. This lint fails on the obvious patterns:
//
//   - `.error.message` / `.error?.message` / `err.message` rendered in JSX
//     (`{snapshot.error?.message}`, `{m.error.message}`, …);
//   - `String(e|err|error)` in a render path (JSX/TSX or app TS);
//   - `setError(….message)` / `setProblem(….message)` and friends.
//
// A deliberate exception (logging, `__DEV__` diagnostics, dev-only screens)
// is marked with `lint-raw-errors-allow` on the line. Hits in files owned by
// another lane live in PENDING below with the reason; they print as warnings
// so this lane's `check` stays green, and a stale entry (the owner fixed it)
// prints as info. New hits anywhere else fail the run.
//
//   node packages/i18n/scripts/lint-raw-errors.mjs <app-src-dir>...
import {readdirSync, readFileSync, statSync} from 'node:fs';
import {join, relative, resolve} from 'node:path';

/** Hits another lane owns (or dev-only diagnostics): warn, don't fail. */
const strict = process.argv.includes('--strict');
const PENDING = [
  {file: 'screens/server/Libraries.tsx', pattern: 'error', reason: 'screens/server/** owned by the server-console lane (M21 Questions)'},
  {file: 'screens/server/Storage.tsx', pattern: 'error', reason: 'screens/server/** owned by the server-console lane (M21 Questions)'},
  {file: 'screens/server/LiveSourceExtras.tsx', pattern: 'e.message', reason: 'screens/server/** owned by the server-console lane (M21 Questions)'},
  {file: 'shell/CustodyPrompt.tsx', pattern: '.message', reason: 'shell/ is outside M21 screen ownership; exact fix in M21 results (Questions)'},
  {file: 'screens/dev/Features.tsx', pattern: 'String(e)', reason: 'dev-only diagnostics dump, not user-facing'},
  {file: 'dev/audio-conformance.ts', pattern: 'String(e)', reason: 'dev-only conformance diagnostics, not user-facing'},
  {file: 'ui/Artwork.tsx', pattern: 'String(', reason: 'image retry-slot diagnostic, never rendered as text'},
  {file: 'app/debug.ts', pattern: 'String(e)', reason: 'logging-only trace path'},
];

const JSX_MESSAGE = /\{[^}\n]*(\.error\?\.message|\.error\.message|\berr\.message|\be\.message\b)[^}\n]*\}/;
const STRING_ERR = /String\s*\(\s*(e|err|error)\b/;
/** `setError(….message)` and friends hand raw text to a notice. Other setters
 * (`setNote`, `setLogoResult`, …) carry success-path copy, not errors. */
const SETTER_MESSAGE = /set(Error|Problem|Failure|Notice|Toast)\([^()\n]*\.message\b/;

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(tsx?)$/.test(name) && !/\.test\.[tj]sx?$/.test(name) && !/\.d\.ts$/.test(name)) out.push(p);
  }
  return out;
}

const dirs = process.argv.slice(2).filter(a => !a.startsWith('--'));
if (!dirs.length) {
  console.error('usage: lint-raw-errors.mjs <app-src-dir>...');
  process.exit(2);
}

const failures = [];
const warnings = [];
const pendingSeen = new Set();
for (const dir of dirs) {
  const root = resolve(dir);
  for (const file of walk(root)) {
    const rel = relative(process.cwd(), file);
    const short = relative(root, file);
    const text = readFileSync(file, 'utf8');
    text.split('\n').forEach((line, i) => {
      if (!strict && line.includes('lint-raw-errors-allow')) return;
      const at = `${rel}:${i + 1}`;
      const checks = [];
      // P1 is JSX text: the line renders a tag. Logic lines that mention
      // `.error.message` (a throw, a diagnostic) are not user-facing text.
      if (file.endsWith('.tsx') && line.includes('<') && JSX_MESSAGE.test(line)) checks.push(`raw error.message in JSX (${line.trim().slice(0, 100)})`);
      if (STRING_ERR.test(line)) checks.push(`String(err) in render path (${line.trim().slice(0, 100)})`);
      if (SETTER_MESSAGE.test(line)) checks.push(`setError(….message) passes raw text (${line.trim().slice(0, 100)})`);
      for (const c of checks) {
        const pend = !strict && PENDING.find(p => short === p.file || short.endsWith('/' + p.file));
        if (pend && line.includes(pend.pattern)) {
          pendingSeen.add(pend.file);
          warnings.push(`pending ${at}: ${c} [${pend.reason}]`);
        } else {
          failures.push(`${at}: ${c}`);
        }
      }
    });
  }
}

for (const p of strict ? [] : PENDING) {
  if (!pendingSeen.has(p.file)) console.log(`info: pending entry stale (fixed?): ${p.file} [${p.reason}]`);
}
for (const w of warnings) console.warn(`warning ${w}`);
if (failures.length) {
  console.error(failures.join('\n'));
  console.error(`\n${failures.length} raw-error presentation hit(s): route them through presentError/errorText/say, or mark logging with lint-raw-errors-allow.`);
  process.exit(1);
}
console.log('raw-error presentation clean');
