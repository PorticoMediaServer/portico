import type {SelectionActionSource} from '@core/selection-actions-controller.ts';
import type {SelectionOperation} from '@core/selection-model/types.ts';

export type SelectionActionHost<C> = Readonly<{
  subscribe: (listener: () => void) => () => void;
  captureOperation?: () => SelectionOperation<C> | null;
  isOperationCurrent?: (operation: SelectionOperation<C>) => boolean;
}>;

/** Content publications may replace the selection host while an accepted command remains visible. */
export function createSelectionActionHostBridge<C>() {
  let host: SelectionActionHost<C> | undefined, off: (() => void) | undefined;
  const listeners = new Set<() => void>();
  const emit = () => {for (const listener of [...listeners]) listener();};
  return {
    subscribe(listener: () => void) {listeners.add(listener); return () => {listeners.delete(listener);};},
    captureOperation: () => host?.captureOperation?.() ?? null,
    isOperationCurrent: (operation: SelectionOperation<C>) => host?.isOperationCurrent?.(operation) === true,
    attach(next?: SelectionActionHost<C>) {off?.(); host = next; off = next?.subscribe(emit); emit();},
    dispose() {off?.(); off = undefined; host = undefined; emit(); listeners.clear();},
  };
}

/** Submission freezes the command, not the catalogue projection. Route/query/authority stay binding. */
export function createSelectionActionLifetime(options: Readonly<{
  expectedTotal: number; signal: AbortSignal; scopeKey: () => string; isCurrent: () => boolean;
  selectionCurrent: () => boolean; attempted: () => boolean; subscribe: (listener: () => void) => () => void;
}>) {
  const scope = options.scopeKey(); let sent = false, retired = false;
  const attempted = () => {sent ||= options.attempted(); return sent;};
  const source: SelectionActionSource = Object.freeze({expectedTotal: options.expectedTotal, signal: options.signal,
    isCurrent: () => !retired && !options.signal.aborted && options.isCurrent() && options.scopeKey() === scope
      && (attempted() || options.selectionCurrent()),
    subscribe: options.subscribe,
  });
  return {source, attempted, retire: () => {retired = true;}};
}
