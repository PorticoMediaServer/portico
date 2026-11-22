import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

test('a block preset\'s conditions offer the fixed preset fields, Mood only when the server offers it', async () => {
  for (const moodAvailable of [false, true]) {
    const host = hooks(), effects: (() => void)[] = [];
    const react = {...host.react, useEffect: (effect: () => void) => {effects.push(effect);}};
    const {useConditionFields} = await screenModule(new URL('../src-next/pages/shared/live/channels/useChannelConditions.ts', import.meta.url), {react,
      '../../../../../../../packages/client-core/src/server-admin/channel-catalogue': {channelFieldLabel: ({id}: {id: string}) => id},
      '../../../../../../../packages/client-core/src/server-admin/owner-library-channels': {readChannelLibrary: assert.fail},
      '../../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../../../screens/settings-live/server/channels/source': {terminal: () => false}});
    const lookup = {api: {}, current: () => true, report: assert.fail, moodAvailable};
    host.render(() => useConditionFields({lookup, libraries: ['lib'], preset: true}));
    effects.splice(0).forEach(effect => effect()); await new Promise(resolve => setTimeout(resolve, 0));
    const view: any = host.render(() => useConditionFields({lookup, libraries: ['lib'], preset: true}));
    assert.deepEqual(view.fields.map((field: any) => field.id), ['genre', 'tag', 'studio', 'network', 'contentRating', 'decade', 'year', 'audioLanguage', 'country', 'collection', ...(moodAvailable ? ['mood'] : [])]);
    assert.equal(view.error, false);
  }
});
