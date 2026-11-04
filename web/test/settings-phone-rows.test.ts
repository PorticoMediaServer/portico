import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const shell = readFileSync(new URL('../src/kit/shell/settings.module.css', import.meta.url), 'utf8');
const server = readFileSync(new URL('../src/screens/settings/server/server.module.css', import.meta.url), 'utf8');
const phone = (css: string) => css.slice(css.lastIndexOf('@media (max-width: 599px) {'));

test('at phone width a row with a help line and a select, or a path value, stacks its control under the label', () => {
  const rules = phone(shell);
  assert.match(rules, /\.row:has\(> \.rowLabel > p\):has\(> \.rowControl \[role='combobox'\]\), \.row:has\(> \.rowControl :is\(code, pre\)\) \{ grid-template-columns: minmax\(0, 1fr\);/);
  // Nothing truncates a select's value or forces a share on short rows.
  assert.doesNotMatch(rules, /minmax\(45%/);
});

test('a long path in the Storage usage list wraps inside the panel', () => {
  assert.match(server, /\.storageText small \{[^}]*overflow-wrap: anywhere;/);
});
