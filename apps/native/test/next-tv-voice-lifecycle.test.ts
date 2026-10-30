import test from 'node:test';
import assert from 'node:assert/strict';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

// Real Fabric lifecycle; the recognizer bridge is controlled, not device qualification.
for (const retire of [false, true]) test(`voice result and original return belong to the opening (retire=${retire})`, async () => {
  const abort = new AbortController(), texts: string[] = [], errors: unknown[] = [], completions: boolean[] = [], canceled: string[] = [];
  let resolve: (value: string | null) => void = () => {}, api: any;
  const bridge = {playbackRequestId: () => 'actual-opening', canTVVoiceSearch: async () => true,
    startTVVoiceSearch: () => new Promise<string | null>(done => {resolve = done;}), cancelTVVoiceSearch: (id: string) => canceled.push(id)};
  const {useTVVoiceSearch} = await screenModule(new URL('../src-next/screens/settings-live/use-tv-voice-search.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {NativeModules: {PorticoRuntime: bridge}, Platform: {OS: 'android', isTV: true}},
  });
  const lifetime = {signal: abort.signal, current: () => !abort.signal.aborted};
  const host = await fabricHost();
  function Voice() {api = useTVVoiceSearch(true, lifetime, (text: string) => texts.push(text), (error: unknown) => errors.push(error)); return null;}
  const act = async (work: () => void | Promise<void>) => {await work(); await host.settle();};
  try {
    await host.render(React.createElement(Voice));
    await act(() => {assert.equal(api.begin((live: boolean) => completions.push(live)), true);});
    assert.equal(api.busy, true);
    assert.equal(api.begin(() => {throw new Error('second concurrent opening');}), false);
    if (retire) await act(() => {abort.abort();});
    await act(async () => {resolve('the actual query'); await Promise.resolve();});
    assert.deepEqual(texts, retire ? [] : ['the actual query']);
    assert.deepEqual(completions, [!retire]); assert.deepEqual(errors, []);
    if (retire) assert.ok(canceled.includes('actual-opening'));
  } finally {await host.unmount();}
});
