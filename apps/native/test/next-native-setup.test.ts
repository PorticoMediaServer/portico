// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {fabricHost, React} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';
import {defaultI18n} from '../../../packages/i18n/src';
import {passwordStrength} from '../../../packages/client-core/src/password-strength';
import {setupBusyMessage, setupEndedMessage, setupErrorMessage, setupNewerMessage, setupStartOverOffer} from '../../../packages/client-core/src/presentation/setup-messages';

// NativeSetup draws whichever state the shared setup controller reports, through the phone layout stubs. The
// controller here is a recording fake: the screen's own wiring (subscriptions, form gates, Back, admission and
// disposal) is what these cases exercise, not the reducer already covered in client-core.
type NativeNode = {type: string; props: Record<string, any>; children: NativeNode[]};
const flatten = (nodes: NativeNode[]): NativeNode[] => nodes.flatMap(node => [node, ...flatten(node.children ?? [])]);
const textOf = (node: any): string => {
  if (node == null || typeof node === 'boolean') return '';
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(textOf).join('');
  if (typeof node.props?.text === 'string') return node.props.text;
  return textOf(node.children);
};
const t = defaultI18n.t;

type Call = {name: string; args: any[]};
function fakeController(initialView: any) {
  let view = initialView, busy = false;
  const listeners = new Set<() => void>();
  const calls: Call[] = [];
  const record = (name: string, ...args: any[]) => {calls.push({name, args});};
  const controller: any = {
    getSnapshot: () => view,
    busy: () => busy,
    subscribe(listener: () => void) {listeners.add(listener); return () => {listeners.delete(listener);};},
    start: () => record('start'),
    submitHosted: (name: string, options: any) => record('submitHosted', name, options),
    submitLocal: (name: string, username: string, password: string, remote?: unknown) => record('submitLocal', name, username, password, remote),
    chooseKind: (mode: string) => record('chooseKind', mode),
    back: () => record('back'),
    startAgain: () => record('startAgain'),
    startOver: () => record('startOver'),
    submitCode: (code: string) => record('submitCode', code),
    retry: () => record('retry'),
    approveInApp: () => record('approveInApp'),
    addLibrary: () => record('addLibrary'),
    skip: () => record('skip'),
    setForeground: (foreground: boolean) => record('setForeground', foreground),
    dispose: () => record('dispose'),
  };
  const notify = () => {for (const listener of [...listeners]) listener();};
  return {
    controller, calls,
    set(next: any) {view = next; notify();},
    setBusy(next: boolean) {busy = next; notify();},
    count: (name: string) => calls.filter(call => call.name === name).length,
    last: (name: string) => calls.filter(call => call.name === name).at(-1),
  };
}

