#!/usr/bin/env node
/**
 * X-01: every server preference key must have a runtime consumer.
 *
 * Fails (exit 1) when a registry key has no reader outside the registry
 * itself, the label/copy files and tests. Keys come from the checked-in
 * registry source (`server/internal/operations/preferences.go`):
 * there is no OpenAPI enum of preference keys (verified: `server/api`
 * mentions preferences only in prose), so the registry source is the list.
 *
 * Readers count in client code (`web/src`,
 * the native app's `apps/native/src` (portico-react-native), `packages/client-core/src`, `packages/i18n/src`)
 * and in server code (`server/internal`): a key enforced
 * server-side (delivery policy, personal activity, search history, home
 * layout) has a consumer even when no client reads it.
 *
 * Excluded (never readers):
 * - the registry file itself,
 * - label/copy files (`preference-copy.ts`, `preference-labels.ts`,
 *   `catalog/en-US/preferences.ts`),
 * - tests (`*.test.*`, `test/` directories),
 * - exclusion-list literals (`notOnApple`, `managedElsewhere` sets name keys
 *   precisely because nothing reads them),
 * - this script.
 *
 * `EXCEPTIONS` are keys with no reader yet. They warn but do not fail, so the ratchet
 * still catches every new unread key.
 */
import {existsSync, readFileSync, readdirSync, statSync} from 'node:fs';
import {join, relative, resolve, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '..', '..', '..');

const REGISTRY = join(root, 'server/internal/operations/preferences.go');
// The native app reads preferences too; it lives in the portico-react-native checkout
// beside this one. Its readers are counted when that checkout is there. When it is not, the
// check still runs over this repository: a key that only the native app reads then fails
// with a pointer to PORTICO_REACT_NATIVE_REPO, which is a visible answer, not a skipped check.
const nativeRepo = process.env.PORTICO_REACT_NATIVE_REPO;
const nativeSource = resolve(nativeRepo ?? join(root, '..', 'portico-react-native'), 'apps/native/src');
const SEARCH_ROOTS = [
  'web/src',
  'packages/client-core/src',
  'packages/i18n/src',
  'server/internal',
];


/** Keys with no reader that another lane must resolve (key: reason). None today. */
export const EXCEPTIONS = {};

/** The two saved default qualities (`quality.home`, `quality.remote`) are read
 * through a prefix and the name of the network class, never as literals. These
 * files contain the prefix read and cover both. */
const PREFIX_READERS = [
  {prefix: 'quality.', files: ['server/internal/playback/delivery_policy.go', 'server/internal/httpapi/playback_v1_routes.go']},
];

export function extractRegistryKeys(goSource) {
  const keys = new Set();
  for (const m of goSource.matchAll(/pref\w+\("([^"]+)"/g)) keys.add(m[1]);
  return [...keys].sort();
}

export function isExcludedFile(rel) {
  return (
    rel === 'server/internal/operations/preferences.go' ||
    rel.endsWith('preference-copy.ts') ||
    rel.endsWith('preference-labels.ts') ||
    rel.endsWith('catalog/en-US/preferences.ts') ||
    rel.endsWith('check-preference-consumers.mjs') ||
    rel.includes('/test/') ||
    /(^|\/)(test|tests)(\/|$)/.test(rel) ||
    /\.test\.[mc]?[tj]sx?$/.test(rel) ||
    /_test\.go$/.test(rel)
  );
}

export function isExcludedLine(line) {
  if (line.includes('notOnApple') || line.includes('managedElsewhere')) return true;
  const trimmed = line.trim();
  // Comments never read a preference.
  if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*') || trimmed.startsWith('#')) return true;
  // Bare list literals (`'a.b', 'c.d',` in an exclusion set) name keys
  // precisely because nothing reads them.
  const stripped = line.replace(/'[^']*'|"[^"]*"|`[^`]*`/g, '');
  return /^[\s,\[\]();{}]*$/.test(stripped);
}

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.(go|[mc]?[tj]sx?)$/.test(name)) out.push(p);
  }
  return out;
}

export function findReaders(keys, files) {
  const hits = new Map(keys.map(k => [k, []]));
  const contents = new Map();
  for (const file of files) {
    let text;
    try { text = readFileSync(file, 'utf8'); } catch { continue; }
    contents.set(file, text);
  }
  for (const key of keys) {
    for (const [file, text] of contents) {
      if (!text.includes(key)) continue;
      const rel = relative(root, file);
      if (isExcludedFile(rel)) continue;
      const lines = text.split('\n');
      const at = [];
      lines.forEach((line, i) => {
        if (line.includes(key) && !isExcludedLine(line)) at.push(i + 1);
      });
      if (at.length) hits.get(key).push(`${rel}:${at[0]}`);
    }
  }
  // Dynamic lane-prefix reads cover their whole group.
  for (const {prefix, files: readers} of PREFIX_READERS) {
    const present = readers.filter(f => {
      const text = contents.get(join(root, f));
      return text && text.includes(prefix);
    });
    if (!present.length) continue;
    for (const key of keys) {
      if (key.startsWith(prefix) && !hits.get(key).length) hits.set(key, present.map(f => `${f} (lane prefix)`));
    }
  }
  return hits;
}

function main() {
  const native = existsSync(nativeSource);
  if (!native && nativeRepo !== undefined) {
    console.error(`FAIL: PORTICO_REACT_NATIVE_REPO is set, but there is no native app source at ${nativeSource}.`);
    process.exit(1);
  }
  if (!native) console.log(`note: no portico-react-native checkout at ${dirname(dirname(dirname(nativeSource)))}; checking this repository’s readers only (set PORTICO_REACT_NATIVE_REPO to count the native app’s).`);
  const go = readFileSync(REGISTRY, 'utf8');
  const keys = extractRegistryKeys(go);
  const [web, ...rest] = SEARCH_ROOTS.map(r => join(root, r));
  const files = [web, ...(native ? [nativeSource] : []), ...rest].flatMap(dir => walk(dir));
  const hits = findReaders(keys, files);
  const missing = [];
  const excused = [];
  for (const key of keys) {
    if (hits.get(key).length) continue;
    if (key in EXCEPTIONS) excused.push(key);
    else missing.push(key);
  }
  for (const [file, lines] of hits) {
    if (lines.length) console.log(`ok   ${file} <- ${lines.slice(0, 3).join(', ')}${lines.length > 3 ? ` (+${lines.length - 3})` : ''}`);
  }
  for (const key of excused) console.log(`warn ${key} (exception: ${EXCEPTIONS[key]})`);
  if (missing.length) {
    console.error(`\nFAIL: ${missing.length} preference key(s) with no reader outside the registry, label files and tests:`);
    for (const key of missing) console.error(`  - ${key}`);
    if (!native) console.error('If only the native app reads one of these, run again with PORTICO_REACT_NATIVE_REPO set to its checkout.');
    process.exit(1);
  }
  console.log(`\nPASS: ${keys.length} keys, ${excused.length} documented exception(s).`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
