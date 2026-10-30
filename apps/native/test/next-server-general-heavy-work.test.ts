import test from 'node:test';
import assert from 'node:assert/strict';
import type {ConsoleClient} from '../../../packages/client-core/src/console';
import {maintenanceForm, type MaintenanceDocument, type MaintenanceSettings} from '../../../packages/client-core/src/server-admin/server-forms';
import {patchHeavyWork, readHeavyWork} from '../src-next/screens/settings-live/server/heavy-work';
import * as heavyWork from '../src-next/screens/settings-live/server/heavy-work';
import {DraftForm} from '../../../packages/client-core/src/server-admin/draft-form';
import {settingsRowIndex} from '../../../packages/client-core/src/presentation/settings-spec';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const initial = (): MaintenanceDocument => ({revision: 7, pausedUntilUnixMillis: 0,
  settings: {windows: [
    {id: 'backup', name: 'Backups', enabled: true, cadence: 'weekly', days: ['sunday'], startMinute: 180, durationMinutes: 60, timezone: 'America/Halifax', tasks: ['backup']},
    {id: 'analysis-window', name: 'Heavy work', enabled: false, cadence: 'custom', days: ['monday'], startMinute: 480, durationMinutes: 120, timezone: 'Europe/London', tasks: ['analysis', 'trickplay']},
  ], retention: {diagnostics: 5}, backupKeepCount: 3, backgroundSpeed: 'gentle'},
  cadences: [], tasks: ['backup', 'analysis', 'trickplay'], days: [], retentionMaxima: {}, storageCategories: []});

test('General heavy-work edits roundtrip through the revisioned maintenance form without changing backups', async () => {
  let document = initial(), reads = 0;
  const writes: {expectedRevision: number; operationId: string; settings: MaintenanceSettings}[] = [];
  const form = maintenanceForm({serverId: 'server', console: {} as ConsoleClient, operationId: () => 'a'.repeat(48),
    api: {request: async <T,>(_path: string, method?: string, body?: unknown) => {
      if (method === 'PUT') {
        const value = body as typeof writes[number];
        assert.equal(value.expectedRevision, document.revision);
        assert.match(value.operationId, /^[a-f0-9]{48}$/);
        writes.push(structuredClone(value));
        document = {...document, revision: document.revision + 1, settings: structuredClone(value.settings)};
        return {protocolVersion: '1', serverId: 'server', result: structuredClone(document)} as T;
      }
      reads++;
      return {protocolVersion: '1', serverId: 'server', result: structuredClone(document)} as T;
    }},
  });
  try {
    await form.load();
    assert.equal(readHeavyWork(form.getSnapshot().draft!, 'general.heavyWork'), 'any');
    form.set(draft => patchHeavyWork(draft, 'general.heavyWork', 'overnight'));
    await form.save();
    assert.equal(reads, 1, 'save consumes the complete canonical write answer without an extra read');
    assert.equal(readHeavyWork(form.getSnapshot().saved!, 'general.heavyWork'), 'overnight');
    assert.deepEqual(document.settings.windows[0], initial().settings.windows[0]);
    assert.deepEqual(document.settings.windows[1], {...initial().settings.windows[1], enabled: true, cadence: 'nightly', days: ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'], startMinute: 60, durationMinutes: 300, timezone: ''});
    form.set(draft => patchHeavyWork(draft, 'general.heavyWork', 'custom'));
    form.set(draft => patchHeavyWork(draft, 'general.heavyStart', '480'));
    form.set(draft => patchHeavyWork(draft, 'general.heavyLength', '2'));
    const days = readHeavyWork(form.getSnapshot().draft!, 'general.heavyDays') as Record<string, string>;
    form.set(draft => patchHeavyWork(draft, 'general.heavyDays', {...days, sunday: 'skips'}));
    await form.save();
    assert.equal(writes[1].expectedRevision, 8);
    assert.equal(readHeavyWork(form.getSnapshot().saved!, 'general.heavyWork'), 'custom');
    assert.equal(document.settings.windows[1].startMinute, 480);
    assert.equal(document.settings.windows[1].durationMinutes, 120);
    assert.ok(!document.settings.windows[1].days.includes('sunday'));
    const custom = document.settings.windows[1];
    form.set(draft => patchHeavyWork(draft, 'general.heavyWork', 'any'));
    await form.save();
    assert.deepEqual(document.settings.windows[1], {...custom, enabled: false});
    assert.deepEqual(document.settings.windows[0], initial().settings.windows[0]);
    assert.equal(document.settings.backgroundSpeed, 'gentle');
    assert.equal(document.settings.backupKeepCount, 3);
  } finally {form.dispose();}
});

test('Custom preserves actual heavy window fields and absent producers cannot create or edit a backup window', () => {
  const draft = initial().settings;
  const custom = patchHeavyWork(draft, 'general.heavyWork', 'custom');
  assert.deepEqual(custom.windows[1], {...draft.windows[1], enabled: true});
  const missing = {...draft, windows: [draft.windows[0]]};
  assert.equal(readHeavyWork(missing, 'general.heavyWork'), undefined);
  assert.throws(() => patchHeavyWork(missing, 'general.heavyWork', 'overnight'), /Load maintenance settings/);
  assert.deepEqual(missing.windows[0], draft.windows[0]);
});

test('Custom opens actual overnight fields for editing and Discard restores the stored mode', async () => {
  const saved = patchHeavyWork(initial().settings, 'general.heavyWork', 'overnight');
  const form = new DraftForm<MaintenanceSettings, MaintenanceDocument>({load: async () => ({value: saved}), save: async () => {}});
  await form.load();
  const host = hooks();
  const module = await screenModule(new URL('../src-next/screens/settings-live/server/HeavyWorkControls.tsx', import.meta.url), {
    react: host.react, 'react-native': {View: 'View', Text: 'Text'},
    '../../../../../../packages/client-core/src/presentation/settings-spec': {settingsRowIndex},
    '../../../kit/controls/Button': {Button: 'Button'}, '../../../kit/controls/Select': {Select: 'Select'},
    '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/theme': {useTheme: () => ({colors: {text: 'white'}})},
    './heavy-work': heavyWork,
  });
  const nodes = (node: any): any[] => Array.isArray(node) ? node.flatMap(nodes) : node?.props ? [node, ...nodes(node.props.children)] : [];
  const render = () => host.render(() => module.HeavyWorkControls({form, state: form.getSnapshot(), t: (key: string) => key}));
  const control = (id: string) => nodes(render()).find(node => node.props.testID === id);
  try {
    control('general.heavyWork').props.onValueChange('custom');
    assert.equal(control('general.heavyWork').props.value, 'custom');
    assert.equal(control('general.heavyStart').props.disabled, false);
    assert.equal(control('general.heavyStart').props.value, '60');
    form.discard();
    assert.equal(control('general.heavyWork').props.value, 'overnight');
    assert.equal(control('general.heavyStart').props.disabled, true);
  } finally {form.dispose();}
});