type FixtureOptions = Readonly<{view: any; hostedConfigured?: boolean; session?: any; serverName?: string; serverId?: string; origin?: string}>;
async function fixture(options: FixtureOptions) {
  const made: ReturnType<typeof fakeController>[] = [];
  let adapterDisposals = 0;
  const opened: string[] = [];
  const appStateListeners = new Set<(state: string) => void>();
  const target: any = {origin: options.origin ?? 'https://studio.example',
    server: {id: options.serverId ?? 'studio-server', name: options.serverName ?? 'Studio', hostedConfigured: options.hostedConfigured ?? true}};
  const credentials = {getSnapshot: () => ({session: options.session}), subscribe: () => () => {}};
  const theme = {platform: 'phone', isTV: false, colors: {accentStrong: '#d00', line: '#ccc', text: '#111', textSecondary: '#666'},
    type: {bodyStrong: {}, caption: {}}};
  const {NativeSetup} = await screenModule(new URL('../src-next/screens/accounts/NativeSetup.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {AppState: {currentState: 'active', addEventListener: (_: string, handler: (state: string) => void) => {
      appStateListeners.add(handler); return {remove: () => appStateListeners.delete(handler)};}},
      Linking: {openURL: async (url: string) => {opened.push(url);}}, Text: 'Text', View: 'View'},
    '../../../../../packages/client-core/src/setup-controller': {createSetupController: () => {
      const fake = fakeController(options.view);
      made.push(fake);
      return fake.controller;
    }},
    '../../../../../packages/client-core/src/presentation/setup-messages': {setupBusyMessage, setupEndedMessage, setupErrorMessage, setupNewerMessage, setupStartOverOffer},
    '../../../../../packages/client-core/src/password-strength': {passwordStrength},
    '../../../../../packages/i18n/src': {defaultI18n},
    '../../kit/controls': {Button: 'Button', Focusable: 'Focusable', TextField: 'TextField'}, '../../kit/overlays': {BottomSheet: 'BottomSheet'}, '../../kit/Icon': {Icon: 'Icon'},
    '../../kit/states': {StateLayout: 'StateLayout'},
    '../../kit/theme': {useTheme: () => theme},
    '../../platform/accounts': {nativeAccounts: {accountCredentials: () => credentials}},
    '../../platform/setup-adapter': {createNativeSetupAdapter: (_target: any, _live: any) => ({ports: {}, dispose() {adapterDisposals++;}})},
    './PasswordStrengthMeter': {PasswordStrengthMeter: 'PasswordStrengthMeter'},
    // The layout and the approval sheet are real components here so their props stay inspectable and the form's
    // beforeActions children actually mount; every leaf they draw is still a string stub.
    './PhoneAccountForm': {PhoneAccountForm: (props: any) => React.createElement('PhoneAccountForm', props, props.beforeActions)},
    './PhoneSetupApproval': {PhoneSetupApproval: (props: any) => React.createElement('PhoneSetupApproval', props)},
  });
  const admitted: string[] = [], left: string[] = [];
  const element = React.createElement(NativeSetup, {target,
    onAdmitted: (serverId: string) => {admitted.push(serverId);}, onLeave: () => {left.push('left');}});
  const host = await fabricHost();
  await host.render(element);
  const find = (type: string) => flatten(host.root).find(node => node.type === type);
  const all = (type: string) => flatten(host.root).filter(node => node.type === type);
  return {
    host, target, opened, admitted, left, made,
    adapterDisposals: () => adapterDisposals,
    fake: () => made.at(-1)!,
    find, all,
    layout: () => find('PhoneAccountForm'),
    radios: () => all('Focusable').filter(node => node.props.accessibilityRole === 'radio' && !String(node.props.testID ?? '').startsWith('setup-')),
    choice: (id: string) => all('Focusable').find(node => node.props.testID === id),
    act: async (work: () => any) => {await work(); await host.settle();},
  };
}
const field = (f: Awaited<ReturnType<typeof fixture>>, label: string) =>
  f.all('TextField').find(node => node.props.label === label)!;
const view = (kind: string, extra: Record<string, any> = {}) => ({kind, ...extra});

