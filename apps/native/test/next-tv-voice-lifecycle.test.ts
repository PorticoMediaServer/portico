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

// N3-4: Apple TV's voice key opens the system keyboard (Siri dictation, iPhone typing) through a hidden field.
for (const retire of [false, true]) test(`Apple TV: the voice key opens the system keyboard and its text fills the query (retire=${retire})`, async () => {
  const abort = new AbortController(), texts: string[] = [], completions: boolean[] = [], calls: string[] = [];
  let api: any, input: any;
  function TextInput(props: any) {input = props; React.useImperativeHandle(props.ref, () => ({focus: () => calls.push('focus'), blur: () => {calls.push('blur'); props.onBlur?.();}, clear: () => calls.push('clear')})); return null;}
  const {useTVVoiceSearch} = await screenModule(new URL('../src-next/screens/settings-live/use-tv-voice-search.ts', import.meta.url), {
    react: {...React, default: React}, 'react-native': {NativeModules: {}, Platform: {OS: 'ios', isTV: true}, TextInput},
  });
  const lifetime = {signal: abort.signal, current: () => !abort.signal.aborted};
  const host = await fabricHost();
  function Voice() {api = useTVVoiceSearch(true, lifetime, (text: string) => texts.push(text), assert.fail); return api.field;}
  const act = async (work: () => void | Promise<void>) => {await work(); await host.settle();};
  try {
    await host.render(React.createElement(Voice));
    assert.ok(api.start && api.begin, 'the voice key is enabled on Apple TV');
    assert.equal(input.focusable, false); assert.equal(input.importantForAccessibility, 'no-hide-descendants');
    await act(() => {assert.equal(api.begin((live: boolean) => completions.push(live)), true);});
    assert.deepEqual(calls, ['clear', 'focus']); assert.equal(api.busy, true);
    assert.equal(api.begin(() => {}), false, 'one keyboard at a time');
    await act(() => {input.onChangeText('  severance ');});
    if (retire) await act(() => {abort.abort();});
    else await act(() => {input.onEndEditing();});
    assert.deepEqual(texts, retire ? [] : ['severance']);
    assert.deepEqual(completions, [!retire]); assert.equal(api.busy, false);
    if (retire) assert.ok(calls.includes('blur'), 'a retired page closes its keyboard');
    await act(() => {input.onBlur?.();});
    assert.deepEqual(completions, [!retire], 'settles once');
  } finally {await host.unmount();}
});
