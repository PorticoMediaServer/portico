// @fabric-react
import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import * as React from 'react';
import {defaultI18n} from '../../../packages/i18n/src/index.ts';
import {createTheme, textLines} from '../src-next/kit/theme.ts';
import * as registryModule from '../src-next/kit/focus/registry.ts';
import {fabricHost} from './helpers/fabric-host.mjs';
import {screenModule} from './helpers/screen-harness.mjs';

type HostNode = {tag: number; type: unknown; props: Record<string, any>; children: HostNode[]};
const allNodes = (list: HostNode[]): HostNode[] => list.flatMap(n => [n, ...allNodes(n.children ?? [])]);

// Phase 1-4 architectural invariants verified through the real production Fabric reconciler.

test('FocusRegion & useFocusTarget short-circuit on touch (plain View, zero FocusRegistry registrations/subscriptions)', async () => {
  const touchTheme = createTheme({isTV: false, width: 390});
  const native = {
    Platform: {OS: 'android', isTV: false},
    View: 'View',
    TVFocusGuideView: 'TVFocusGuideView',
    findNodeHandle: (host: any) => host?.tag ?? null,
  };
  const {useHostRef} = await screenModule(new URL('../src-next/kit/focus/useHostRef.ts', import.meta.url), {react: {...React, default: React}});
  const {FocusProvider, FocusRegion, FocusWithin, useFocusTarget} = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': native,
    '../theme': {useTheme: () => touchTheme},
    './registry': registryModule,
    './cell-navigation': {},
    './useHostRef': {useHostRef},
    './CoveredLayer': {useLayerCovered: () => false},
  });

  const baseRegistry = registryModule.createFocusRegistry();
  let regionSubscribes = 0, targetSubscribes = 0, attachCalls = 0, mountCalls = 0;
  const spyRegistry = {
    ...baseRegistry,
    subscribeRegion: (...args: Parameters<typeof baseRegistry.subscribeRegion>) => {regionSubscribes++; return baseRegistry.subscribeRegion(...args);},
    subscribeTarget: (...args: Parameters<typeof baseRegistry.subscribeTarget>) => {targetSubscribes++; return baseRegistry.subscribeTarget(...args);},
    attach: (...args: Parameters<typeof baseRegistry.attach>) => {attachCalls++; return baseRegistry.attach(...args);},
    mountRegion: (...args: Parameters<typeof baseRegistry.mountRegion>) => {mountCalls++; return baseRegistry.mountRegion(...args);},
  };

  const focusWithinEvents: boolean[] = [];
  function TouchChild() {
    const target = useFocusTarget({focusId: 'item-1', preferred: true});
    return React.createElement('View', {testID: target.testID, ref: target.ref, onFocus: target.onFocus, onBlur: target.onBlur});
  }

  const host = await fabricHost({publicInstance: tag => ({tag, requestTVFocus() {throw new Error('touch must not request TV focus');}})});
  try {
    await host.render(
      React.createElement(FocusProvider, {registry: spyRegistry},
        React.createElement(FocusWithin, {onChange: (focused: boolean) => focusWithinEvents.push(focused)},
          React.createElement(FocusRegion, {id: 'shelf-1', testID: 'touch-region', orientation: 'horizontal'},
            React.createElement(TouchChild),
          ),
        ),
      ),
    );
    const committed = allNodes(host.root as HostNode[]);
    assert.equal(committed.some(n => n.type === 'TVFocusGuideView'), false, 'touch never mounts TVFocusGuideView');
    const regionView = committed.find(n => n.props.testID === 'touch-region');
    assert.equal(regionView?.type, 'View', 'touch FocusRegion renders a plain View');
    assert.equal(baseRegistry.size().regions, 0, 'zero regions registered in FocusRegistry on touch');
    assert.equal(mountCalls, 0, 'mountRegion never called on touch');
    assert.equal(attachCalls, 0, 'attach never called on touch');
    assert.equal(regionSubscribes, 0, 'zero region subscriptions on touch');
    assert.equal(targetSubscribes, 0, 'zero target subscriptions on touch');

    // FocusWithin callbacks still fire cleanly on touch.
    const childView = committed.find(n => n.props.testID === registryModule.focusTestID('shelf-1', 'item-1'));
    assert.ok(childView, 'child keeps deterministic testID');
    childView.props.onFocus();
    childView.props.onBlur();
    assert.deepEqual(focusWithinEvents, [true, false]);
  } finally {
    await host.unmount();
  }
});

