import test from 'node:test';
import assert from 'node:assert/strict';
import {settingsSpec, settingsRowIndex, settingsDesignRevision} from '../../packages/client-core/src/presentation/settings-spec.ts';
import {settingsSpecMessages} from '../../packages/i18n/src/catalog/en-US/next/settings-spec.ts';

test('the approved structure preserves all sections and the exact General-first server order', () => {
  assert.equal(settingsDesignRevision, '812dacacc70e296ac870ec36d1a37a485eaeb8ed');
  assert.equal(settingsSpec.length, 25);
  assert.deepEqual(settingsSpec.filter(section => section.scope === 'server').map(section => section.id), ['general','dashboard','history','libraries','remote-sources','metadata','segments','live','people','signin','remote','integrations','streaming','storage','troubleshooting']);
  assert.ok(settingsRowIndex.size > 400);
  assert.equal(settingsRowIndex.get('people.accessTokens')?.panelId, 'person');
  assert.equal(settingsRowIndex.get('libraries.folderPath')?.tabId, 'folders');
  assert.equal(settingsRowIndex.get('streaming.uploadSpeed')?.row.control.default, null);
});
test('drawn sample rows, secrets, facts and successful action results cannot ship through the spec', () => {
  const text = JSON.stringify(settingsSpec);
  assert.equal(/"(?:start|result|values|tags|designLinks)"\s*:|\.html/.test(text), false);
  assert.doesNotMatch(JSON.stringify(settingsSpecMessages), /Portico demo|demo@example|sam_k|nana_r|Frees 9\.6/);
  for (const {row} of settingsRowIndex.values()) {
    const c = row.control;
    if (c.type === 'table') assert.equal('rows' in c, false);
    if (c.type === 'list' && !c.reorder) assert.equal('items' in c, false);
    if (c.type === 'readonly') assert.equal('value' in c, false);
    if (c.type === 'status') assert.equal('text' in c, false);
    if (c.type === 'text' && c.secret) assert.equal(c.default, '');
  }
});
test('all preset references and dependent rows resolve through one canonical index', () => {
  for (const {row} of settingsRowIndex.values()) {
    if (row.enableWhen) assert.ok(settingsRowIndex.has(row.enableWhen.id), row.id);
    const presets = row.control.options?.filter(option => option.sets) ?? [];
    for (const option of presets) for (const key of Object.keys(option.sets!)) assert.ok(settingsRowIndex.has(key), `${row.id} -> ${key}`);
    if (presets.length) assert.equal(row.control.options?.filter(option => option.value === 'custom' && !option.sets).length, 1, row.id);
  }
});
