import test from 'node:test';
import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import {join, relative} from 'node:path';

// React Native's AbortSignal has no throwIfAborted, so calling the method in shared code stops
// that screen on phones and televisions with a TypeError. Shared code calls the helper
// throwIfAborted(signal) from abort-lifetime.ts instead. These files run only in a browser.
const browserOnly = /(^|\/)(query\/browser-account-[^/]+|original-browser-read-view|web-capacity-retry)\.ts$/;
const root = join(import.meta.dirname, '..', 'src');
const files = (dir: string): string[] => readdirSync(dir, {withFileTypes: true}).flatMap(entry =>
  entry.isDirectory() ? files(join(dir, entry.name)) : /\.tsx?$/.test(entry.name) ? [join(dir, entry.name)] : []);

test('shared client code never calls a signal\'s own throwIfAborted', () => {
  const found = files(root).map(file => relative(root, file)).filter(file => !browserOnly.test(file))
    .flatMap(file => readFileSync(join(root, file), 'utf8').split('\n')
      .flatMap((line, index) => /[\w)\]?]\s*\.\s*throwIfAborted\s*\(/.test(line) ? [`${file}:${index + 1}`] : []));
  assert.deepEqual(found, []);
});