test('FocusRegion & useFocusTarget on TV mount TVFocusGuideView autoFocus, register region & children, and fire single-shot preferred focus', async () => {
  const tvTheme = createTheme({isTV: true, width: 1920});
  const native = {
    Platform: {OS: 'ios', isTV: true},
    View: 'View',
    TVFocusGuideView: 'TVFocusGuideView',
    findNodeHandle: (host: any) => host?.tag ?? null,
  };
  const {useHostRef} = await screenModule(new URL('../src-next/kit/focus/useHostRef.ts', import.meta.url), {react: {...React, default: React}});
  const {FocusProvider, FocusRegion, useFocusTarget} = await screenModule(new URL('../src-next/kit/focus/FocusRegion.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': native,
    '../theme': {useTheme: () => tvTheme},
    './registry': registryModule,
    './cell-navigation': {},
    './useHostRef': {useHostRef},
    './CoveredLayer': {useLayerCovered: () => false},
  });

  const registry = registryModule.createFocusRegistry();
  const focusRequests: number[] = [];
  const guideDestinations: Array<{tag: number; handles: number[]}> = [];

  function TVCard({id, preferred = false, blocked = false}: {id: string; preferred?: boolean; blocked?: boolean}) {
    const target = useFocusTarget({focusId: id, preferred, preferredBlocked: blocked});
    return React.createElement('View', {
      testID: target.testID,
      ref: target.ref,
      nextFocusRight: target.nextFocusRight,
      onFocus: target.onFocus,
      onBlur: target.onBlur,
    });
  }

  function Scene({preferred, blocked, tick}: {preferred: boolean; blocked: boolean; tick: number}) {
    return React.createElement(FocusProvider, {registry},
      React.createElement('View', {testID: `tick-${tick}`},
        React.createElement(FocusRegion, {id: 'rail', orientation: 'vertical', edges: {right: 'page'}},
          React.createElement(TVCard, {id: 'home'}),
        ),
        React.createElement(FocusRegion, {id: 'page', orientation: 'horizontal', edges: {left: 'rail'}},
          React.createElement(TVCard, {id: 'card-1', preferred, blocked}),
        ),
      ),
    );
  }

  const host = await fabricHost({
    publicInstance: tag => ({
      tag,
      requestTVFocus() {focusRequests.push(tag);},
      setDestinations(handles: number[]) {guideDestinations.push({tag, handles});},
    }),
  });
  try {
    // 1) Mount with preferredBlocked=true -> deferred (zero focus requests yet), regions & guides still registered.
    await host.render(React.createElement(Scene, {preferred: true, blocked: true, tick: 1}));
    const guides = allNodes(host.root as HostNode[]).filter(n => n.type === 'TVFocusGuideView');
    assert.equal(guides.length, 2, 'both TV FocusRegions render TVFocusGuideView');
    for (const guide of guides) {
      assert.equal(guide.props.autoFocus, true, 'TVFocusGuideView mounts with autoFocus');
      assert.equal(guide.props.focusable, true);
    }
    assert.equal(registry.size().regions, 2, 'both regions registered in FocusRegistry');
    assert.ok(registry.child({regionId: 'page', childId: 'card-1'}), 'child attached in FocusRegistry');
    assert.ok(guideDestinations.some(d => d.handles.length === 1), 'TVFocusGuideView received destination handle');
    assert.equal(focusRequests.length, 0, 'preferredBlocked defers preferred focus');

    // Edge wire: rail item points nextFocusRight to page guide handle.
    const railItem = allNodes(host.root as HostNode[]).find(n => n.props.testID === registryModule.focusTestID('rail', 'home'));
    assert.equal(typeof railItem?.props.nextFocusRight, 'number', 'rail child wired nextFocusRight to page region');

    // 2) Unblock -> single-shot preferred focus fires once.
    await host.render(React.createElement(Scene, {preferred: true, blocked: false, tick: 2}));
    assert.equal(focusRequests.length, 1, 'preferred focus fires once on unblock');

    // 3) Unrelated re-render with preferred still true -> does NOT fire again (single-shot).
    await host.render(React.createElement(Scene, {preferred: true, blocked: false, tick: 3}));
    assert.equal(focusRequests.length, 1, 're-render does not re-ask preferred focus');

    // 4) Toggling preferred off then on re-arms single-shot focus.
    await host.render(React.createElement(Scene, {preferred: false, blocked: false, tick: 4}));
    await host.render(React.createElement(Scene, {preferred: true, blocked: false, tick: 5}));
    assert.equal(focusRequests.length, 2, 'turning preferred off then on asks again');
  } finally {
    await host.unmount();
  }
});

