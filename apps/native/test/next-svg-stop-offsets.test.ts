/**
 * react-native-svg reads a string stop offset only with a leading digit: ".7" parses as 0, so a
 * fade collapses to a hard edge or a flat fill (37 such stops, fixed in cde21e338). Numbers are
 * fine (`offset={.62}`); strings must be written "0.7".
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import {existsSync, readdirSync, readFileSync} from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const app = fileURLToPath(new URL('..', import.meta.url));
const sources = (dir: string): string[] => existsSync(path.join(app, dir)) ? readdirSync(path.join(app, dir), {withFileTypes: true}).flatMap(entry => {
  const relative = path.join(dir, entry.name);
  return entry.isDirectory() ? sources(relative) : /\.tsx?$/.test(entry.name) ? [relative] : [];
}) : [];

/** A string attribute, or a string literal anywhere in an `offset={…}` expression, that starts with a bare decimal point. */
const bare = /\boffset=(?:["']\s*[+-]?\.\d|\{[^}]*?["'`]\s*[+-]?\.\d)/g;
const findings = (source: string) => source.split('\n').flatMap((line, at) => [...line.matchAll(bare)].map(() => at + 1));

test('the guard catches every way a bare-decimal offset has been written', () => {
  for (const sample of ['<Stop offset=".7"/>', "<Stop offset='.5'/>", "<Stop offset={isTV ? '.56' : '0.5'}/>", '<Stop offset={`.4`}/>', '<Stop offset="-.2"/>'])
    assert.deepEqual(findings(sample), [1], sample);
  for (const sample of ['<Stop offset="0.7"/>', '<Stop offset={.62}/>', "<Stop offset={isTV ? '0.56' : '0.5'}/>", '<Stop offset="70%"/>', 'scrollToOffset({offset: 0})', 'offset={snapshot.songPage.offset}'])
    assert.deepEqual(findings(sample), [], sample);
});

test('no native gradient stop offset is a string without a leading digit', () => {
  const files = [...sources('src-next'), ...sources('../vega/src')];
  assert.ok(files.length > 100, 'the scan found the native sources');
  const hits = files.flatMap(file => findings(readFileSync(path.join(app, file), 'utf8')).map(line => `${file}:${line}`));
  assert.deepEqual(hits, [], `write string offsets as "0.N":\n${hits.join('\n')}`);
});
