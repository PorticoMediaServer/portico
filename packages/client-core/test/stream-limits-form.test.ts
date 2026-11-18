import test from 'node:test';
import assert from 'node:assert/strict';
import {createServerForm} from '../src/server-admin/server-forms.ts';
import {applySettingsBindingPatch, readSettingsBinding, settingsBinding, settingsBindingPatch} from '../src/presentation/settings-bindings.ts';

// People › Advanced › Streams at once for each person (definition 07 rule 36), on the rebuilt server's stream limits.
test('streams at once for each person reads and saves the stream limits, naming only perPerson', async () => {
  const sent: unknown[] = [];
  let limits: Record<string, unknown> = {total: 4};
  const api = {request: async <T>(path: string, method?: string, body?: Record<string, unknown>) => {
    sent.push(body === undefined ? [method, path] : [method, path, body]);
    if (method === 'PATCH') limits = {...limits, ...body};
    return limits as T;
  }};
  const form = createServerForm('streamLimits', {api, console: {} as never, serverId: 'server', operationId: () => 'unused'});
  const binding = settingsBinding('people.maxStreamsEach')!;
  try {
    await form.load();
    assert.equal(readSettingsBinding(binding, {forms: {streamLimits: form.getSnapshot().draft}})?.current, 'unlimited', 'no default set is unlimited');
    form.set((draft: Record<string, unknown>) => applySettingsBindingPatch(draft, settingsBindingPatch(binding, '3')));
    await form.save();
    assert.equal(readSettingsBinding(binding, {forms: {streamLimits: form.getSnapshot().saved}})?.current, '3');
    form.set((draft: Record<string, unknown>) => applySettingsBindingPatch(draft, settingsBindingPatch(binding, 'unlimited')));
    await form.save();
    assert.deepEqual(sent, [['GET', '/v1/admin/playback/stream-limits'], ['PATCH', '/v1/admin/playback/stream-limits', {perPerson: 3}],
      ['PATCH', '/v1/admin/playback/stream-limits', {perPerson: 0}]]);
    assert.deepEqual(limits, {total: 4, perPerson: 0}, 'the server-wide limit is left alone');
  } finally {form.dispose();}
});
