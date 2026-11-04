import assert from 'node:assert/strict';
import test from 'node:test';
import {readFileSync} from 'node:fs';

const panel = readFileSync(new URL('../src/screens/settings/libraries/LibrariesPanel.tsx', import.meta.url), 'utf8');
const css = readFileSync(new URL('../src/screens/settings/libraries/libraries.module.css', import.meta.url), 'utf8');

test('a library row is its covers, name, counts and one state line, with one ⋯ menu (owner-libraries 1, 24a)', () => {
  // No inline Scan now / Edit buttons crowd the row (they wrapped the name letter by letter at 390).
  assert.doesNotMatch(panel, /<Button variant="glass" size="small"[^>]*onClick=\{\(\) => \{ const library = libraries\.find/);
  assert.doesNotMatch(panel, /onClick=\{\(\) => \{if \(!lifetime\.current\?\.running\) onEdit\(row\.id\);\}\}>/);
  // The menu holds them, in the frame's order; removing a library lives in its panel.
  const order = ["{id: 'edit'", "{id: 'scan'", "{id: 'unmatched'", "{id: 'up'", "{id: 'down'"].map(key => panel.indexOf(key));
  assert.ok(order.every(at => at > 0), 'every drawn menu item is present');
  assert.deepEqual([...order].sort((a, b) => a - b), order, 'menu order');
  assert.doesNotMatch(panel, /\{id: 'remove'/);
  // The spec keeps the list to short facts: counts yes, the folder path no.
  assert.match(panel, /summary\?\.counts/); assert.match(panel, /summary\?\.coverUrls/);
  assert.doesNotMatch(panel, /\{row\.folder\}/);
  assert.match(panel, /next\.ownerLibraries\.scannedToday/); assert.match(panel, /next\.ownerLibraries\.scannedYesterday/);
  // A narrow list (phone, or 1024 beside the settings menu): the state is the last line; a wide one: at the right.
  assert.match(css, /@container \(min-width: 680px\) \{\n  \.identity \{ display: grid;/);
  assert.match(css, /container-type: inline-size;/);
  assert.match(css, /\.identity > \.state \{ grid-column: 2;/);
});