test('the choice preselects Portico Accounts and Continue submits hosted, navigating only when the app has no Portico Account', async () => {
  const hosted = await fixture({view: view('form', {mode: 'choose', name: 'Studio'})});
  try {
    assert.equal(hosted.fake().count('start'), 1, 'mounting the screen starts the controller once');
    const radios = hosted.radios();
    assert.equal(radios.length, 2, 'both kinds are offered when the server has Portico Accounts');
    assert.equal(radios[0]!.props.accessibilityState.checked, true, 'Portico Accounts is preselected');
    assert.equal(radios[1]!.props.accessibilityState.checked, false);
    assert.match(textOf(radios[0]), /Portico Accounts · Recommended/);
    assert.match(textOf(radios[1]), /Local sign-in/);
    await hosted.act(() => hosted.layout().props.primaryAction.onPress());
    assert.equal(hosted.fake().count('submitHosted'), 1);
    assert.deepEqual(hosted.fake().last('submitHosted')!.args, ['Studio', {navigate: true, remote: {remoteAccess: 'on', remoteSecurity: 'enhanced'}}]);
    // O-169: Remote access On (Portico Accounts' default) shows Remote security, Enhanced preselected.
    assert.equal(hosted.choice('setup-remote-on')!.props.accessibilityState.checked, true);
    assert.equal(hosted.choice('setup-security-enhanced')!.props.accessibilityState.checked, true);
    assert.match(textOf(hosted.choice('setup-security-enhanced')), /Remote connections require HTTPS, and Portico is only accessible from outside your network via Portico domains and addresses you add explicitly/);
    await hosted.act(() => hosted.choice('setup-security-standard')!.props.onPress());
    await hosted.act(() => hosted.choice('setup-remote-private')!.props.onPress());
    assert.equal(hosted.choice('setup-security-enhanced'), undefined, 'Private only: no Remote security');
    await hosted.act(() => hosted.layout().props.primaryAction.onPress());
    assert.deepEqual(hosted.fake().last('submitHosted')!.args, ['Studio', {navigate: true, remote: {remoteAccess: 'private', remoteSecurity: 'standard'}}]);
  } finally {await hosted.host.unmount();}

  const signedIn = await fixture({view: view('form', {mode: 'choose', name: 'Studio'}), session: {account: {displayName: 'Justin', username: 'justin'}}});
  try {
    await signedIn.act(() => signedIn.layout().props.primaryAction.onPress());
    assert.deepEqual(signedIn.fake().last('submitHosted')!.args, ['Studio', {navigate: false, remote: {remoteAccess: 'on', remoteSecurity: 'enhanced'}}],
      'an app Portico Account approves in place, so there is no browser trip');
  } finally {await signedIn.host.unmount();}
});

test('choosing Local sign-in and continuing chooses the local kind and never submits hosted', async () => {
  const f = await fixture({view: view('form', {mode: 'choose', name: 'Studio'})});
  try {
    await f.act(() => f.radios()[1]!.props.onPress());
    assert.equal(f.radios()[1]!.props.accessibilityState.checked, true);
    assert.equal(f.radios()[0]!.props.accessibilityState.checked, false);
    await f.act(() => f.layout().props.primaryAction.onPress());
    assert.deepEqual(f.fake().last('chooseKind')!.args, ['local']);
    assert.equal(f.fake().count('submitHosted'), 0);
  } finally {await f.host.unmount();}

  const localOnly = await fixture({view: view('form', {mode: 'choose', name: 'Studio'}), hostedConfigured: false});
  try {
    assert.equal(localOnly.radios().length, 1, 'a server without Portico Accounts offers only local sign-in');
    assert.equal(localOnly.radios()[0]!.props.accessibilityState.checked, true);
    await localOnly.act(() => localOnly.layout().props.primaryAction.onPress());
    assert.deepEqual(localOnly.fake().last('chooseKind')!.args, ['local']);
    assert.equal(localOnly.fake().count('submitHosted'), 0);
  } finally {await localOnly.host.unmount();}
});

test('the local owner form enables Create owner only for a valid username and a matching acceptable password, then submits once and clears the secrets', async () => {
  const f = await fixture({view: view('form', {mode: 'local', name: 'Studio'})});
  try {
    const password = 'correct horse battery staple';
    assert.equal(f.layout().props.title, t('web.setup.ownerTitle'));
    assert.equal(f.layout().props.primaryAction.disabled, true);
    await f.act(() => field(f, t('auth.password')).props.onChangeText(password));
    await f.act(() => field(f, t('accounts.signIn.confirmPassword')).props.onChangeText(password));
    assert.equal(f.layout().props.primaryAction.disabled, true, 'a valid password is not enough without a username');
    await f.act(() => field(f, t('auth.username')).props.onChangeText('ab'));
    assert.equal(f.layout().props.primaryAction.disabled, true, 'a short username is refused');
    await f.act(() => field(f, t('auth.username')).props.onChangeText('owner'));
    assert.equal(f.layout().props.primaryAction.disabled, false);
    await f.act(() => field(f, t('accounts.signIn.confirmPassword')).props.onChangeText('correct horse battery stapl'));
    assert.equal(f.layout().props.primaryAction.disabled, true, 'a mismatched confirmation is refused');
    await f.act(() => field(f, t('accounts.signIn.confirmPassword')).props.onChangeText(password));
    assert.equal(f.layout().props.primaryAction.disabled, false);
    await f.act(() => f.layout().props.primaryAction.onPress());
    assert.equal(f.fake().count('submitLocal'), 1);
    assert.deepEqual(f.fake().last('submitLocal')!.args, ['Studio', 'owner', password, {remoteAccess: 'off', remoteSecurity: 'enhanced'}],
      'local sign-in: Remote access Off unless chosen (O-169)');
    assert.equal(field(f, t('auth.password')).props.value, '');
    assert.equal(field(f, t('accounts.signIn.confirmPassword')).props.value, '');
    assert.equal(field(f, t('auth.username')).props.value, 'owner');
  } finally {await f.host.unmount();}
});

