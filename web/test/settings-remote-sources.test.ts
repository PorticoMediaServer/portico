import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {renderToStaticMarkup} from 'react-dom/server';
import {transformTsx} from './transform-tsx.mjs';

// The page fences a selection on the app router's rendered location; the test
// supplies that boundary rather than compiling the whole router tree.
const routerModule = `export const router = {state: {location: {href: '/', pathname: '/', search: {}, searchStr: ''}}, subscribe: () => () => {}, navigate: () => {}};`;
registerHooks({
  resolve(specifier, context, next) {
    if ((specifier === './router' || specifier === '../router' || specifier.endsWith('/router')) && context.parentURL?.includes('/web/src/')) {
      return {url: 'remote-sources:router', shortCircuit: true};
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'remote-sources:router') return {format: 'module', source: routerModule, shortCircuit: true};
    if (url.endsWith('.module.css')) return {format: 'module', source: 'export default new Proxy({}, {get: (_, key) => String(key)});', shortCircuit: true};
    if (url.endsWith('.tsx')) return {format: 'module', source: transformTsx(url), shortCircuit: true};
    return next(url, context);
  },
});
const {defaultI18n} = await import('../../packages/i18n/src/index.ts');
const {sourceReadPreset, sourceReadPresetName} = await import('../../packages/client-core/src/remote-sources.ts');
const {RemoteSourcesPage, remoteErrorText} = await import('../src/screens/settings/remote-sources/RemoteSourcesPage.tsx');
// The page's reader admits the server's original empty list, with its viewer fence.
const emptySources = async () => ({scope: {serverId: 'server', viewerFence: 'fence'}, sources: [], mountSupported: false});
const pageProps: Parameters<typeof RemoteSourcesPage>[0] = {
  api: {request: emptySources} as never,
  serverId: 'server', viewerId: 'owner', isCurrent: () => true, reportError: () => {},
};

/** One mounted component: its hook slots, in the order React would keep them. */
type Instance = {slots: any[]; mounted: boolean};
/**
 * The hooks these screens use, driven the way React drives them: a changed
 * dependency tuple retires its previous effect *before* the next one runs, a
 * changed subscription unsubscribes before subscribing again, and unmounting
 * retires every effect, reader and subscription the component owned. Only those
 * hooks are modelled; the components themselves still do all the work.
 */
const hookRuntime = (React: any) => {
  const internals = React.__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE;
  const previous = internals.H;
  const instances = new Map<string, Instance>();
  const all: Instance[] = [];
  const changed = (next: readonly any[] | undefined, old: readonly any[] | undefined) =>
    !next || !old || next.length !== old.length || next.some((value, index) => !Object.is(value, old[index]));
  const mount = (name: string, component: any, props: any) => {
    let instance = instances.get(name);
    if (!instance?.mounted) {
      instance = {slots: [], mounted: true};
      instances.set(name, instance);
      all.push(instance);
    }
    const owner = instance;
    let tree: any, dirty = false;
    do {
      dirty = false;
      let cursor = 0;
      const retirements: (() => void)[] = [], setups: (() => void)[] = [];
      const effect = (run: () => any, deps: readonly any[] | undefined) => {
        const slot = owner.slots[cursor++] ??= {};
        if (!changed(deps, slot.deps)) return;
        slot.deps = deps ? [...deps] : undefined;
        const retire = slot.cleanup;
        if (typeof retire === 'function') retirements.push(() => {slot.cleanup = undefined;retire();});
        setups.push(() => {slot.cleanup = run() ?? undefined;});
      };
      const dispatcher = {
        useState(initial: any) {
          const slot = owner.slots[cursor++] ??= {};
          if (!('value' in slot)) slot.value = typeof initial === 'function' ? initial() : initial;
          const set = (next: any) => {if (!owner.mounted) return;slot.value = typeof next === 'function' ? next(slot.value) : next;dirty = true;};
          return [slot.value, set];
        },
        useRef(initial: any) {const slot = owner.slots[cursor++] ??= {};return slot.ref ??= {current: initial};},
        useEffect(run: () => any, deps?: any[]) {effect(run, deps);},
        useLayoutEffect(run: () => any, deps?: any[]) {effect(run, deps);},
        useMemo(factory: () => any, deps: any[]) {const slot = owner.slots[cursor++] ??= {};if (changed(deps, slot.deps)) {slot.deps = [...deps];slot.value = factory();}return slot.value;},
        useCallback(fn: any, deps: any[]) {const slot = owner.slots[cursor++] ??= {};if (changed(deps, slot.deps)) {slot.deps = [...deps];slot.value = fn;}return slot.value;},
        useSyncExternalStore(subscribe: (onChange: () => void) => () => void, getSnapshot: () => any) {
          const slot = owner.slots[cursor++] ??= {};
          if (slot.subscribe !== subscribe) {
            const retire = slot.unsubscribe;
            if (typeof retire === 'function') retirements.push(() => {slot.unsubscribe = undefined;retire();});
            slot.subscribe = subscribe;
            setups.push(() => {slot.unsubscribe = subscribe(() => {dirty = true;});});
          }
          return getSnapshot();
        },
      };
      internals.H = dispatcher;
      try {tree = component(props);} finally {internals.H = previous;}
      for (const retire of retirements) retire();
      for (const setup of setups) setup();
    } while (dirty);
    return tree;
  };
  const finishAll = (tasks: (() => void)[]) => {
    let failed = false, failure: unknown;
    for (const task of tasks) try {task();} catch (error) {if (!failed) {failed = true; failure = error;}}
    if (failed) throw failure;
  };
  const unmount = (name: string) => {
    const instance = instances.get(name);
    if (!instance?.mounted) return;
    instance.mounted = false;
    instances.delete(name);
    const tasks: (() => void)[] = [];
    for (const slot of instance.slots) {
      const retire = slot?.cleanup;
      if (slot) slot.cleanup = undefined;
      if (typeof retire === 'function') tasks.push(retire);
      const unsubscribe = slot?.unsubscribe;
      if (slot) slot.unsubscribe = undefined;
      if (typeof unsubscribe === 'function') tasks.push(unsubscribe);
    }
    finishAll(tasks);
  };
  return {
    mount, unmount,
    /** Retires all independently mounted fixtures, even if one cleanup fails. */
    unmountAll: () => finishAll([...instances.keys()].reverse().map(name => () => unmount(name))),
    /** Every instance's committed state, including retired ones, for a before/after compare. */
    snapshot: () => JSON.stringify(all.map(instance => instance.slots.map(slot => slot?.value ?? slot?.ref ?? null))),
  };
};

