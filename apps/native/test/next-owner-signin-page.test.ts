import test from 'node:test';
import assert from 'node:assert/strict';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

const nodes = (value: any): any[] => Array.isArray(value) ? value.flatMap(nodes) : value?.props ? [value, ...nodes(value.props.children), ...nodes(value.props.footer)] : [];
const words = (value: any): string => Array.isArray(value) ? value.map(words).join('') : typeof value === 'string' ? value : value?.props ? words([value.props.label, value.props.description, value.props.children]) : '';
async function fixture(authorized = true, isTV = false) {
  const host = hooks();
  const useCallback = (fn: (...args: any[]) => any, deps: readonly unknown[]) => host.react.useMemo(() => fn, deps);
  Object.assign(host.react, {useCallback}); Object.assign(host.react.default, {useCallback});
  const effects: (() => void)[] = [], cleanups = new Set<() => void>();
  const effect = (fn: () => (() => void) | undefined, deps: unknown[]) => {const slot = host.react.useRef(undefined); if (!slot.current || deps.some((value, index) => value !== slot.current.deps[index])) {const previous = slot.current?.cleanup; const next: any = {deps}; slot.current = next; effects.push(() => {previous?.(); const cleanup = fn(); next.cleanup = cleanup; if (cleanup) cleanups.add(cleanup);});}};
  Object.assign(host.react, {useEffect: effect}); Object.assign(host.react.default, {useEffect: effect});
  let alive = true, reads = 0, disposed = 0, finishes = 0, resolve: (value: unknown) => void = () => {};
  const result = new Promise(value => {resolve = value;});
  const visits: string[] = [], pages: string[] = [], options: any[] = [];
  const api = {baseUrl: 'http://selected-server.invalid:8096'};
  const context = {api, serverName: 'Selected server', account: {account: {role: 'owner', primaryProfileId: 'main'}, capabilities: {manageSignIn: authorized}}};
  const scope = {api, lifetime: {current: () => alive}, authority: {serverId: 'server', profileId: 'main'}};
  const theme = () => ({colors: {raised: '#09131b', white: '#fff'}, space: {}, type: {}, baseRadii: {}, isTV});
  const frame = {useFrameMetrics: () => ({})}, react = host.react;
  const progress = await screenModule(new URL('../src-next/kit/settings/SignInMoveProgress.tsx', import.meta.url), {
    react, 'react-native': {View: 'View'}, '../frames/metrics': frame, '../cards/primitives': {Progress: 'Progress'},
  });
  const parts = await screenModule(new URL('../src-next/kit/settings/SettingsParts.tsx', import.meta.url), {
    react, 'react-native': {Text: 'Text', View: 'View'}, 'react-native-qrcode-svg': {default: 'QRCode'},
    '@portico/design': {frameSpec: {settings: {managementQR: 200}}}, '../frames/metrics': frame, '../theme': {useTheme: theme},
    '../../tv/auth/TVHandoff': {publicTVHandoffAddress: (url: string) => ({url, display: url})},
    '../controls': {}, '../focus': {}, '../Icon': {}, '../shell/visual': {}, '../states/sentence-parts': {stateSentenceParts: () => []},
  });
  const model = await screenModule(new URL('../src-next/pages/shared/settings/useServerSignIn.ts', import.meta.url), {
    react, 'react-native': {Linking: {openURL: async (url: string) => {visits.push(url);}}},
    '../../../../../../packages/client-core/src/account-move': {OwnerSignInSettingsClient: class {constructor(transport: unknown, input: unknown) {assert.equal(transport, api); options.push(input);} read() {reads++; return result;} finish() {finishes++; return Promise.resolve(3);} dispose() {disposed++;}}},
    '../../../../../../packages/client-core/src/sign-in': {serverSignInSettingsURL: (origin: string) => {assert.equal(origin, api.baseUrl); return new URL('/settings?scope=server&section=signin', origin).href;}},
    '../../../kit/theme': {useTheme: theme}, '../../../screens/settings-live/use-settings-request-scope': {useSettingsRequestScope: () => scope},
  });
  const {ServerSignInPage} = await screenModule(new URL('../src-next/pages/touch/settings/ServerSignInPage.tsx', import.meta.url), {
    react, '../../shared/settings/useServerSignIn': model, '../../../kit/settings/SignInMoveProgress': progress, '../../../kit/settings/SettingsParts': parts,
    '../../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../../kit/overlays/surface': {OverlayFootRow: 'Footer'},
    '../../../../../../packages/i18n/src': {defaultI18n: {date: (value: string) => `date:${value}`}},
    '../../../kit/controls': {Button: 'Button'}, '../../../kit/shell': {SettingsRow: 'SettingsRow'},
    '../../../kit/states': {StateView: 'StateView'}, '../../../kit/frames': {PanelBlock: 'PanelBlock', SectionCard: 'SectionCard', SettingsPageFrame: 'SettingsPageFrame'},
  });
  const props = {ports: {context}, runtime: {text: {t: (id: string) => id}, error: () => ({title: 'failure'})}, onOpenPage: (id: string) => pages.push(id)};
  const render = () => {
    let tree = host.render(() => ServerSignInPage(props));
    if (effects.length) {effects.splice(0).forEach(run => run()); tree = host.render(() => ServerSignInPage(props));}
    return tree;
  };
  return {render, visits, pages, options, reads: () => reads, finishes: () => finishes, disposed: () => disposed, end: () => {alive = false;}, unmount: () => {cleanups.forEach(fn => fn());},
    reply: async (move: unknown = null) => {resolve({signIn: 'local', owner: {username: 'Owner'}, move}); await Promise.resolve(); await Promise.resolve();}};
}

