import {webhookEventGroups} from '../../../packages/client-core/src/server-admin/api-words.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import {selectWebhookEventGroup, webhookSecretChange} from '../src-next/screens/settings-live/server/integrations/webhook-form.ts';

test('webhook event groups preserve the server order and include unfamiliar event families', () => {
  assert.deepEqual(webhookEventGroups(['title.added', 'scan.started', 'title.updated', 'future.event', 'scan.finished', 'title.added']), [
    {id: 'titles', label: 'api.webhooks.group.titles', events: ['title.added', 'title.updated']},
    {id: 'scans', label: 'api.webhooks.group.scans', events: ['scan.started', 'scan.finished']},
    {id: 'other', label: 'api.webhooks.group.other', events: ['future.event']},
  ]);
});

test('All and None affect only their webhook event family and never duplicate subscriptions', () => {
  const original = Object.freeze(['playback.started', 'title.added']);
  const all = selectWebhookEventGroup(original, ['title.added', 'title.updated'], true);
  assert.deepEqual(all, ['playback.started', 'title.added', 'title.updated']);
  assert.deepEqual(selectWebhookEventGroup(all, ['title.added', 'title.updated'], false), ['playback.started']);
  assert.deepEqual(original, ['playback.started', 'title.added']);
});

test('an empty replacement asks for rotation; an explicit secret is preserved byte for byte', () => {
  assert.deepEqual(webhookSecretChange(''), {rotateSecret: true});
  assert.deepEqual(webhookSecretChange(' test-only-secret '), {secret: ' test-only-secret '});
});