test('the four reading choices are the server SourceReadPreset values', () => {
  assert.deepEqual(sourceReadPreset('list'), {scanDepth: 'file_list_only', wholeFileReads: false, connections: 2, requestsPerSecond: 2, bytesPerSecond: 0});
  assert.deepEqual(sourceReadPreset('once'), {scanDepth: 'basic', wholeFileReads: false, connections: 4, requestsPerSecond: 4, bytesPerSecond: 0});
  assert.deepEqual(sourceReadPreset('everything'), {scanDepth: 'complete', wholeFileReads: false, connections: 8, requestsPerSecond: 10, bytesPerSecond: 0});
  assert.deepEqual(sourceReadPreset('ask'), {scanDepth: 'basic', wholeFileReads: false, connections: 2, requestsPerSecond: 2, bytesPerSecond: 0});
  assert.equal(sourceReadPresetName(sourceReadPreset('everything')), 'everything');
  assert.equal(sourceReadPresetName({...sourceReadPreset('once'), connections: 6}), undefined);
});

test('the Remote sources page draws the add action and its empty state', async () => {
  // The page owns its reader in a layout pass, so a static render of the page only
  // draws its loading status. Drive the page's own pass, then draw the body it commits.
  const React = await import('react');
  const hooks = hookRuntime(React);
  let requests = 0;
  const api = {request: async () => {requests++; return emptySources();}} as never;
  try {
    const tree: any = hooks.mount('page', RemoteSourcesPage, {...pageProps, api});
    // The reader's own mount read is the server's empty source list.
    await new Promise(resolve => setImmediate(resolve));
    const markup = renderToStaticMarkup(tree);
    assert.match(markup, /Add remote source/);
    assert.match(markup, /No remote sources yet/);
    assert.match(markup, /give Portico a folder on another computer/);
    // The page retires that exact reader when it unmounts: a read afterwards starts nothing.
    hooks.unmount('page');
    const before = requests;
    await tree.props.service.load();
    assert.equal(requests, before, 'the page’s retired reader starts no read');
  } finally {hooks.unmountAll();}
});

test('the source states and the blocked sentence have their drawn words', () => {
  assert.equal(defaultI18n.t('next.remoteSources.state.unreachable'), 'Can’t be reached');
  assert.equal(defaultI18n.t('next.remoteSources.state.credentials'), 'Needs its sign-in again');
  assert.equal(defaultI18n.t('next.remoteSources.state.waiting'), 'Waiting');
  assert.equal(defaultI18n.t('next.remoteSources.state.limited'), 'Limited');
  assert.match(defaultI18n.t('next.remoteSources.problem.range_unsupported'), /read parts of a file/);
});

