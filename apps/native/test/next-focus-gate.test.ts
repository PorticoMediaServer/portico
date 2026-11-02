import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

async function gate(tv: boolean, host: object | undefined) {
  const destinations: unknown[][] = [];
  const guide = {setDestinations: (value: unknown[]) => destinations.push(value)};
  const layout: (() => void)[] = [];
  const react: any = {createElement: (type: any, props: any, ...children: any[]) => ({type, props: {...props, children}}),
    useCallback: (fn: any) => fn, useRef: () => ({current: guide}), useSyncExternalStore: (_s: any, get: any) => get(), useLayoutEffect: (fn: () => void) => layout.push(fn)};
  react.default = react;
  const {FocusGate} = await screenModule(new URL('../src-next/kit/focus/FocusGate.tsx', import.meta.url), {
    react, 'react-native': {TVFocusGuideView: 'TVFocusGuideView', findNodeHandle: (value: unknown) => value === host ? 42 : null},
    './FocusRegion': {useFocusRegistry: () => ({host: (id: string) => id === 'settings.pages' ? host : undefined, subscribeTarget: () => () => {}, targetSnapshot: () => 1})},
    '../theme': {useTheme: () => ({isTV: tv})}});
  const element = FocusGate({regionId: 'settings.pages', testID: 'gate', style: {position: 'absolute'}});
  for (const effect of layout) effect();
  return {element, destinations};
}

test('W-14: on TV the gate is a focus guide that hands focus to the page column', async () => {
  const {element, destinations} = await gate(true, {tag: 'column'});
  assert.equal(element.type, 'TVFocusGuideView');
  assert.deepEqual(destinations.at(-1), [42], 'Left from any content lands in the column, whose entry is the page you came from');
});

test('a gate whose region is not attached yet hands focus nowhere rather than guessing', async () => {
  const {destinations} = await gate(true, undefined);
  assert.deepEqual(destinations.at(-1), []);
});

test('touch has no D-pad: no gate is drawn', async () => {
  const {element} = await gate(false, {tag: 'column'});
  assert.equal(element, null);
});
