import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

// Walk W-2 (2 Nov): a title that couldn't load drew an inline state at the very top: its heading under the status
// bar, the Back button over its text, no backdrop. The whole-page failure is the standard full-page state
// (states.html 13): backdrop, drawn below Back and the status bar; TV keeps its side backdrop, centred.
async function page(isTV: boolean) {
  const react = {createElement: (type: any, props: any, ...children: any[]) => ({type, props: {...props, ...(children.length ? {children} : {})}})};
  const mod = await screenModule(new URL('../src-next/screens/native-screen-services.tsx', import.meta.url), {
    react: {...react, default: react, useSyncExternalStore: () => undefined}, 'react-native': {NativeModules: {}},
    '../kit/dock/DockClearance': {useDockClearance: () => 0}, '../kit/overlays': {ToastHost: 'ToastHost'},
    '../kit/states': {StateLayout: 'StateLayout', StateView: 'StateView'}, '../kit/theme': {useTheme: () => ({isTV})},
    './screen-services': {NativeScreenServices: class {}},
  });
  const service = {error: (_error: unknown, retry: () => void, context: string) => ({title: `${context} failed`, body: 'Check the server.', icon: 'i-warn', primaryAction: {id: 'retry', label: 'Try again', onPress: retry}})};
  const element = mod.nativeScreenErrorPage(service, 'detail')(new Error('offline'), () => {}) as {type: any; props: any};
  return element.type(element.props) as {type: string; props: any};
}

test('W-2: a title that couldn\'t load is the full-page state, below Back and the status bar, on a phone or tablet', async () => {
  const drawn = await page(false);
  assert.equal(drawn.type, 'StateLayout');
  assert.equal(drawn.props.contentTop, 250);
  assert.equal(drawn.props.backdropHeight, 420);
  assert.equal(drawn.props.title, 'detail failed');
  assert.equal(drawn.props.primaryAction.label, 'Try again');
});

test('W-2: on TV the same state keeps its side backdrop and sits centred', async () => {
  const drawn = await page(true);
  assert.equal(drawn.type, 'StateLayout');
  assert.equal(drawn.props.contentTop, undefined);
  assert.equal(drawn.props.backdropHeight, undefined);
});
