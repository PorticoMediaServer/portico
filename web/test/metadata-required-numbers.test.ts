import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {defaultI18n} from '@i18n';
import {componentModule} from './helpers/component-harness.mjs';
import {createRenderer} from './helpers/interaction-harness.mjs';

// OW-3: an episode's Season number and Episode number could be cleared in the editor; the server refused the whole
// save, and the editor called it an uncertain write. The schema now marks them required, and the field says so first.
const h = createRenderer();
const {fieldError} = await componentModule(new URL('../src/screens/metadata/MetadataEditor.tsx', import.meta.url), {react: h.react, '@i18n': {defaultI18n}});
const spec = (field: string, min: number, max: number, required?: boolean) => ({field, label: field === 'seasonNumber' ? 'Season number' : 'Episode number', group: 'numbers', type: 'integer', min, max, bulk: false, ...(required ? {required} : {})});

test('a required number can\'t be left empty, and says its bounds beside the field', () => {
  assert.equal(fieldError(spec('episodeNumber', 1, 9999, true), ''), defaultI18n.t('web.metadata.numberRange', {label: 'Episode number', min: 1, max: 9999}));
  assert.equal(fieldError(spec('seasonNumber', 0, 99, true), '  '), defaultI18n.t('web.metadata.numberRange', {label: 'Season number', min: 0, max: 99}));
  assert.equal(fieldError(spec('seasonNumber', 0, 99, true), '100'), defaultI18n.t('web.metadata.numberRange', {label: 'Season number', min: 0, max: 99}), 'the server\'s own bound');
  assert.equal(fieldError(spec('seasonNumber', 0, 99, true), '0'), '', 'specials are season zero');
  assert.equal(fieldError(spec('discNumber', 1, 999), ''), '', 'other numbers still clear');
});

test('the editor\'s notices go through the server-error presenter, once (OW-4)', () => {
  const editor = readFileSync(new URL('../src/screens/metadata/MetadataEditor.tsx', import.meta.url), 'utf8');
  assert.doesNotMatch(editor, /HostedError/, 'not the Portico Account presenter');
  assert.match(editor, /if \(!saved && !service\.getSnapshot\(\)\.error\) setError\(/, 'no second copy of the service\'s answer');
  for (const file of ['SongLyricsEditor.tsx', 'NeedsAttention.tsx']) assert.doesNotMatch(readFileSync(new URL('../src/screens/metadata/' + file, import.meta.url), 'utf8'), /HostedError/, file);
});
