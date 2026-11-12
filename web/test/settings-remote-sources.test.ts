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
const {RemoteSourcesPage, remoteErrorText} = await import('../src/screens/settings/remote-sources/RemoteSourcesPage.tsx');
// The server's empty list of sources.
const emptySources = async () => ({items: []});
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

const {addCloudTokenSource, addRcloneRemoteFromFields} = await import('../../packages/client-core/src/remote-storage.ts');

test('the Remote sources page draws the add action and its empty state', async () => {
  const React = await import('react');
  const hooks = hookRuntime(React);
  try {
    const props = {...pageProps, api: {request: emptySources} as never};
    hooks.mount('page', RemoteSourcesPage, props);
    await new Promise(resolve => setImmediate(resolve));
    const markup = renderToStaticMarkup(hooks.mount('page', RemoteSourcesPage, props));
    assert.match(markup, /Add remote source/);
    assert.match(markup, /No remote sources yet/);
  } finally {hooks.unmountAll();}
});

test('a source left before a folder of it is in a library says Setup not finished and offers Continue setup (rule 10)', async () => {
  const React = await import('react');
  const hooks = hookRuntime(React);
  const source = {id: 'rs_1', kind: 'webdav', name: 'NAS', preset: 'read-once', limits: {connections: 4, perSecond: 4}, webdav: {address: 'https://nas.local/dav', passwordSet: true}, state: 'setup', folders: 0};
  try {
    const props = {...pageProps, api: {request: async () => ({items: [source]})} as never};
    hooks.mount('page', RemoteSourcesPage, props);
    await new Promise(resolve => setImmediate(resolve));
    const markup = renderToStaticMarkup(hooks.mount('page', RemoteSourcesPage, props));
    assert.match(markup, /Setup not finished/);
    assert.match(markup, /Continue setup/);
    assert.match(markup, /https:\/\/nas\.local\/dav/);
  } finally {hooks.unmountAll();}
});

test('OneDrive or Dropbox by token and a new rclone remote say plainly that this server does not offer them yet, and send nothing', async () => {
  let sent = 0;
  const api = {request: async () => {sent++; return {};}};
  for (const attempt of [addCloudTokenSource(api, {name: 'Drive', provider: 'onedrive', token: '{}', path: '', preset: 'read-once'}),
    addRcloneRemoteFromFields(api, {name: 'Bucket', type: 's3', options: {}, path: '', preset: 'read-once'})]) {
    const error = await attempt.then(() => undefined, failure => failure);
    assert.equal(remoteErrorText(error), defaultI18n.t('next.remoteSources.notOnServerYet'));
  }
  assert.equal(sent, 0);
});

test('a refusal shows what the server wrote for this area, or names the field, and anything else is a plain line', () => {
  assert.equal(remoteErrorText(Object.assign(new Error('Portico could not reach that address: connection refused'), {code: 'source_unreachable'})), 'Portico could not reach that address: connection refused');
  assert.equal(remoteErrorText(Object.assign(new Error('Check the request and try again.'), {code: 'invalid_request', path: 'rclone.config'})), defaultI18n.t('next.remoteSources.field.config'));
  assert.equal(remoteErrorText(Object.assign(new Error('<script>'), {code: 'internal'})), defaultI18n.t('next.remoteSources.error.generic'));
});