test('awaiting approval with an app Portico Account shows the approval sheet, and Not now waits with the browser action and Back', async () => {
  const approvalUrl = 'https://portico.example/setup/approve/abc';
  const f = await fixture({view: view('awaiting_approval', {name: 'Studio', serverPhase: 'awaiting_approval', approvalUrl, polls: 0}),
    session: {account: {displayName: 'Justin Owner', username: 'justin'}}});
  try {
    const sheet = f.find('PhoneSetupApproval');
    assert.ok(sheet, 'the app approves in place when it holds a Portico Account');
    assert.equal(sheet!.props.display.serverName, 'Studio');
    assert.equal(sheet!.props.display.address, 'https://studio.example');
    assert.equal(sheet!.props.display.accountName, 'Justin Owner');
    assert.equal(f.layout(), undefined);
    await f.act(() => sheet!.props.onApprove());
    assert.equal(f.fake().count('approveInApp'), 1);
    await f.act(() => sheet!.props.onClose());
    assert.equal(f.find('PhoneSetupApproval'), undefined, 'Not now leaves the sheet for the waiting screen');
    const layout = f.layout()!;
    assert.equal(layout.props.title, t('web.claim.title', {server: 'Studio'}));
    assert.equal(layout.props.body, t('accounts.setup.waiting'));
    assert.equal(layout.props.primaryAction.id, 'open');
    assert.equal(layout.props.primaryAction.label, t('accounts.signIn.continuePortico'));
    await f.act(() => layout.props.primaryAction.onPress());
    assert.deepEqual(f.opened, [approvalUrl]);
    assert.ok(layout.props.secondaryAction, 'Back remains the ordinary way out');
    await f.act(() => f.layout()!.props.secondaryAction.onPress());
    assert.equal(f.fake().count('back'), 1);
    assert.equal(f.left.length, 0);
  } finally {await f.host.unmount();}
});

test('awaiting approval without an app Portico Account shows the waiting screen and the browser action directly', async () => {
  const approvalUrl = 'https://portico.example/setup/approve/xyz';
  const f = await fixture({view: view('awaiting_approval', {name: 'Studio', serverPhase: 'started', approvalUrl, polls: 0})});
  try {
    assert.equal(f.find('PhoneSetupApproval'), undefined);
    const layout = f.layout()!;
    assert.equal(layout.props.title, t('web.claim.title', {server: 'Studio'}));
    assert.equal(layout.props.primaryAction.id, 'open');
    await f.act(() => layout.props.primaryAction.onPress());
    assert.deepEqual(f.opened, [approvalUrl]);
  } finally {await f.host.unmount();}
});

