import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {enUS} from '../../packages/i18n/src/catalog/en-US.ts';

test('File info and Filters close with the mock-up\'s own names and 22px glyph', () => {
  assert.equal(enUS['next.overlays.closeFileInfo'], 'Close file info');
  assert.equal(enUS['next.browse.closeFilters'], 'Close filters');
  const fileInfo = readFileSync(new URL('../src/kit/overlays/FileInfo.tsx', import.meta.url), 'utf8');
  assert.match(fileInfo, /closeIconSize=\{22\} closeLabel=\{t\('next\.overlays\.closeFileInfo'\)\}/);
  const filters = readFileSync(new URL('../src/screens/browse/Filters.tsx', import.meta.url), 'utf8');
  assert.match(filters, /closeIconSize=\{22\} closeLabel=\{t\('next\.browse\.closeFilters'\)\}/);
  const dialog = readFileSync(new URL('../src/kit/overlays/Dialog.tsx', import.meta.url), 'utf8');
  assert.match(dialog, /aria-label=\{closeLabel \?\? defaultI18n\.t\('next\.overlays\.close'\)\}/, 'every other dialog keeps "Close"');
});
