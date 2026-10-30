/**
 * X-01: the preference-consumer ratchet. The check script fails when a
 * registry key has no reader outside the registry, label files and tests.
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import {spawnSync} from 'node:child_process';
import {mkdirSync, mkdtempSync, writeFileSync} from 'node:fs';
import {join} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import {extractRegistryKeys, findReaders, isExcludedFile, isExcludedLine, EXCEPTIONS} from '../scripts/check-preference-consumers.mjs';

const GO = `
var preferenceRegistry = func() []PreferenceField {
  out := []PreferenceField{
    prefBool("appearance.showBackdrops", true, ScopeDeviceClass),
    prefBool("notifications.badges", true, ScopeProfileServer),
    prefText("region.locale", "en-US", 35, ScopeProfileServer),
    prefEnum("quality.home", "original", SavedQualities, ScopeDeviceClass),
    prefEnum("quality.remote", "original", SavedQualities, ScopeDeviceClass),
  }
  return out
}();
`;

test('X-01: registry keys are extracted, the two saved qualities among them', () => {
  const keys = extractRegistryKeys(GO);
  assert.ok(keys.includes('appearance.showBackdrops'));
  assert.ok(keys.includes('notifications.badges'));
  assert.ok(keys.includes('region.locale'));
  assert.deepEqual(keys.filter(k => k.startsWith('quality.')), ['quality.home', 'quality.remote']);
});

test('X-01: the registry, label files, tests and exclusion lists are not readers', () => {
  assert.equal(isExcludedFile('server/internal/operations/preferences.go'), true);
  assert.equal(isExcludedFile('packages/client-core/src/presentation/preference-copy.ts'), true);
  assert.equal(isExcludedFile('packages/i18n/src/catalog/en-US/preferences.ts'), true);
  assert.equal(isExcludedFile('server/internal/operations/preferences_test.go'), true);
  assert.equal(isExcludedFile('server/internal/catalog/foo_test.go'), true);
  assert.equal(isExcludedFile('web/test/x.test.ts'), true);
  assert.equal(isExcludedFile('web/src/shell/Shell.tsx'), false);
  assert.equal(isExcludedLine("  'appearance.showBackdrops', 'appearance.cardSizePercent',"), true);
  assert.equal(isExcludedLine(' * backdrops (`appearance.showBackdrops`) are off'), true);
  assert.equal(isExcludedLine("  const backdrops = server.value('appearance.showBackdrops', true);"), false);
  assert.equal(isExcludedLine('  mode := values.Text("playback.subtitleMode")'), false);
});

test('X-01: a key read only in an exclusion list has no reader; a value() read counts', () => {
  const dir = mkdtempSync(join(tmpdir(), 'm9-pref-'));
  const listOnly = join(dir, 'SettingsPages.tsx');
  writeFileSync(listOnly, `const notOnApple = new Set([\n  'appearance.showBackdrops',\n]);\n`);
  const reader = join(dir, 'Shell.tsx');
  writeFileSync(reader, `const backdrops = server.value('appearance.showBackdrops', true);\n`);
  const keys = ['appearance.showBackdrops', 'notifications.badges'];
  const listHits = findReaders(keys, [listOnly]);
  assert.deepEqual(listHits.get('appearance.showBackdrops'), []);
  const both = findReaders(keys, [listOnly, reader]);
  assert.equal(both.get('appearance.showBackdrops')!.length, 1);
  assert.deepEqual(both.get('notifications.badges'), []);
});

test('X-01: only the documented blocked keys may lack readers', () => {
  assert.deepEqual(Object.keys(EXCEPTIONS).sort(), []);
});

const script = fileURLToPath(new URL('../scripts/check-preference-consumers.mjs', import.meta.url));
const run = (native: string | undefined) => {
  const env = {...process.env};
  if (native === undefined) delete env.PORTICO_REACT_NATIVE_REPO; else env.PORTICO_REACT_NATIVE_REPO = native;
  return spawnSync(process.execPath, [script], {env, encoding: 'utf8'});
};

test('X-01: the check runs over the real registry, with or without the native checkout beside it', () => {
  const result = run(undefined);
  assert.doesNotMatch(result.stdout, /skipped/, 'a missing native checkout must not turn the check off');
  assert.match(result.stdout, /PASS: \d+ keys/);
  assert.equal(result.status, 0, result.stderr);
  // Every registry key was looked up, not an empty list.
  assert.ok(Number(/PASS: (\d+) keys/.exec(result.stdout)![1]) >= 30);
});

test('X-01: a native checkout that was named but is not there fails the check', () => {
  const missing = join(mkdtempSync(join(tmpdir(), 'pref-native-')), 'nowhere');
  const result = run(missing);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /PORTICO_REACT_NATIVE_REPO/);
});

test('X-01: the native app’s source is read where the app lives', () => {
  const repo = mkdtempSync(join(tmpdir(), 'pref-native-'));
  mkdirSync(join(repo, 'apps/native/src'), {recursive: true});
  const result = run(repo);
  assert.equal(result.status, 0, result.stderr);
  assert.doesNotMatch(result.stdout, /this repository’s readers only/);
});