test('a request the Portico Account declined says so with Continue and Back, and never offers to approve it again in the app', async () => {
  for (const session of [undefined, {account: {displayName: 'Justin', username: 'justin'}}]) {
    const f = await fixture({view: view('awaiting_approval', {name: 'Studio', serverPhase: 'started', polls: 0, declined: true}), ...(session ? {session} : {})});
    try {
      assert.equal(f.find('PhoneSetupApproval'), undefined, 'no in-app approval of a declined request');
      const layout = f.layout()!;
      assert.equal(layout.props.body, t('web.setup.declined'));
      assert.equal(layout.props.primaryAction.label, t('accounts.signIn.handoff'));
      assert.equal(layout.props.secondaryAction.label, t('action.back'));
      await f.act(() => layout.props.primaryAction.onPress());
      assert.equal(f.fake().count('retry'), 1, 'Continue asks again with a new request');
    } finally {await f.host.unmount();}
  }
});

test('Back steps through the form as on the web: out of setup from the choice, back to the choice from the owner form, and the controller elsewhere', async () => {
  const choice = await fixture({view: view('form', {mode: 'choose', name: 'Studio'})});
  try {
    await choice.act(() => choice.layout()!.props.secondaryAction.onPress());
    assert.equal(choice.left.length, 1);
    assert.equal(choice.fake().count('back'), 0, 'the choice hands Back to the shell instead of the controller');
  } finally {await choice.host.unmount();}

  const owner = await fixture({view: view('form', {mode: 'local', name: 'Studio'})});
  try {
    await owner.act(() => owner.layout()!.props.secondaryAction.onPress());
    assert.equal(owner.fake().count('chooseKind'), 1, 'the owner form goes back to the choice');
    assert.equal(owner.fake().count('back'), 0);
    assert.equal(owner.left.length, 0);
  } finally {await owner.host.unmount();}

  for (const state of [view('awaiting_approval', {name: 'Studio', serverPhase: 'awaiting_approval', approvalUrl: 'https://portico.example/a', polls: 0})]) {
    const f = await fixture({view: state});
    try {
      await f.act(() => f.layout()!.props.secondaryAction.onPress());
      assert.equal(f.fake().count('back'), 1);
      assert.equal(f.left.length, 0);
    } finally {await f.host.unmount();}
  }
});

test('admission reports the server once for the library and for done', async () => {
  for (const kind of ['library', 'done'] as const) {
    const f = await fixture({view: view(kind)});
    try {
      assert.deepEqual(f.admitted, ['studio-server']);
      await f.act(() => f.fake().set({kind}));
      assert.deepEqual(f.admitted, ['studio-server'], 'a later render of the same admitted state never reports twice');
    } finally {await f.host.unmount();}
  }
});

test('a failed setup offers Try again that retries the same controller step', async () => {
  const error = {code: 'network', retryable: true};
  const f = await fixture({view: view('failed', {error, retryable: true, name: 'Studio', hold: 'none', retry: {to: view('form', {mode: 'choose', name: 'Studio'}), effects: []}})});
  try {
    const layout = f.layout()!;
    assert.equal(layout.props.title, t('accounts.setup.title'));
    assert.equal(layout.props.body, setupErrorMessage(error));
    assert.equal(layout.props.primaryAction.id, 'retry');
    assert.equal(layout.props.primaryAction.label, t('action.tryAgain'));
    await f.act(() => layout.props.primaryAction.onPress());
    assert.equal(f.fake().count('retry'), 1);
    assert.equal(f.fake().count('startAgain'), 0);
  } finally {await f.host.unmount();}
});

test('an ended setup offers Try again that starts again', async () => {
  const f = await fixture({view: view('ended', {outcome: 'cancelled', name: 'Studio'})});
  try {
    const layout = f.layout()!;
    assert.equal(layout.props.title, t('accounts.setup.declinedTitle', {server: 'Studio'}));
    assert.equal(layout.props.body, setupEndedMessage('denied', 'Studio'));
    await f.act(() => layout.props.primaryAction.onPress());
    assert.equal(f.fake().count('startAgain'), 1);
    assert.equal(f.fake().count('retry'), 0);
  } finally {await f.host.unmount();}
});

