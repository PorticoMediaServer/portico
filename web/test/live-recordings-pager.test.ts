import test from 'node:test';
import assert from 'node:assert/strict';
import {registerHooks} from 'node:module';
import {Children, isValidElement, type ReactNode} from 'react';
import {transformTsx} from './transform-tsx.mjs';
const react = 'data:text/javascript,' + encodeURIComponent(`export * from '${import.meta.resolve('react')}';export const useState=v=>[typeof v==='function'?v():v,()=>{}];`);
const loader = registerHooks({load(url, context, next) {
  const module = (source: string) => ({format: 'module' as const, source, shortCircuit: true});
  if (url.endsWith('.module.css')) return module('export default new Proxy({}, {get:(_,k)=>String(k)});');
  if (url.endsWith('/kit/controls/index.ts')) return module("export const Button='button',Chip='chip',Select='select';");
  if (url.endsWith('/kit/overlays/index.ts')) return module("export const Banner='banner',Dialog='dialog',Menu='menu',SidePanel='panel';");
  if (url.endsWith('/kit/artwork/index.ts')) return module("export const ChannelMark='channel';");
  if (url.endsWith('/kit/states/index.ts')) return module("export const StateView='state';");
  if (url.endsWith('/src/router.tsx')) return module('export const router={};');
  if (url.endsWith('/screens/browse/screen-context.ts')) return module('export const useCommittedLibraryScreenContext=()=>undefined;');
  if (url.endsWith('/screens/live/useRecordedGroupLease.ts')) return module('export const useRecordedGroupLease=()=>undefined;');
  if (url.endsWith('/screens/live/useRecordingsLease.ts')) return module('export const useRecordingsLease=()=>undefined;');
  if (url.endsWith('/screens/live/useCommittedOpening.ts')) return module('export const useCommittedOpening=()=>({});');
  if (url.endsWith('/screens/live/SeriesRules.tsx')) return module("export const LiveRecordingTabs='tabs';");
  if (url.endsWith('.tsx')) return module(transformTsx(url).replace(/from (['"])react\1/g, `from ${JSON.stringify(react)}`));
  return next(url, context);
}, resolve(specifier, context, next) {
  if (specifier === '@portico/client-core/kit-model') return {url: 'data:text/javascript,export const channelTint=()=>undefined;', shortCircuit: true};
  return next(specifier, context);
}});
const {RecordingsPager, UpcomingView, RecordedView, HistoryView, conflictOperationCurrent} = await import('../src/screens/live/RecordingsScreen.tsx');
function elements(tree: ReactNode): any[] {const result: any[] = [];Children.forEach(tree, child => {if (isValidElement<any>(child)) {result.push(child);result.push(...elements(child.props.children));}});return result;}
test('all three actual list presenters mount the shared offered-cursor pager', () => {
  const props: any = {context: {}, controller: {}, snapshot: {page: {recordings: []}, groups: {groups: []}}, now: 0, current: () => true, onResolve() {}};
  for (const view of [UpcomingView, RecordedView, HistoryView]) assert.equal(elements(view(props)).filter(e => e.type === RecordingsPager).length, 1);
});
test('actual paging controls retain the original snapshot and retry the original failed direction', () => {
  const calls: unknown[] = [], snapshot: any = {paging: {busy: false, error: 'refused', previous: true, next: true}};
  const controller: any = Object.fromEntries(['more', 'previous', 'retryPage'].map(name => [name, (held: unknown) => {calls.push([name, held]);return Promise.resolve();}]));
  const tree = elements(RecordingsPager({controller, snapshot}));
  tree.find(e => e.type === 'banner').props.action.onAction();
  const buttons = tree.filter(e => e.type === 'button');buttons[0].props.onClick();buttons[1].props.onClick();
  assert.deepEqual(calls, [['retryPage', snapshot], ['previous', snapshot], ['more', snapshot]]);
  const pending = elements(RecordingsPager({controller, snapshot: {...snapshot, paging: {...snapshot.paging, busy: true}}}));
  assert.equal(pending.find(e => e.type === 'button').props.disabled, true);
  assert.equal(pending.filter(e => e.type === 'button')[1].props.loading, true);
});
test('an externally current predicate cannot revive a retired or replaced conflict operation', () => {
  for (const retire of ['operation', 'opening', 'replace']) {
    const opening: any = {live: true}, held: any = {live: true, opening}, ref = {current: held};
    assert.equal(conflictOperationCurrent(held, ref, opening, () => {
      if (retire === 'operation') held.live = false;
      if (retire === 'opening') opening.live = false;
      if (retire === 'replace') ref.current = {...held};
      return true;
    }, true), false);
  }
});
test('the accepted command still owns its operation after consuming its own opening', () => {
  const opening: any = {live: false}, held: any = {live: true, opening}, ref = {current: held};
  assert.equal(conflictOperationCurrent(held, ref, opening, () => true), true);
  assert.equal(conflictOperationCurrent(held, ref, opening, () => true, true), false);
});
test.after(() => loader.deregister());
