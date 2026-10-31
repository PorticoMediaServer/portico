import assert from 'node:assert/strict';
import test from 'node:test';
import {screenModule} from './helpers/screen-harness.mjs';
import {createInitialRegionEntry} from '../src-next/kit/focus/initial-entry.ts';
import {defaultI18n} from '../../../packages/i18n/src';
import {createTheme, nativeType} from '../src-next/kit/theme';

// Android TV walk, 31 Oct: the re-entry PIN at launch opened with nothing focused. The pad mounts
// under the boot screen's fade, where it is painted but inert, and spent its one request for first
// focus there. It now asks once the pad is live.

const theme = createTheme({isTV: true, width: 1920});

/** Just enough React to run the pad's effects when their dependencies change. */
function host() {
  const slots: any[] = []; let cursor = 0;
  const pending: Array<() => void> = [];
  const slot = (initial: () => unknown) => {const n = cursor++; if (!(n in slots)) slots[n] = {value: initial()}; return slots[n];};
  const same = (a?: readonly unknown[], b?: readonly unknown[]) => !!a && !!b && a.length === b.length && a.every((value, i) => Object.is(value, b[i]));
  const react = {
    createElement: (type: unknown, props: any, ...children: unknown[]) => ({type, props: {...props, children}}),
    useRef: (value: unknown) => slot(() => ({current: value})).value,
    useMemo: (fn: () => unknown, deps: readonly unknown[]) => {const s = slot(() => undefined); if (!s.value || !same(s.value.deps, deps)) s.value = {deps, value: fn()}; return s.value.value;},
    useEffect: (fn: () => void, deps: readonly unknown[]) => {const s = slot(() => undefined); if (!same(s.value, deps)) {s.value = deps; pending.push(fn);}},
    useSyncExternalStore: (_subscribe: unknown, get: () => unknown) => get(),
    useContext: (context: {value: unknown}) => context.value,
  };
  return {react: {...react, default: react}, render(fn: () => unknown) {cursor = 0; const out = fn(); for (const effect of pending.splice(0)) effect(); return out;}};
}

test('the pad asks for first focus only once it is live, and then only once', async () => {
  const h = host(), focused: string[] = [];
  let paint = {visible: true, interactive: false, root: true}, revision = 0;
  const registry = {subscribe: () => () => {}, getSnapshot: () => revision, attachedFocus: () => undefined, hasRetainedFocus: () => false,
    entryAddress: (id: string) => `address:${id}`, focus: (address: string) => {focused.push(address); return true;}};
  const pad = await screenModule(new URL('../src-next/tv/auth/TVNumericPad.tsx', import.meta.url), {
    react: h.react, 'react-native': {Text: 'Text', View: 'View', AccessibilityInfo: {announceForAccessibility() {}}},
    '../../../../../packages/i18n/src': {defaultI18n}, '../../kit/controls': {Button: 'Button', Focusable: 'Focusable'},
    '../../kit/focus': {FocusRegion: 'Region', FocusItem: 'Item', useFocusRegistry: () => registry},
    '../../kit/focus/initial-entry': {createInitialRegionEntry}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/overlays/OverlayPaintContext': {useOverlayPaintMode: () => paint},
    '../../kit/theme': {nativeType, useTheme: () => theme},
  });
  const props = {id: 'tv.auth.profile.pin', busy: false, onDigit() {}, onDelete() {}, help: 'Forgot PIN?', onHelp() {}};
  h.render(() => pad.TVNumericPad(props));
  revision++; h.render(() => pad.TVNumericPad(props));
  assert.deepEqual(focused, [], 'not while the boot screen still covers it');
  paint = {visible: true, interactive: true, root: true};
  h.render(() => pad.TVNumericPad(props));
  assert.deepEqual(focused, ['address:tv.auth.profile.pin.pad'], 'the pad\'s first key, once it is live');
  revision++; h.render(() => pad.TVNumericPad(props));
  assert.equal(focused.length, 1, 'and never again');
});
