import assert from 'node:assert/strict';
import test from 'node:test';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {sameFence} from '../../../packages/player-core/src/identity.ts';
import {createTVKeyAdapter} from '../src-next/platform/tv-key-adapter.ts';
import {createTVSeekRepeat} from '../src-next/platform/tv-seek-repeat.ts';

async function fixture() {
  const host = hooks(), pending: (() => void)[] = [], cleanups = new Set<() => void>();
  const effect = (callback: () => void | (() => void), dependencies: readonly unknown[]) => {
    const slot = host.react.useRef(undefined);
    if (!slot.current || dependencies.some((value, index) => value !== slot.current.dependencies[index])) {
      pending.push(() => {slot.current?.cleanup?.(); const cleanup = callback(); slot.current = {dependencies, cleanup}; if (cleanup) cleanups.add(cleanup);});
    }
  };
  const react = {...host.react, useEffect: effect, useLayoutEffect: effect};
  let now = 0, serial = 0, listener: (event: any) => void = () => {}, back: () => boolean = () => false;
  const timers = new Map<number, {at: number; callback: () => void}>(), commands: any[] = [];
  const clock = {now: () => now, schedule(callback: () => void, milliseconds: number) {const id = ++serial; timers.set(id, {at: now + milliseconds, callback}); return id;}, cancel(id: unknown) {timers.delete(id as number);}};
  const keys = createTVKeyAdapter();
  const model = await screenModule(new URL('../src-next/kit/dock/model.ts', import.meta.url), {
    react, '../../../../../packages/client-core/src/cast': {}, '../../../../../packages/player-core/src/index': {sameFence},
    '../../../../../packages/client-core/src/presentation/kit-model/text': {}, '../../../../../packages/i18n/src/index': {},
  });
  const {TVMusicSeek} = await screenModule(new URL('../src-next/screens/destinations/music/TVMusicFocus.tsx', import.meta.url), {
    react, 'react-native': {View: 'View', findNodeHandle: () => 7, BackHandler: {addEventListener(_name: string, callback: () => boolean) {back = callback; return {remove() {back = () => false;}};}}},
    '../../../platform/tv-keys': {useTVKeyEvent(callback: (event: any) => void) {listener = raw => {const event = keys.read(raw); if (event) callback(event);};}},
    '../../../platform/tv-seek-repeat': {createTVSeekRepeat: (step: Parameters<typeof createTVSeekRepeat>[0]) => createTVSeekRepeat(step, clock)},
    '../../../../../../packages/player-core/src/identity': {sameFence}, '../../../kit/dock/model': model,
    '../../../kit/controls': {Focusable: 'Focusable'}, '../../../kit/focus': {FocusRegion: 'FocusRegion'}, '../../../kit/theme': {useTheme: () => ({colors: {text: '#fff'}, isTV: true})},
    '../../../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => key}}, '../../destinations/useEntryFocus': {},
  });
  const fence = {kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile'}, run: 1}, sessionId: 'session', itemId: 'song', sourceId: 'queue', entryId: 'entry', generation: 1};
  let occupant = {mode: 'music', playback: {fence, positionSeconds: 20}, presentation: {fence, capabilities: {seek: true}, durationSeconds: 100, skipBackSeconds: 10}};
  const render = (enabled = true) => {
    const tree = host.render(() => TVMusicSeek({occupant, enabled, testID: 'seek', onAction: (command: unknown) => commands.push(command)}));
    pending.splice(0).forEach(run => run()); return tree;
  };
  return {render, commands, timers, key: (event: unknown) => listener(event), back: () => back(),
    advance(positionSeconds: number) {occupant = {...occupant, playback: {...occupant.playback, positionSeconds}};},
    replace() {const nextFence = {...fence, generation: 2}; occupant = {...occupant, playback: {...occupant.playback, fence: nextFence}, presentation: {...occupant.presentation, fence: nextFence}};},
    tick() {const first = [...timers.entries()].sort((a, b) => a[1].at - b[1].at)[0]; if (first) {timers.delete(first[0]); now = first[1].at; first[1].callback();}},
    unmount() {cleanups.forEach(run => run());},
  };
}

test('music holds progress within the seek window and stop on release, blur, Back and coverage', async () => {
  const f = await fixture(), tree = f.render(); tree.props.onFocus();
  f.key({eventType: 'longRight', eventKeyAction: 0}); f.tick();
  assert.deepEqual(f.commands.map(command => command.positionSeconds), [30, 40]);
  f.key({eventType: 'longRight', eventKeyAction: 1}); assert.equal(f.timers.size, 0);
  f.key({eventType: 'longLeft', eventKeyAction: 0}); tree.props.onBlur(); assert.equal(f.timers.size, 0);
  f.key({eventType: 'longLeft', eventKeyAction: 1}); tree.props.onFocus();
  f.key({eventType: 'longRight', eventKeyAction: 0}); assert.equal(f.back(), false, 'seek cancellation preserves the parent Back handler'); assert.equal(f.timers.size, 0);
  f.key({eventType: 'longRight', eventKeyAction: 1}); f.key({eventType: 'longRight', eventKeyAction: 0});
  f.render(false); assert.equal(f.timers.size, 0); const count = f.commands.length; f.tick(); assert.equal(f.commands.length, count);
  f.unmount();
});

test('music holds retire with the captured fence and cannot repeat past the duration', async () => {
  const f = await fixture(), tree = f.render(); tree.props.onFocus();
  f.key({eventType: 'longRight', eventKeyAction: 0}); f.replace(); f.render(); assert.equal(f.timers.size, 0);
  f.key({eventType: 'longRight', eventKeyAction: 1}); f.key({eventType: 'longRight', eventKeyAction: 0});
  for (let index = 0; index < 20; index++) f.tick();
  assert.equal(f.commands.at(-1).positionSeconds, 100); assert.equal(f.timers.size, 0);
  assert.ok(f.commands.every(command => command.positionSeconds >= 0 && command.positionSeconds <= 100));
  assert.ok(f.commands.slice(1).every(command => command.fence.generation === 2));
  f.unmount();
});

test('accessibility seek uses fresh playback progress in the same fence and cancels a remote hold', async () => {
  const f = await fixture();
  f.render().props.onAccessibilityAction({nativeEvent: {actionName: 'increment'}});
  assert.equal(f.commands.at(-1).positionSeconds, 30);
  f.advance(50);
  const tree = f.render();
  tree.props.onAccessibilityAction({nativeEvent: {actionName: 'increment'}});
  assert.equal(f.commands.at(-1).positionSeconds, 60);
  tree.props.onFocus(); f.key({eventType: 'longRight', eventKeyAction: 0});
  assert.equal(f.timers.size, 1);
  tree.props.onAccessibilityAction({nativeEvent: {actionName: 'decrement'}});
  assert.equal(f.commands.at(-1).positionSeconds, 40); assert.equal(f.timers.size, 0);
  f.unmount();
});
