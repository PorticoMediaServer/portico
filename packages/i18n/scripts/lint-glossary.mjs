#!/usr/bin/env node
// Product Language glossary lint (CON-20, CON-21). Fails when an "avoid" term
// from Consistency §5.1 (§5.2 string list, CON-21 engineering words, CON-17
// retry verbs) appears in an en-US catalogue *value* (US spelling throughout:
// "Favorites", "Customize", "Minimize").
//
//   node packages/i18n/scripts/lint-glossary.mjs
//
// Only en-US values are checked (en-CA legitimately uses "Favourites") and
// only values, never keys (`retry` in a key like `title.retry` is fine — its
// value is "Try again"). A line with `lint-glossary-allow` is skipped; the
// ALLOW map below documents the legitimate uses with their reasons. Single
// common words from the Avoid column (room, session, host, lane as a bare
// word in prose, Activate, receivers…) are deliberately NOT substring-linted:
// they are too broad and are enforced by review against the registry instead.
import {readFileSync, readdirSync} from 'node:fs';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const catalogDir = resolve(here, '../src/catalog');

// [pattern, concept] — pattern matches a regression to an avoid term.
const AVOIDS = [
  [/Favourites|favourites/, 'Favorites (US spelling)'],
  [/Customise|customise/, 'Customize Home (US spelling)'],
  [/Switch server or profile/, 'Switch profile or server'],
  [/Connect to a server directly/, 'Sign in directly to a server'],
  [/Direct sign-in/, 'server account (not "Direct sign-in" as a status)'],
  [/\bLocal account\b|\bdirect account\b/i, 'server account'],
  [/View details/, 'Details'],
  [/Playback info/, 'Media information'],
  [/Cast to a television|Play on another device/, 'Play on'],
  [/AirPlay and speakers|Chromecast/i, 'AirPlay / Google Cast'],
  [/Link a TV with a code/, 'Enter a TV code (removed 22 Sep; never a code to type)'],
  [/Direct play/, 'Automatic / Original quality choices'],
  [/\bTranscode\b/, 'Allow conversion (not Transcode)'],
  [/Convert to|CONVERT TO/, 'quality rungs (not Convert to)'],
  [/Prepared copy/i, 'Saved version (not Prepared copy)'],
  [/Source file/, 'Original (not Source file)'],
  [/Lane:/, 'network class (Home network / Away from home)'],
  [/two-factor/i, 'two-step verification'],
  [/\b2FA\b|\bMFA\b/, 'two-step verification'],
  [/\bRetry\b/, 'Try again (retry verb)'],
  [/Try now/, 'Try again (retry verb)'],
  [/\bReload\b/, 'Refresh (reload of changed content) / Try again (retry)'],
  [/Retry shortly/, 'Try again (retry verb)'],
  [/\bslower\b/i, 'no "slower" hint (CON-04: delete the meta)'],
  [/\bprobe\b/i, 'Read file details (not Probe)'],
  [/\bscope\b/i, 'engineering word (CON-21)'],
  [/evidence records/i, 'Server logs (not Evidence records)'],
  [/in this build/, 'drop "in this build" (CON-21)'],
  [/\blane\b/i, 'library (not lane)'],
  [/\bLog in\b|\blogin\b|\bLog out\b/i, 'sign in / sign out'],
  [/On Deck/, 'Continue Watching / Up Next'],
  [/Technical details/, 'Media information (title page only; console uses are allowed)'],
  [/\bLoad more\b/, 'Show more'],
  [/Earlier seasons…|More seasons…/, 'no ellipsis (they page, they don’t ask)'],
  [/Show from the start/, 'Back to the start'],
  [/smart playlist/i, 'Saved view'],
  [/\bViews\b/, 'Saved views (tab)'],
  [/watch list|bookmarks/i, 'Watchlist'],
];

// Legitimate uses: key substring → reason. Kept narrow; everything else fails.
const ALLOW = [
  // Canonical Accounts model, 22 Oct 2026: local account is the explicit sign-in label.
  ['accounts.session.local', 'AC-C2 binding Local account sign-in label'],
  ['accounts.consent.local', 'AC-C3 same Local account TV sign-in consent'],
  // First-run fallback server names (glossary: "except first-run").
  ['servers.fallbackName', '"Portico server" first-run fallback name'],
  ['web.servers.fallbackName', '"Portico server" first-run fallback name'],
  // Browser page reloads, not retry verbs (CON-17).
  ['web.signIn.error.update', 'browser "Reload the page"'],
  ['web.signIn.error.bug', 'browser "Reload the page"'],
  ['web.signIn.error.accountBug', 'browser "Reload the page"'],
  ['web.signIn.error.reload', 'browser "Reload it"'],
  // Console / feedback sections, not the title page (glossary scopes the avoid to the title page).
  ['feedback.diagnostics', 'Report-a-problem diagnostics toggle'],
  ['server.technicalDetails', 'console section label'],
  ['web.capabilities.technical', 'console section label'],
  ['web.capabilities.reason.other', 'console pointer to technical details'],
  // Server-console delivery statuses; lead decision pending (M6 Q2).
  ['web.nowPlaying.delivery.directPlay', 'console delivery status, pending lead'],
  ['web.nowPlaying.delivery.directStream', 'console delivery status, pending lead'],
  // Player options info row; lead decision pending (M6 Q1).
  ['player.opt.info', 'player options row, pending lead'],
  // Count noun for device sessions, not "session" as a device synonym.
  ['web.devices.sessions', '"sessions" count noun'],
  // DVR conversion-mode option label (owner console), not a player quality rung.
  ['web.dvrSettings.convertSmaller', 'DVR conversion option, pending strings-lane review if CON-21 is extended'],
];

function catalogFiles() {
  const out = [join(catalogDir, 'en-US.ts')];
  for (const name of readdirSync(join(catalogDir, 'en-US'))) {
    if (name.endsWith('.ts')) out.push(join(catalogDir, 'en-US', name));
  }
  return out;
}

// Matches `'message.id': 'value',` pairs (catalogue is `as const satisfies`).
const entry = /'([^']+)'\s*:\s*'((?:[^'\\]|\\.)*)'/g;

function main() {
  const failures = [];
  for (const file of catalogFiles()) {
    const lines = readFileSync(file, 'utf8').split('\n');
    lines.forEach((line, i) => {
      if (line.includes('lint-glossary-allow')) return;
      entry.lastIndex = 0;
      let m;
      while ((m = entry.exec(line)) !== null) {
        const [, key, value] = m;
        if (ALLOW.some(([sub]) => key.includes(sub))) continue;
        for (const [rx, concept] of AVOIDS) {
          if (rx.test(value)) {
            failures.push({key, value: value.slice(0, 80), concept, loc: `${file.split('catalog/')[1]}:${i + 1}`});
            break;
          }
        }
      }
    });
  }
  if (failures.length) {
    for (const f of failures) console.error(`glossary: ${f.loc} ${f.key} → "${f.value}" (avoid; use: ${f.concept})`);
    console.error(`glossary lint failed: ${failures.length} avoid term(s) in en-US values`);
    return 1;
  }
  console.log('glossary lint passed: no avoid terms in en-US values');
  return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) process.exit(main());

export {AVOIDS, ALLOW};
