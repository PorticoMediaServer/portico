import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {railPitch} from '../src/kit/layout/geometry.ts';

const css = readFileSync(new URL('../src/screens/browse/browse.module.css', import.meta.url), 'utf8');

// J-4: desktop-browse draws the rail 18px in from the window edge, its # and A–Z rows at 21px.
test('J-4: the desktop rail sits 18px from the window edge in the page gutter at every width', () => {
  // 64px shell gutter at 1440/1920 and 32px at 1024: 18 - gutter puts the right edge 18px in, where
  // the old fixed -46px pushed up to 14px of the rail (and its band) off-screen below 1440.
  // W2-A3: the gutter the rail measures from is never under 56px, so the grid keeps its room.
  assert.match(css, /\.rail \{ position: absolute; right: calc\(18px - max\(var\(--shell-gutter, var\(--gutter-desktop\)\), 56px\) \+ var\(--grid-overflow, 0px\)\); top: 8px; \}/);
});

test('W2-A3: a grid that draws the rail keeps at least 56px on its right, 10px clear of the rail at 1024', () => {
  assert.match(css, /\.results:has\(> \.rail\) \{ margin-right: calc\(max\(var\(--shell-gutter, var\(--gutter-desktop\)\), 56px\) - var\(--grid-overflow, 0px\)\); \}/);
  assert.match(css, /\.results:has\(> \.rail\) \{ margin-right: calc\(44px - var\(--grid-overflow, 0px\)\); \} \.rail \{ right: calc\(-36px \+ var\(--grid-overflow, 0px\)\); \}/, 'medium widths keep the rail 8px clear');
});

test('J-4: the rail keeps 21px rows whenever the window holds # and A–Z', () => {
  assert.equal(railPitch(567, 27), 21, 'the drawn 27-row rail fits exactly');
  assert.equal(railPitch(760, 27), 21, 'a taller window keeps the drawn rows');
  assert.equal(railPitch(400, 27), 15, 'a short window tightens no further than 15px');
});

test('J-4: extra script keys tighten the rail so its last letter stays in the window', () => {
  const room = 564; // the space below the toolbar in a 900px-tall window at the rail's top
  const pitch = railPitch(room, 33);
  assert.ok(pitch > 15 && pitch < 21, `33 keys tighten the pitch (${pitch})`);
  assert.ok(pitch * 33 <= room + .01, 'the whole alphabet, extra keys included, stays in the window');
});