test('a hostile transport message is never shown; only a bounded source error is', () => {
  const hostile = Object.assign(new Error('https://private-user:private-password@private.example/media/'), {code: 'remote_offline'});
  const text = remoteErrorText(hostile);
  assert.equal(text, defaultI18n.t('next.remoteSources.error.generic'));
  assert.doesNotMatch(text, /private-user|private-password|private\.example/);
  assert.equal(remoteErrorText(Object.assign(new Error('x'), {code: 'remote_credentials_required'})), defaultI18n.t('next.remoteSources.problem.credentials_required'));
  assert.equal(remoteErrorText(Object.assign(new Error('x'), {code: 'refresh_failed'})), defaultI18n.t('next.remoteSources.error.refreshFailed'));
});

test('saved Add handlers cannot adopt a reopened or unmounted form lifetime', async () => {
  const React = await import('react');
  const {AddSourceDialog} = await import('../src/screens/settings/remote-sources/RemoteSourcesPage.tsx');
  // The drawn dialog asks the real phone media query; this is its narrowest boundary.
  const previousWindow = (globalThis as any).window;
  (globalThis as any).window = {matchMedia: () => ({matches: false, addEventListener() {}, removeEventListener() {}})};
  // Execute the real dialog hooks and effects, so a lifetime ends only when the
  // component's own cleanup says it does.
  const hooks = hookRuntime(React);
  let io = 0, completions = 0, open = true;
  const service = {testConnection: async () => {io++;return {origin: 'https://a.example', rangeSupport: 'unknown'};},
    configure: async () => {io++;}, getSnapshot: () => ({sources: [], busy: false, ambiguous: false})} as any;
  const api = {request: async () => ({})} as never;
  // One identity per callback: the body's owned resources compare dependencies by identity.
  const isCurrent = () => true, reportError = () => completions++, onClose = () => completions++, onCreated = () => completions++;
  const props = {service, api, serverId: 'server', viewerId: 'owner', isCurrent, reportError, onClose, onCreated};
  // The drawn dialog mounts its loaded body as a second component, so each keeps
  // its own hook slots and its own reader across the passes of its lifetime.
  let reader: any;
  const render = () => {
    const dialog: any = hooks.mount('dialog', AddSourceDialog, {...props, open});
    reader = dialog.props.libraries;
    return hooks.mount('body', dialog.type, {...dialog.props, open});
  };
  const elements = (node: any): any[] => Array.isArray(node) ? node.flatMap(elements) : node && typeof node === 'object' && node.props ? [node, ...elements(node.props.children), ...elements(node.props.footer)] : [];
  const button = (tree: any, label: string) => elements(tree).find(node => node.props.children === defaultI18n.t(label as any));
  // The drawn wizard opens on what to connect; WebDAV is its default family, then
  // the three fields the connection test and the save command both require.
  const fill = (tree: any) => {
    button(tree, 'next.remoteSources.continue').props.onClick(); tree = render();
    for (const [label, value] of [['name', 'A'], ['address', 'https://a.example'], ['password', 'secret-A']] as const) {
      elements(tree).find((node: any) => node.props.label === defaultI18n.t(`next.remoteSources.${label}` as any)).props.onChange({target: {value}});tree = render();
    }
    return tree;
  };
  try {
    let tree = fill(render());
    const oldTest = button(tree, 'next.remoteSources.test').props.onClick;
    oldTest();await new Promise(resolve => setImmediate(resolve));tree = render();
    const oldCreate = button(tree, 'next.remoteSources.saveAndContinue').props.onClick;
    assert.equal(button(tree, 'next.remoteSources.saveAndContinue').props.disabled, false);
    button(tree, 'next.remoteSources.back').props.onClick(); tree = render();
    button(tree, 'next.remoteSources.cancel').props.onClick(); open = false; render(); open = true; tree = render();
    const reopened = {io, completions, state: hooks.snapshot()};
    oldTest();await oldCreate();await new Promise(resolve => setImmediate(resolve));
    assert.equal(io, reopened.io, 'old form sends no provider test or mutation');
    assert.equal(completions, reopened.completions, 'old form cannot close or complete successor');
    assert.equal(hooks.snapshot(), reopened.state, 'successor hook state stays unchanged');
    // A form still drawn, then unmounted rather than closed, retires through its
    // own unmount cleanup: the live handler first proves the capture does act…
    tree = fill(tree);
    const liveTest = button(tree, 'next.remoteSources.test').props.onClick;
    liveTest();await new Promise(resolve => setImmediate(resolve));tree = render();
    assert.equal(io, reopened.io + 1, 'the drawn form does send its own provider test');
    const retainedTest = button(tree, 'next.remoteSources.test').props.onClick;
    const retainedCreate = button(tree, 'next.remoteSources.saveAndContinue').props.onClick;
    hooks.unmount('body');
    const unmounted = {io, completions, state: hooks.snapshot()};
    retainedTest();await retainedCreate();await new Promise(resolve => setImmediate(resolve));
    assert.equal(io, unmounted.io, 'an unmounted form sends no provider test or mutation');
    assert.equal(completions, unmounted.completions, 'an unmounted form cannot close or complete a successor');
    assert.equal(hooks.snapshot(), unmounted.state, 'an unmounted form changes no hook state');
    // …and a new api retires the reader of the old one before its successor exists.
    const retired = reader;
    const replaced: any = hooks.mount('dialog', AddSourceDialog, {...props, api: {request: async () => ({})} as never, open: false});
    assert.notEqual(replaced.props.libraries, retired, 'a new api gets its own reader');
    assert.throws(() => retired.cancel(), /disposed/, 'the previous reader is disposed before its successor exists');
    assert.doesNotThrow(() => replaced.props.libraries.cancel(), 'the successor reader is live');
  } finally {
    try {hooks.unmountAll();} finally {
      if (previousWindow === undefined) delete (globalThis as any).window; else (globalThis as any).window = previousWindow;
    }
  }
});