test('GuideProgrammeTitle renders deterministic textLines truncation with zero timers and immediate onLayout height reporting', async () => {
  let timersCreated = 0;
  const origSetTimeout = globalThis.setTimeout;
  const origSetInterval = globalThis.setInterval;
  (globalThis as any).setTimeout = (...args: any[]) => {timersCreated++; return (origSetTimeout as any)(...args);};
  (globalThis as any).setInterval = (...args: any[]) => {timersCreated++; return (origSetInterval as any)(...args);};

  const {GuideProgrammeTitle} = await screenModule(new URL('../src-next/kit/live/GuideProgrammeTitle.tsx', import.meta.url), {
    react: {...React, default: React},
    'react-native': {Text: 'Text', View: 'View'},
    '../theme': {textLines},
  });

  const reportedHeights: number[] = [];
  const host = await fabricHost();
  try {
    await host.render(React.createElement(GuideProgrammeTitle, {
      title: 'Evening News Live Special',
      lines: 2,
      style: {fontSize: 15, lineHeight: 20},
      maxHeight: 40,
      onHeight: (h: number) => reportedHeights.push(h),
    }));
    assert.equal(timersCreated, 0, 'GuideProgrammeTitle schedules zero timers');
    const textNode = allNodes(host.root as HostNode[]).find(n => n.type === 'Text');
    assert.ok(textNode, 'renders Text node');
    assert.equal(textNode.props.numberOfLines, 2);
    assert.equal(textNode.props.ellipsizeMode, 'tail');
    assert.equal(textNode.props.textBreakStrategy, 'simple');
    assert.equal(textNode.props.android_hyphenationFrequency, 'none');
    assert.deepEqual(textNode.props.style, [{fontSize: 15, lineHeight: 20}, {maxHeight: 40}]);

    // Trigger onLayout -> reports height immediately with zero timers.
    textNode.props.onLayout({nativeEvent: {layout: {height: 38}}});
    assert.deepEqual(reportedHeights, [38]);
    assert.equal(timersCreated, 0, 'onLayout reporting creates zero timers');

    // Empty title renders null.
    await host.render(React.createElement(GuideProgrammeTitle, {title: '', lines: 2, style: {fontSize: 15}}));
    assert.equal((host.root as HostNode[]).length, 0, 'empty title renders nothing');
  } finally {
    await host.unmount();
    globalThis.setTimeout = origSetTimeout;
    globalThis.setInterval = origSetInterval;
  }
});

