import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';
import {FreshnessHub} from '../../../packages/client-core/src/freshness-hub.ts';
import {phoneHeaderGeometry, shellLayout} from '../src-next/shell/model.ts';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const text = (value: any): string => typeof value === 'string' ? value : Array.isArray(value) ? value.map(text).join('') : value?.props ? text(value.props.children) : '';

async function line(top = 47) {
  const host = hooks();
  const {ShellUnreachableLine} = await screenModule(new URL('../src-next/shell/UnreachableLine.tsx', import.meta.url), {
    react: host.react, 'react-native': {Text: 'Text', View: 'View'},
    'react-native-safe-area-context': {useSafeAreaInsets: () => ({top, bottom: 0})},
    '../../../../packages/i18n/src': {defaultI18n: {t: (key: string) => ({'next.overlays.unreachableStart': "Can't reach ", 'next.overlays.unreachableEnd': '. Trying again.', 'next.states.cantReachServer': "Can't reach your server"} as Record<string, string>)[key] ?? key}},
    '../kit/overlays': {Banner: 'Banner'}, '../kit/theme': {fontFamilies: {700: 'Bold'}, useTheme: () => ({colors: {text: '#fff'}, layers: {notice: 9}})},
    './model': {phoneHeaderGeometry},
  });
  return (props: Record<string, unknown>) => {
    const tree = host.render(() => ShellUnreachableLine(props));
    return {tree, banner: nodes(tree).find(node => node.type === 'Banner')};
  };
}

test('N2-5: the shell says it can\'t reach the server only after its reads have failed for a while, and stops on the next answer', async () => {
  let now = 0;
  const timers: {at: number; fn: () => void}[] = [];
  const hub = new FreshnessHub({now: () => now, random: () => 0, setTimer: (fn, ms) => {const timer = {at: now + ms, fn}; timers.push(timer); return timer;},
    clearTimer: timer => {const index = timers.indexOf(timer as never); if (index >= 0) timers.splice(index, 1);}});
  const advance = (ms: number) => {now += ms; for (const timer of timers.filter(value => value.at <= now)) {timers.splice(timers.indexOf(timer), 1); timer.fn();}};
  const reachability = {getSnapshot: hub.getReachability, subscribe: hub.subscribeReachability};
  const draw = await line();
  const phone = shellLayout(390, 844, false);
  const props = {reachability, serverName: 'Portico demo', layout: phone, hidden: false};
  assert.equal(draw(props).banner.props.visible, false, 'connected: nothing');
  const source = {};
  hub.report(source, false); advance(5_000);
  assert.equal(draw(props).banner.props.visible, false, 'one short failure says nothing');
  advance(15_000);
  const shown = draw(props).banner;
  assert.equal(shown.props.visible, true, 'failing for longer than the limit: the line');
  assert.equal(shown.props.variant, 'quiet');
  assert.equal(text(shown.props.message), "Can't reach Portico demo. Trying again.");
  assert.equal(shown.props.accessibilityLabel, "Can't reach Portico demo. Trying again.");
  assert.equal(shown.props.topInset, phoneHeaderGeometry(47, false).height + 2, 'phone: under the top bar');
  assert.equal(draw({...props, hidden: true}).banner.props.visible, false, 'the full-screen player or reader hides it');
  hub.report(source, true);
  assert.equal(draw(props).banner.props.visible, false, 'the first answer takes it down');
  hub.dispose();
});

test('N2-5: on TV the line sits top centre in the 1920 canvas and takes no focus or touch; with no name it says "your server"', async () => {
  const draw = await line(0);
  const tv = shellLayout(1920, 1080, true);
  const {tree, banner} = draw({reachability: {getSnapshot: () => 'unreachable', subscribe: () => () => {}}, layout: tv, hidden: false});
  assert.equal(tree.props.pointerEvents, 'none');
  assert.deepEqual(tree.props.style.transform, [{scale: tv.scale}]);
  assert.equal(banner.props.topInset, 44, 'states.html 4b, TV: top centre');
  assert.equal(text(banner.props.message), "Can't reach your server. Trying again.");
  const touch = draw({reachability: {getSnapshot: () => 'unreachable', subscribe: () => () => {}}, serverName: 'Den', layout: shellLayout(390, 844, false), hidden: false});
  assert.equal(touch.tree.props.pointerEvents, 'none', 'the page under it stays usable');
});
