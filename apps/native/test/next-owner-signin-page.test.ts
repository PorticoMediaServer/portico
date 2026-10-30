import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children)] : [];
const words = (value: any): string => Array.isArray(value) ? value.map(words).join('') : typeof value === 'string' ? value : value?.props ? words(value.props.children) : '';
async function fixture(authorized = true, isTV = false) {
  const host = hooks();
  const useCallback = (fn: (...args: any[]) => any, deps: readonly unknown[]) => host.react.useMemo(() => fn, deps);
  Object.assign(host.react, {useCallback}); Object.assign(host.react.default, {useCallback});
  const effects: (() => void)[] = [], cleanups = new Set<() => void>();
  const effect = (fn: () => (() => void) | undefined, deps: unknown[]) => {const slot = host.react.useRef(undefined); if (!slot.current || deps.some((value, index) => value !== slot.current.deps[index])) {const previous = slot.current?.cleanup; const next: any = {deps}; slot.current = next; effects.push(() => {previous?.(); const cleanup = fn(); next.cleanup = cleanup; if (cleanup) cleanups.add(cleanup);});}};
  Object.assign(host.react, {useEffect: effect}); Object.assign(host.react.default, {useEffect: effect});
  let alive = true, reads = 0, disposed = 0, resolve: (value: unknown) => void = () => {};
  const result = new Promise(value => {resolve = value;});
  const visits: string[] = [], pages: string[] = [], options: any[] = [];
  const api = {baseUrl: 'http://selected-server.invalid:8096'};
  const context = {api, serverName: 'Selected server', account: {account: {role: 'owner', primaryProfileId: 'main'}, capabilities: {manageSignIn: authorized}}};
  const scope = {api, lifetime: {current: () => alive}, authority: {serverId: 'server', profileId: 'main'}};
  const {NativeServerSignIn} = await screenModule(new URL('../src-next/screens/settings-live/server/SignInPage.tsx', import.meta.url), {
    react: host.react, 'react-native-qrcode-svg': {default: 'QRCode'},
    'react-native': {Text: 'Text', View: 'View', ScrollView: 'ScrollView', Linking: {openURL: async (url: string) => {visits.push(url);}}},
    '../../../../../../packages/client-core/src/account-move': {ReadOnlySignInClient: class {constructor(transport: unknown, input: unknown) {assert.equal(transport, api); options.push(input);} read() {reads++; return result;} dispose() {disposed++;}}},
    '../../../../../../packages/client-core/src/sign-in': {serverSignInSettingsURL: (origin: string) => {assert.equal(origin, api.baseUrl); return new URL('/settings?scope=server&section=signin', origin).href;}},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'}, '../../../kit/shell/ServerIdentity': {ServerHeader: 'ServerHeader'},
    './page-layout': {settingsPageInset: () => ({}), settingsPageHeading: () => ({})}, '../../../kit/shell/visual': {shellType: () => ({})}, '../../../kit/states': {StateView: 'StateView'}, '../../../kit/theme': {useTheme: () => ({colors: {}, isTV})},
    '../use-settings-request-scope': {useSettingsRequestScope: () => scope},
  });
  const props = {ports: {context}, runtime: {text: {t: (id: string) => id}, error: () => ({title: 'failure'})}, onOpenPage: (id: string) => pages.push(id)};
  const render = () => {let tree = host.render(() => NativeServerSignIn(props)); if (effects.length) {effects.splice(0).forEach(run => run()); tree = host.render(() => NativeServerSignIn(props));} return tree;};
  return {render, visits, pages, options, reads: () => reads, disposed: () => disposed, end: () => {alive = false;}, unmount: () => {cleanups.forEach(fn => fn());},
    reply: async (move = {stage: 'complete', to: 'local', waiting: 0, people: 4, profiles: 5}) => {resolve({signIn: 'local', owner: {username: 'Owner'}, move}); await Promise.resolve(); await Promise.resolve();}};
}

test('Native Sign-in reads server facts and opens only the selected server web settings on an explicit action', async () => {
  const f = await fixture();
  try {
    f.render(); assert.equal(f.reads(), 1); assert.deepEqual(f.visits, []);
    assert.equal(f.options[0].serverId, 'server'); assert.equal(f.options[0].isCurrent(), true);
    await f.reply(); const tree = f.render();
    assert.ok(words(tree).includes('accounts.settings.moveComplete')); assert.ok(!words(tree).includes('Moving to local sign-in'));
    const open = nodes(tree).find(node => node.props.testID === 'server-signin-open-web')!;
    assert.ok(open); open.props.onPress();
    assert.deepEqual(f.visits, ['http://selected-server.invalid:8096/settings?scope=server&section=signin']);
    const people = nodes(tree).find(node => node.type === 'SettingsRow' && node.props.label === 'settings.server.people')!;
    people.props.onPress(); assert.deepEqual(f.pages, ['people']);
    f.end(); open.props.onPress(); people.props.onPress(); assert.equal(f.visits.length, 1); assert.equal(f.pages.length, 1);
  } finally {f.unmount();}
  assert.equal(f.disposed(), 1);
});

test('Sign-in refuses missing authority and discards a reply after the original scope retires', async () => {
  const denied = await fixture(false); denied.render(); assert.equal(denied.reads(), 0); assert.deepEqual(denied.visits, []); denied.unmount();
  const stale = await fixture();
  try {stale.render(); stale.end(); await stale.reply(); assert.ok(!nodes(stale.render()).some(node => node.props.testID === 'server-signin-open-web')); assert.deepEqual(stale.visits, []);}
  finally {stale.unmount();}
});

test('Moving progress uses real counts and TV hands off to the selected server URL', async () => {
  const f = await fixture(true, true);
  try {
    f.render(); await f.reply({stage: 'moving_to_local', to: 'local', waiting: 2, people: 4, profiles: 5});
    const tree = f.render(), progress = nodes(tree).find(node => node.props.testID === 'server-signin-progress');
    assert.deepEqual(progress.props.accessibilityValue, {min: 0, max: 4, now: 2});
    assert.ok(words(tree).includes('accounts.settings.codesOnWeb'));
    assert.equal(nodes(tree).find(node => node.type === 'QRCode').props.value, 'http://selected-server.invalid:8096/settings?scope=server&section=signin');
    assert.deepEqual(f.visits, []);
  } finally {f.unmount();}
});