test('an exhausted setup and an unknown phase each offer Try again that retries', async () => {
  const exhausted = await fixture({view: view('exhausted', {polls: 9})});
  try {
    assert.equal(exhausted.layout()!.props.title, t('web.claim.finishingOnServer'));
    await exhausted.act(() => exhausted.layout()!.props.primaryAction.onPress());
    assert.equal(exhausted.fake().count('retry'), 1);
  } finally {await exhausted.host.unmount();}

  const unknown = await fixture({view: view('unknown')});
  try {
    assert.equal(unknown.layout()!.props.title, t('accounts.setup.title'));
    assert.equal(unknown.layout()!.props.body, setupNewerMessage());
    await unknown.act(() => unknown.layout()!.props.primaryAction.onPress());
    assert.equal(unknown.fake().count('retry'), 1);
  } finally {await unknown.host.unmount();}
});

test('a refused setup explains itself with no Try again', async () => {
  const cases: Array<[string, string]> = [
    ['not_local_source', t('web.setup.notLocalSource')],
    ['address_not_allowed', t('web.setup.notOnNetwork')],
    ['already_set_up', setupErrorMessage({code: 'setup_complete', retryable: false})],
  ];
  for (const [reason, body] of cases) {
    const f = await fixture({view: view('refused', {reason})});
    try {
      const layout = f.layout()!;
      assert.equal(layout.props.title, t('accounts.setup.title'));
      assert.equal(layout.props.body, body, `${reason} states the refusal, never a guess`);
      assert.equal(layout.props.primaryAction, undefined, `${reason} has no Try again`);
    } finally {await f.host.unmount();}
  }
});

test('unmounting the screen disposes the controller and its adapter once', async () => {
  const f = await fixture({view: view('form', {mode: 'choose', name: 'Studio'})});
  await f.host.unmount();
  assert.equal(f.fake().count('dispose'), 1);
  assert.equal(f.adapterDisposals(), 1);
});

test('start over: a setup busy elsewhere starts over at once; an exhausted (approved) one asks first', async () => {
  const busy = await fixture({view: view('busy_elsewhere', {polls: 0})});
  try {
    const layout = busy.layout()!;
    assert.equal(layout.props.primaryAction.label, t('web.setup.startOver'));
    await busy.act(() => layout.props.primaryAction.onPress());
    assert.equal(busy.fake().count('startOver'), 1);
  } finally {await busy.host.unmount();}

  const exhausted = await fixture({view: view('exhausted', {polls: 2})});
  try {
    const layout = exhausted.layout()!;
    assert.equal(layout.props.secondaryAction.label, t('web.setup.startOver'));
    await exhausted.act(() => layout.props.secondaryAction.onPress());
    assert.equal(exhausted.fake().count('startOver'), 0, 'it asks first');
    const sheet = exhausted.find('BottomSheet')!;
    assert.equal(sheet.props.open, true);
    assert.equal(sheet.props.title, t('web.setup.startOverTitle'));
    const confirm = ([] as any[]).concat(sheet.props.footer.props.children).find((node: any) => node?.props?.testID === 'accounts-setup-start-over-confirm');
    await exhausted.act(() => confirm.props.onPress());
    assert.equal(exhausted.fake().count('startOver'), 1);
  } finally {await exhausted.host.unmount();}
});

test('from outside: the setup code screen takes the code from the log and sends it', async () => {
  const starting = view('starting', {mode: 'hosted', name: 'Studio', navigate: false, step: 'start'});
  const f = await fixture({view: view('code', {name: 'Studio', invalid: true, resume: starting})});
  try {
    const layout = f.layout()!;
    assert.equal(layout.props.title, t('web.setup.codeTitle'));
    assert.equal(layout.props.body, t('web.setup.codeInvalid'));
    assert.equal(layout.props.primaryAction.disabled, true, 'nothing to send yet');
    await f.act(() => field(f, t('web.setup.codeLabel')).props.onChangeText('R9TD-3MWK'));
    await f.act(() => f.layout()!.props.primaryAction.onPress());
    assert.equal(f.fake().count('submitCode'), 1);
  } finally {await f.host.unmount();}
});
