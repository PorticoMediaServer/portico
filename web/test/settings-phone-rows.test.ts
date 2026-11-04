import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const shell = readFileSync(new URL('../src/kit/shell/settings.module.css', import.meta.url), 'utf8');
const server = readFileSync(new URL('../src/screens/settings/server/server.module.css', import.meta.url), 'utf8');
const phone = (css: string) => css.slice(css.lastIndexOf('@media (max-width: 599px) {'));

test('at phone width a row with a help line and a select, or a path value, stacks its control under the label', () => {
  const rules = phone(shell);
  assert.match(rules, /\.row:has\(> \.rowLabel > p\):has\(> \.rowControl \[role='combobox'\]\), \.row\[data-path\], \.row:has\(> \.rowControl :is\(code, pre\)\) \{ grid-template-columns: minmax\(0, 1fr\);/);
  // Nothing truncates a select's value or forces a share on short rows.
  assert.doesNotMatch(rules, /minmax\(45%/);
});

test('a long path in the Storage usage list wraps inside the panel', () => {
  assert.match(server, /\.storageText small \{[^}]*overflow-wrap: anywhere;/);
});

test('a path row (Back up to, Data folder) is stacked from the row, so "Loading…" and the path share one layout', () => {
  const rowsSource = readFileSync(new URL('../src/kit/settings/SettingsRows.tsx', import.meta.url), 'utf8');
  assert.match(rowsSource, / path=\{c\.type === 'folder' \|\| c\.type === 'readonly' && c\.mono === true\}/);
  const surfaces = readFileSync(new URL('../src/kit/shell/SettingsSurfaces.tsx', import.meta.url), 'utf8');
  assert.match(surfaces, /data-path=\{path \|\| undefined\}/);
  const spec = readFileSync(new URL('../../packages/client-core/src/presentation/settings-spec.ts', import.meta.url), 'utf8');
  for (const [id, type] of [['storage.dataFolder', 'readonly'], ['signin.dataFolder', 'readonly'], ['storage.backupFolder', 'folder']]) {
    const at = spec.indexOf(`"id": "${id}"`);
    assert.ok(at > 0, id);
    assert.match(spec.slice(at, at + 600), new RegExp(`"type": "${type}"`), id);
  }
});