test('Native Sign-in reads server facts and opens only the selected server web settings on an explicit action', async () => {
  const f = await fixture();
  try {
    f.render(); assert.equal(f.reads(), 1); assert.deepEqual(f.visits, []);
    assert.equal(f.options[0].serverId, 'server'); assert.equal(f.options[0].isCurrent(), true);
    await f.reply(); const tree = f.render();
    assert.ok(!nodes(tree).some(node => node.props.testID === 'server-signin-move'), 'no move, no move part');
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

test('A move shows its direction, since when and how many wait; the bar only with a recorded total; Finish now asks first', async () => {
  const f = await fixture(true, true);
  try {
    f.render(); await f.reply({to: 'local', since: '2026-11-12T10:00:00Z', waiting: 2});
    let tree = f.render();
    assert.ok(words(tree).includes('accounts.settings.movingToLocal')); assert.ok(words(tree).includes('accounts.settings.waitingCount'));
    assert.ok(words(tree).includes('accounts.settings.movingSince'));
    assert.equal(nodes(tree).find(node => node.type?.name === 'SignInMoveProgress'), undefined, 'no total, no bar and no "of"');
    const open = nodes(tree).find(node => node.type === 'SettingsRow' && node.props.label === 'accounts.settings.openPeople')!;
    open.props.onPress(); assert.deepEqual(f.pages, ['people']);
    const handoff = nodes(tree).find(node => node.type?.name === 'SettingsManagementCard');
    assert.equal(nodes(handoff.type(handoff.props)).find(node => node.type === 'QRCode').props.value, 'http://selected-server.invalid:8096/settings?scope=server&section=signin');
    const finishRow = nodes(tree).find(node => node.type === 'SettingsRow' && node.props.label === 'accounts.settings.notMovedTitle')!;
    finishRow.props.control.props.onPress(); tree = f.render();
    const sheet = nodes(tree).find(node => node.props.testID === 'server-signin-finish')!;
    assert.equal(sheet.props.open, true); assert.equal(f.finishes(), 0, 'nothing is finished before the confirmation');
    assert.deepEqual(f.visits, []);
    const confirm = nodes(sheet.props.footer).find(node => node.type === 'Button' && words(node.props.children) === 'accounts.settings.finishNow')!;
    f.end();
    confirm.props.onPress();
    assert.equal(f.finishes(), 0, 'a retired confirmation cannot finish a move');

  } finally {f.unmount();}
});

test('With a recorded total the move draws "N of M people waiting" and its bar', async () => {
  const f = await fixture(true, true);
  try {
    f.render(); await f.reply({to: 'portico', since: '2026-11-12T10:00:00Z', waiting: 2, total: 4});
    const tree = f.render(), progress = nodes(tree).find(node => node.type?.name === 'SignInMoveProgress');
    assert.deepEqual(progress.type(progress.props).props.accessibilityValue, {min: 0, max: 4, now: 2});
    assert.ok(words(tree).includes('accounts.settings.waiting')); assert.ok(words(tree).includes('accounts.settings.movingToPortico'));
  } finally {f.unmount();}
});