test('the imported rclone kind has its drawn words and its bound program error', () => {
  assert.equal(defaultI18n.t('next.remoteSources.kindRclone'), 'Imported rclone source');
  assert.match(defaultI18n.t('next.remoteSources.connectRclone'), /Paste a configuration/);
  assert.match(defaultI18n.t('next.remoteSources.configurationHelp'), /never shows it again/);
  assert.match(defaultI18n.t('next.remoteSources.bundledProgram'), /comes with Portico Server/);
  assert.equal(remoteErrorText(Object.assign(new Error('x'), {code: 'administration_unavailable'})), defaultI18n.t('next.remoteSources.problem.administration_unavailable'));
  assert.equal(remoteErrorText(Object.assign(new Error('x'), {code: 'remote_administration_unavailable'})), defaultI18n.t('next.remoteSources.problem.administration_unavailable'));
});

test('the account-configuration kind has its drawn words', () => {
  assert.equal(defaultI18n.t('next.remoteSources.connectFromConfig'), 'From my config');
  assert.match(defaultI18n.t('next.remoteSources.configPath'), /name and kind/);
  assert.match(defaultI18n.t('next.remoteSources.configEncrypted'), /encrypted/);
  assert.match(defaultI18n.t('next.remoteSources.configChecksOnSave'), /tests the remote when you save/);
});

test('the S3 kind has its drawn fields and its own refusal words', () => {
  assert.equal(defaultI18n.t('next.remoteSources.connectS3'), 'S3 storage');
  assert.match(defaultI18n.t('next.remoteSources.s3EndpointHelp'), /https/);
  assert.match(defaultI18n.t('next.remoteSources.s3Checks'), /nothing is typed in rclone/);
  assert.match(defaultI18n.t('next.remoteSources.s3Invalid'), /Check the endpoint/);
});

test('the Google Drive service-account kind has its drawn words', () => {
  assert.equal(defaultI18n.t('next.remoteSources.connectDrive'), 'Google Drive');
  assert.equal(defaultI18n.t('next.remoteSources.driveServiceAccount'), 'Service account key');
  assert.match(defaultI18n.t('next.remoteSources.driveKeyHelp'), /never shows it again/);
  assert.match(defaultI18n.t('next.remoteSources.driveKeyOAuth'), /OAuth client, not a service account key/);
  assert.match(defaultI18n.t('next.remoteSources.driveCannotSee'), /shared with the address above/);
  assert.match(defaultI18n.t('next.remoteSources.driveOAuthUnavailable'), /not available on this server yet/);
});

test('the OneDrive/Dropbox token kind has its drawn words', () => {
  assert.equal(defaultI18n.t('next.remoteSources.connectCloud'), 'OneDrive or Dropbox');
  assert.equal(defaultI18n.t('next.remoteSources.cloudOneDrive'), 'OneDrive');
  assert.equal(defaultI18n.t('next.remoteSources.cloudDropbox'), 'Dropbox');
  assert.match(defaultI18n.t('next.remoteSources.cloudTokenHelp'), /never shows it back/);
  assert.match(defaultI18n.t('next.remoteSources.cloudTokenClient'), /not a signed-in token/);
  assert.match(defaultI18n.t('next.remoteSources.cloudTokenGoogle'), /Google credential file/);
});
