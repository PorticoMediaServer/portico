import {createContext} from 'react';
import {type FreshnessDependency, type FreshnessHub, type FreshnessInterest, type InterestOutcome} from '@core/freshness-hub.ts';
import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';

export function createSettingsOpen() {
  let open=true;const listeners=new Set<()=>void>();
  return {getSnapshot:()=>open,subscribe:(listener:()=>void)=>{listeners.add(listener);return()=>{listeners.delete(listener);};},
    setOpen(value:boolean){if(open===value)return;open=value;for(const listener of listeners)listener();}};
}
export const SettingsOpenContext=createContext<ReturnType<typeof createSettingsOpen>|undefined>(undefined);

export type QuietInput = Readonly<{
  id: string;
  refresh: () => Promise<QuietRefreshOutcome>;
  cancel?: () => void;
  subscribe?: (listener: () => void) => () => void;
  blocked?: () => boolean;
  /** Changes only for local edits, explicit loads/discards and settled writes. */
  settlement?: () => unknown;
}>;
export function quietInterestOutcome(outcome: QuietRefreshOutcome): InterestOutcome {
  if (outcome.kind === 'adopted') return {kind: 'answered'};
  if (outcome.kind === 'unreadable') return {kind: 'failed', error: outcome.error, retryable: true};
  return outcome.kind === 'conflict' ? {kind: 'deferred'} : outcome;
}

/** Each input owns its debt. A blocked/conflicting input resumes on a real owner
 * transition, never a web retry timer or a publication from its own quiet read. */
export function mountQuietInput(hub: FreshnessHub, name: string, dependencies: readonly FreshnessDependency[], input: QuietInput, visibility?: ReturnType<typeof createSettingsOpen>) {
  let closed = false, waiting = false, reading = false, settled = 0;
  let blocked = input.blocked?.() ?? false, receipt = input.settlement?.();
  let interest: FreshnessInterest;
  const changed = () => {
    if (closed) return;
    const nextBlocked = input.blocked?.() ?? false, nextReceipt = input.settlement?.();
    const resumes = blocked && !nextBlocked || !Object.is(nextReceipt, receipt);
    blocked = nextBlocked;receipt = nextReceipt;
    if (resumes) {settled++;if (waiting && !reading && !blocked) {waiting = false;interest.invalidate();}}
  };
  interest = hub.interest({name: `${name}/${input.id}`, dependencies, background: 'never', read: async ({signal}) => {
    if (closed || signal.aborted) return {kind: 'superseded'};
    if (input.blocked?.()) {waiting = true;return {kind: 'deferred'};}
    const before = settled;waiting = false;reading = true;
    let outcome: QuietRefreshOutcome;
    const cancel=()=>input.cancel?.();signal.addEventListener('abort',cancel,{once:true});
    try {outcome = await input.refresh();} catch (error) {outcome = {kind: 'unreadable', error};}
    finally {signal.removeEventListener('abort',cancel);reading = false;}
    if (closed || signal.aborted) return {kind: 'superseded'};
    waiting = outcome.kind === 'deferred' || outcome.kind === 'conflict';
    if (waiting && settled !== before && !input.blocked?.()) {waiting = false;interest.invalidate();}
    return quietInterestOutcome(outcome);
  }});
  const off = input.subscribe?.(changed);
  const updateOpen=()=>interest.setOpen(visibility?.getSnapshot() ?? true);
  const offOpen=visibility?.subscribe(updateOpen);updateOpen();
  return {getState: interest.getState, invalidate: () => interest.invalidate(), setOpen: (open: boolean) => interest.setOpen(open),
    dispose() {if (closed) return;closed = true;interest.setOpen(false);offOpen?.();off?.();interest.dispose();}};
}
