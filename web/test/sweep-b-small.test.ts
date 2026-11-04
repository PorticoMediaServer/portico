import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const read = (path: string) => readFileSync(new URL(path, import.meta.url), 'utf8');

test('Live TV names conflicts only when there is one (desktop-live: "Recordings 1 conflict")', () => {
  for (const file of ['../src/screens/live/GuideScreen.tsx', '../src/screens/live/SeriesRules.tsx'])
    assert.match(read(file), /conflicts !== undefined && conflicts > 0 && <span/, file);
});

test('a changed Advanced row with no readable value is named alone, never "Label:"', () => {
  assert.match(read('../src/kit/settings/SettingsRows.tsx'), /return value \? `\$\{label\}: \$\{value\}` : label;/);
});

test('Intros and credits: Find, the stats strip with a gap, Review, then Advanced last (desktop-settings 8)', () => {
  const intros = read('../src/screens/settings/server-intros/ServerIntros.tsx');
  assert.ok(intros.indexOf('{modeRows}') < intros.indexOf('<StatTiles') && intros.indexOf('<StatTiles') < intros.indexOf('segments.review') && intros.indexOf('segments.review') < intros.indexOf('{advancedRows &&'));
  assert.match(read('../src/screens/settings/Settings.route.tsx'), /advancedRows=\{renderRows\(\{advanced: selected\.section\.advanced\}\)\}/);
});