test('ProfileGateView (ProfilePickerStage & ProfilePINStage) renders shared picker, PIN entry, and errors across Touch & TV', async () => {
  const {ProfilePickerStage, ProfilePINStage} = (await screenModule(new URL('../src-next/pages/shared/accounts/ProfileGateView.tsx', import.meta.url), {
    react: {...React, default: React},
    '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../kit/artwork': {ProfilePicker: 'ProfilePicker'},
    '../../../kit/accounts/RememberProfileRow': {RememberProfileRow: 'RememberProfileRow'},
    '../../../shell/StartupBoot': {BootWaitLine: 'BootWaitLine'},
    '../../../tv/auth': {
      TVForgotPINHandoff: 'TVForgotPINHandoff',
      TVPickerRememberRow: 'TVPickerRememberRow',
      TVProfilePINForm: 'TVProfilePINForm',
      TVWhosWatching: 'TVWhosWatching',
    },
    '../../touch/accounts/ProfilePINPage': {ProfilePINPage: 'ProfilePINPage'},
    '../../touch/accounts/ProfilesPage': {ProfilesPage: 'ProfilesPage'},
    '../../touch/accounts/ProfileRecoveryPage': {ProfileRecoveryPage: 'ProfileRecoveryPage'},
  })) as Record<string, React.ComponentType<any>>;

  const profiles = [
    {id: 'p1', name: 'Alex', initial: 'A', color: 'blue', primary: true, pinRequired: false},
    {id: 'p2', name: 'Jordan', initial: 'J', color: 'mint', primary: false, pinRequired: true},
  ];
  const host = await fabricHost();
  try {
    // 1) ProfilePickerStage on Touch and TV
    for (const tv of [false, true]) {
      const selected: string[] = [];
      const remembered: boolean[] = [];
      await host.render(React.createElement(ProfilePickerStage, {
        tv,
        testID: 'gate-picker',
        serverName: 'Living Room',
        focusScope: 'scope-1',
        busy: false,
        profiles,
        onSelect: (id: string) => selected.push(id),
        canRemember: true,
        remember: true,
        onRememberChange: (v: boolean) => remembered.push(v),
        rememberFocusId: 'remember-1',
        status: React.createElement('StatusError', {message: 'picker-error'}),
        waitTestID: 'gate-wait',
      }));
      const committed = allNodes(host.root as HostNode[]);
      assert.ok(committed.some(n => n.type === (tv ? 'TVWhosWatching' : 'ProfilesPage')), `${tv ? 'TV' : 'Touch'} wrapper mounted`);
      const picker = tv
        ? committed.find(n => n.type === 'ProfilePicker')
        : committed.find(n => n.type === 'ProfilesPage')?.props.picker;
      assert.ok(picker, 'ProfilePicker rendered');
      picker.props.onSelect('p2');
      assert.deepEqual(selected, ['p2']);
      const rememberNode = committed.find(n => n.type === (tv ? 'TVPickerRememberRow' : 'RememberProfileRow'));
      assert.ok(rememberNode, 'remember control rendered');
      rememberNode.props.onChange(false);
      assert.deepEqual(remembered, [false]);
      assert.ok(committed.some(n => n.type === 'StatusError'), 'status node rendered');
    }

    // 2) ProfilePINStage on Touch and TV (PIN entry, error display, and Forgot/Recovery transitions)
    for (const tv of [false, true]) {
      const typed: string[] = [], submitted: string[] = [];
      await host.render(React.createElement(ProfilePINStage, {
        tv,
        testID: 'gate-pin',
        profile: {id: 'p2', name: 'Jordan', face: {initial: 'J', color: 'mint'}, primary: false, pinRequired: true},
        code: '12',
        onCodeChange: (v: string) => typed.push(v),
        onSubmit: (v: string) => submitted.push(v),
        busy: false,
        rejected: true,
        error: React.createElement('PinErr', {text: 'Wrong PIN'}),
        status: React.createElement('PinErr', {text: 'Wrong PIN'}),
        onBack() {},
        onForgot() {},
      }));
      const committed = allNodes(host.root as HostNode[]);
      const pinNode = committed.find(n => n.type === (tv ? 'TVProfilePINForm' : 'ProfilePINPage'));
      assert.ok(pinNode, `${tv ? 'TV' : 'Touch'} PIN stage rendered`);
      if (tv) {
        pinNode.props.onCodeChange('1234');
        assert.equal(pinNode.props.error.props.text, 'Wrong PIN');
      } else {
        pinNode.props.onChange('1234');
        assert.equal(pinNode.props.status.props.text, 'Wrong PIN');
      }
      pinNode.props.onSubmit('1234');
      assert.deepEqual(typed, ['1234']);
      assert.deepEqual(submitted, ['1234']);
    }

    // TV Forgot PIN handoff & Touch PIN recovery branches
    await host.render(React.createElement(ProfilePINStage, {
      tv: true,
      code: '',
      onCodeChange() {},
      onSubmit() {},
      busy: false,
      rejected: false,
      onBack() {},
      onForgot() {},
      forgot: {profileName: 'Jordan', serverAddress: 'https://home.test', server: {id: 'srv', signIn: 'local'}},
    }));
    assert.ok(allNodes(host.root as HostNode[]).some(n => n.type === 'TVForgotPINHandoff'), 'TV forgot renders TVForgotPINHandoff');

    await host.render(React.createElement(ProfilePINStage, {
      tv: false,
      code: '',
      onCodeChange() {},
      onSubmit() {},
      busy: false,
      rejected: false,
      onBack() {},
      onForgot() {},
      recovery: {controller: {} as any, profileName: 'Jordan', username: 'user', onOpen() {}, onClose() {}},
    }));
    assert.ok(allNodes(host.root as HostNode[]).some(n => n.type === 'ProfileRecoveryPage'), 'recovery renders ProfileRecoveryPage');
  } finally {
    await host.unmount();
  }
});

