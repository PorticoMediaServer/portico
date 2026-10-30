import {useEffect, useSyncExternalStore} from 'react';
import {selectionActionTargetInput, type SelectionAction, type SelectionActionOptions} from '@core/selection-actions.ts';
import type {QuerySelectionTarget, SelectionOperation} from '@core/selection-model/types.ts';
import {BrowserSelectionActionModel, type BrowserSelectionActionOptions} from './selection-action-model.ts';
import {SelectionActionSurface} from './SelectionActionSurface.tsx';
import {createSelectionActionLifetime, type SelectionActionHost} from './selection-action-lifetime.ts';

export type BrowserQueryActionsOptions = BrowserSelectionActionOptions & Readonly<{
  deviceId: string; owner: boolean; isCurrent: () => boolean; signal: AbortSignal;
  /** Stable semantic route/query plus principal/authority key; excludes content/page revisions. */
  scopeKey: () => string;
  /** Principal/authority/route fact source, in addition to the selection model below. */
  subscribe: (listener: () => void) => () => void;
  /** Home supplies its actual immutable document; equal revision strings do not extend its lifetime. */
  sourceIdentity?: () => unknown;
}>;
const actionOptions = (action: SelectionAction, options: BrowserQueryActionsOptions): SelectionActionOptions => action === 'delete'
  ? {action, options: {deleteFiles: true}} : {action, options: {deviceId: options.deviceId, quality: 'original'}};

export function browserQueryActionOffered<C>(target: QuerySelectionTarget<C>, action: SelectionAction, options?: BrowserQueryActionsOptions): boolean {
  if (!options || options.signal.aborted || !options.isCurrent() || action === 'delete' && !options.owner) return false;
  try {selectionActionTargetInput(target, actionOptions(action, options)); return true;} catch {return false;}
}

/** Bind both actual query actions to the model-issued operation and the original live authority. */
export function createBrowserQueryActions<C>(host: SelectionActionHost<C>, options: BrowserQueryActionsOptions) {
  const model = new BrowserSelectionActionModel(options);
  let active: Readonly<{operation: SelectionOperation<C>; action: SelectionActionOptions; lifetime: ReturnType<typeof createSelectionActionLifetime>}> | undefined;
  const close = () => {active?.lifetime.retire(); active = undefined; model.close();};
  const open = (action: SelectionAction) => {
    if (active?.lifetime.attempted() && !['idle', 'retired'].includes(model.controller.getSnapshot().phase)) throw new Error('query-action-in-progress');
    const operation = host.captureOperation?.();
    if (!operation || operation.target.mode !== 'query' || !host.isOperationCurrent?.(operation) || !browserQueryActionOffered(operation.target, action, options)) throw new Error('query-action-unavailable');
    const originalSource = options.sourceIdentity?.();
    const lifetime = createSelectionActionLifetime({expectedTotal: operation.target.total, signal: options.signal, scopeKey: options.scopeKey,
      isCurrent: () => active?.lifetime === lifetime && options.isCurrent(),
      selectionCurrent: () => host.isOperationCurrent?.(operation) === true && (!options.sourceIdentity || options.sourceIdentity() === originalSource),
      attempted: () => {const state = model.controller.getSnapshot(); return !!state.job || ['submitting', 'uncertain', 'running', 'cancelling', 'result'].includes(state.phase);},
      subscribe: changed => {const a = host.subscribe(changed), b = options.subscribe(changed); return () => {a();b();};}});
    active?.lifetime.retire();
    active = Object.freeze({operation, action: actionOptions(action, options), lifetime});
    void model.open(operation.target, active.action, lifetime.source);
  };
  const changeOptions = (value: Readonly<{deleteFiles: boolean}> | Readonly<{quality: string}>) => {
    const original = active;
    if (!original || original.lifetime.attempted() || original.operation.target.mode !== 'query' || !original.lifetime.source.isCurrent()) return;
    const action: SelectionActionOptions = original.action.action === 'delete' && 'deleteFiles' in value
      ? {action: 'delete', options: value} : original.action.action === 'download' && 'quality' in value
        ? {action: 'download', options: {...original.action.options, quality: value.quality}} : original.action;
    active = Object.freeze({...original, action});
    void model.open(original.operation.target, action, original.lifetime.source);
  };
  function Surface() {
    useSyncExternalStore(model.controller.subscribe, model.controller.getSnapshot, model.controller.getSnapshot);
    useEffect(() => close, []);
    return <SelectionActionSurface model={model} onClose={close} onOptionsChange={changeOptions}/>;
  }
  return {model, offered: (target: QuerySelectionTarget<C>, action: SelectionAction) => browserQueryActionOffered(target, action, options),
    openDelete: () => open('delete'), openDownload: () => open('download'), surface: <Surface/>, dispose: () => {active?.lifetime.retire(); active = undefined; model.dispose();}};
}
export type BrowserQueryActionHost<C> = ReturnType<typeof createBrowserQueryActions<C>>;
