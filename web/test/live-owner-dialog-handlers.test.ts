import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactNode} from 'react';
import {remotePreview, uploadPreview, sourceReceipt} from './live-owner-fixtures.ts';
import {transformTsx} from './transform-tsx.mjs';

// Real dialog functions run on a committed layout lease: opening A publishes its controller,
// and a direct replacement render with opening B retires A before B is admitted. No retained
// footer or close handler from A may close or save after that replacement.
type Effect = {deps?: readonly unknown[]; cleanup?: () => void; phase: 'layout' | 'passive'};
const changed = (a?: readonly unknown[], b?: readonly unknown[]) => !a || !b || a.length !== b.length || a.some((value, index) => !Object.is(value, b[index]));
const bridge = {current: undefined as HookFrame | undefined};
(globalThis as Record<string, unknown>).__ownerDialogHooks = bridge;
const reactModule = import.meta.resolve('react');
// Bind the real React namespace and override only the lifecycle hooks this frame owns.
const hooks = 'data:text/javascript,' + encodeURIComponent(`import * as real from ${JSON.stringify(reactModule)};export * from ${JSON.stringify(reactModule)};
const b = globalThis.__ownerDialogHooks;
${['useRef', 'useState', 'useEffect', 'useLayoutEffect', 'useSyncExternalStore'].map(name => `export const ${name} = (...args) => b.current.${name}(...args);`).join('\n')}`);
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get: (_, key) => String(key)});');
  if (url.endsWith('/kit/controls/index.ts')) return module("export const Button = 'button', ResumeButton = 'resume-button', TextField = 'input', Segmented = 'segmented', Select = 'select', Switch = 'switch', Tabs = 'tabs', Chip = 'chip', RichSelect = 'rich-select', Slider = 'slider';");
  if (url.endsWith('/kit/overlays/index.ts')) return module("export const Dialog = 'dialog';");
  if (url.endsWith('.tsx')) return module(transformTsx(url).replace(/from (['"])react\1/g, `from ${JSON.stringify(hooks)}`));
  return next(url, context);
}});

class HookFrame {
  private slots: any[] = [];
  private cursor = 0;
  private dirty = false;
  private queued: {effect: Effect; run: () => void | (() => void)}[] = [];
  useRef(value: unknown) { const i = this.cursor++; return this.slots[i] ??= {current: value}; }
  useState(initial: unknown) {
    const i = this.cursor++;
    if (!(i in this.slots)) this.slots[i] = typeof initial === 'function' ? (initial as () => unknown)() : initial;
    return [this.slots[i], (next: any) => {
      const value = typeof next === 'function' ? next(this.slots[i]) : next;
      if (!Object.is(value, this.slots[i])) { this.slots[i] = value; this.dirty = true; }
    }];
  }
  useSyncExternalStore(_subscribe: unknown, read: () => unknown) { this.cursor++; return read(); }
  useLayoutEffect(run: () => void | (() => void), deps?: readonly unknown[]) { this.effect('layout', run, deps); }
  useEffect(run: () => void | (() => void), deps?: readonly unknown[]) { this.effect('passive', run, deps); }
  private effect(phase: Effect['phase'], run: () => void | (() => void), deps?: readonly unknown[]) {
    const i = this.cursor++, previous: Effect | undefined = this.slots[i];
    if (!previous || changed(previous.deps, deps)) {
      const effect: Effect = {deps, cleanup: previous?.cleanup, phase};
      this.slots[i] = effect; this.queued.push({effect, run});
    }
  }
  render(component: () => ReactNode): ReactNode {
    for (let attempt = 0; attempt < 8; attempt++) {
      bridge.current = this; this.cursor = 0; this.queued = []; this.dirty = false;
      const result = component();
      for (const phase of ['layout', 'passive'] as const) {
        const pending = this.queued.filter(job => job.effect.phase === phase);
        pending.forEach(job => job.effect.cleanup?.());
        pending.forEach(job => { job.effect.cleanup = job.run() ?? undefined; });
      }
      if (!this.dirty) { bridge.current = undefined; return result; }
    }
    bridge.current = undefined;
    throw new Error('dialog effects did not settle within eight renders');
  }
  unmount() { for (const slot of this.slots) { slot?.cleanup?.(); if (slot && 'cleanup' in slot) slot.cleanup = undefined; } }
}

const {RemoteSourceDialog} = await import('../src/screens/live/owner/RemoteSourceDialog.tsx');
const {UploadSourceDialog} = await import('../src/screens/live/owner/UploadSourceDialog.tsx');
test.after(() => {loader.deregister();delete (globalThis as Record<string, unknown>).__ownerDialogHooks;});

function elements(tree: ReactNode): any[] {const result: any[] = [];Children.forEach(tree, child => {if (isValidElement<any>(child)) {result.push(child);result.push(...elements(child.props.children));}});return result;}
const inputs = (tree: any) => elements(tree).filter(element => element.type === 'input');
const buttons = (tree: any) => elements(tree.props.footer).filter(element => element.type === 'button');
const action = (tree: any) => buttons(tree).at(-1);
const cancel = (tree: any) => buttons(tree)[0];
const settle = async () => {for (let i = 0; i < 12; i++) await Promise.resolve();};
type Request = {path: string; method?: string; body?: unknown; signal?: AbortSignal};
function apiFor(preview: unknown, save: Promise<unknown>) {
  const requests: Request[] = [];
  return {requests, request(path: string, method?: string, body?: unknown, signal?: AbortSignal) {
    requests.push({path, method, body, signal});
    return path.endsWith('/preview') ? Promise.resolve(preview) : save;
  }};
}
function deferred<T>() {let resolve!: (value: T) => void;const promise = new Promise<T>(done => {resolve = done;});return {promise, resolve};}
function dialogProps(api: unknown, openingId: string, opening: {live: boolean}, counters: {saved: number; closed: number}) {
  return {api, serverId: 'server', isCurrent: () => true, reportError: () => {}, openingId, opening,
    close: () => {counters.closed++;}, saved: () => {counters.saved++;}};
}
const paint = (frame: HookFrame, component: any, props: unknown) => frame.render(() => component(props)) as any;

for (const [name, component, preview, savePath] of [
  ['remote', RemoteSourceDialog, remotePreview, '/v1/admin/live-sources/remote/save'],
  ['upload', UploadSourceDialog, uploadPreview, '/v1/admin/live-sources'],
] as const) {
  test(`${name} stale saved/close handlers cannot affect replacement dialog`, async t => {
    const frame = new HookFrame(), counters = {saved: 0, closed: 0}, pending = deferred<unknown>();
    t.after(() => frame.unmount());
    const apiA = apiFor(preview, pending.promise), apiB = apiFor(preview, Promise.resolve(sourceReceipt()));
    const openingA = {live: true}, openingB = {live: true};
    const propsA = dialogProps(apiA, `${name}-a`, openingA, counters), propsB = dialogProps(apiB, `${name}-b`, openingB, counters);
    let tree = paint(frame, component, propsA);
    inputs(tree)[0].props.onChange({currentTarget: {value: 'Stored'}});
    if (name === 'remote') inputs(tree)[1].props.onChange({currentTarget: {value: 'https://provider.example'}});
    else {const file = inputs(tree).find(element => element.props.type === 'file');
      file.props.onChange({currentTarget: {files: [{text: async () => '#EXTM3U'}]}});await settle();}
    tree = paint(frame, component, propsA);
    action(tree).props.onClick();
    await settle();
    tree = paint(frame, component, propsA);
    const stale = tree;
    action(stale).props.onClick();
    assert.equal(apiA.requests.at(-1)?.path, savePath, 'the pending save belongs to the opening that drew it');
    const replacement = paint(frame, component, propsB);
    stale.props.onOpenChange(false);
    cancel(stale).props.onClick();
    pending.resolve(sourceReceipt());
    await settle();
    assert.equal(counters.saved, 0, 'a retired opening cannot save after replacement');
    assert.equal(counters.closed, 0, 'a retired opening cannot close after replacement');
    assert.equal(apiB.requests.length, 0, 'no retained handler reached the replacement opening');
    cancel(replacement).props.onClick();
    assert.equal(counters.closed, 1, 'the replacement opening still owns its own close');
    assert.equal(counters.saved, 0);
    frame.unmount();
  });
}