test('kit/navigation/back.ts enforces observer -> overlay -> surface -> page -> shell precedence with active() gating across mounted components', async () => {
  const subscriptions: Array<() => boolean> = [];
  const BackHandler = {
    addEventListener: (_event: string, fn: () => boolean) => {
      subscriptions.push(fn);
      return {remove: () => {const idx = subscriptions.indexOf(fn); if (idx >= 0) subscriptions.splice(idx, 1);}};
    },
  };
  const back = await screenModule(new URL('../src-next/kit/navigation/back.ts', import.meta.url), {
    react: {default: {}},
    'react-native': {BackHandler},
  });

  const trace: string[] = [];
  let pageActive = false;
  let overlayOpen = false;
  let surfaceOpen = false;

  function BackSubscriber({label, scope, consumes, active}: {label: string; scope: string; consumes: boolean; active?: () => boolean}) {
    React.useEffect(() => back.registerBack(() => {trace.push(label); return consumes;}, {scope, ...(active ? {active} : {})}), [label, scope, consumes, active]);
    return null;
  }

  const host = await fabricHost();
  try {
    // Mount in reverse order (shell first, observer last) to prove scope precedence beats registration order.
    await host.render(React.createElement(React.Fragment, null,
      React.createElement(BackSubscriber, {label: 'shell', scope: 'shell', consumes: true}),
      React.createElement(BackSubscriber, {label: 'page', scope: 'page', consumes: true, active: () => pageActive}),
      React.createElement(BackSubscriber, {label: 'surface', scope: 'surface', consumes: true, active: () => surfaceOpen}),
      React.createElement(BackSubscriber, {label: 'overlay', scope: 'overlay', consumes: true, active: () => overlayOpen}),
      React.createElement(BackSubscriber, {label: 'observer', scope: 'observer', consumes: true}),
    ));
    assert.equal(subscriptions.length, 1, 'single platform BackHandler subscription');
    assert.equal(back.backRegistrations(), 5);

    const pressBack = () => subscriptions[0]!();

    // All conditional scopes inactive -> observer sees it (does not consume), shell handles it.
    trace.length = 0;
    assert.equal(pressBack(), true);
    assert.deepEqual(trace, ['observer', 'shell']);

    // Page becomes active -> observer -> page wins over shell.
    pageActive = true;
    trace.length = 0;
    assert.equal(pressBack(), true);
    assert.deepEqual(trace, ['observer', 'page']);

    // Surface becomes active -> observer -> surface wins over page & shell.
    surfaceOpen = true;
    trace.length = 0;
    assert.equal(pressBack(), true);
    assert.deepEqual(trace, ['observer', 'surface']);

    // Overlay becomes active -> observer -> overlay wins over surface, page & shell.
    overlayOpen = true;
    trace.length = 0;
    assert.equal(pressBack(), true);
    assert.deepEqual(trace, ['observer', 'overlay']);
  } finally {
    await host.unmount();
  }
  assert.equal(back.backRegistrations(), 0, 'unmounting releases every Back registration');
  assert.equal(subscriptions.length, 0, 'platform BackHandler subscription removed when last handler unmounts');
});

test('device-smoke.mjs supports --help, --dry-run (phone & tv), and fails closed without AGENT_DEVICE_BIN', () => {
  const script = fileURLToPath(new URL('../scripts/device-smoke.mjs', import.meta.url));
  const help = spawnSync(process.execPath, [script, '--help'], {encoding: 'utf8'});
  assert.equal(help.status, 0, help.stderr);
  assert.match(help.stdout, /AGENT_DEVICE_BIN/);

  for (const mode of ['phone', 'tv'] as const) {
    const dry = spawnSync(process.execPath, [script, '--dry-run', '--mode', mode], {encoding: 'utf8', env: {...process.env, AGENT_DEVICE_BIN: ''}});
    assert.equal(dry.status, 0, dry.stderr);
    const parsed = JSON.parse(dry.stdout);
    assert.equal(parsed.mode, mode);
    assert.equal(parsed.dryRun, true);
    assert.ok(Array.isArray(parsed.steps) && parsed.steps.length >= 8);
  }

  const missing = spawnSync(process.execPath, [script, '--mode', 'phone'], {encoding: 'utf8', env: {...process.env, AGENT_DEVICE_BIN: ''}});
  assert.notEqual(missing.status, 0, 'fails closed when AGENT_DEVICE_BIN is unset');
  assert.match(missing.stderr, /AGENT_DEVICE_BIN/);
});
