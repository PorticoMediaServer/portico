import test from 'node:test';
import assert from 'node:assert/strict';
import {parseSettingsConfiguration} from '../src/settings-configuration.ts';

// The configuration route answers {scope, data}. The settings pages read the answer itself,
// and TV Server settings › General showed "This couldn't load" (walk, 1 Nov).
const document = {revision: 7, registryRevision: 'settings-r40.1', path: '/srv/portico.toml', fields: [
  {id: 'storage.missingRule', key: 'storage.remove_missing_titles', current: 'after_days', effective: 'after_days', default: 'after_days', source: 'default', configOnly: false, applicationMode: 'hot'},
]};

test('the configuration reads from the scoped answer and from its document alike', () => {
  const scoped = parseSettingsConfiguration({scope: {serverId: 'srv_x', viewerFence: 'a'.repeat(64)}, data: document});
  assert.equal(scoped.revision, 7);
  assert.equal(scoped.fields[0]!.id, 'storage.missingRule');
  assert.deepEqual(parseSettingsConfiguration(document), scoped);
  assert.throws(() => parseSettingsConfiguration({scope: {}, data: {revision: 0}}));
});
