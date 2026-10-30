import {useMemo, useRef, useState, useSyncExternalStore, type RefObject} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../../../kit/controls';
import {DeviceEqualizer, type EqualizerPort} from '../../../kit/media';
import {fenceKey} from '../../../shell/playback-model';
import type {WebShellPlaybackRuntime} from '../../../platform/playback';

const t = defaultI18n.t;

/** Immutable facts of one rendered host opening. They never carry a live/authority field. */
export type WatchHostFacts = {
  runtime: WebShellPlaybackRuntime; port: EqualizerPort; authority: object;
  fenceAt: string; presentationAt: string; run: number;
  isCurrent?: () => boolean; scope?: Readonly<{current(): boolean}>;
};
/** One layout receipt for a host or surface opening. A replay allocates a fresh object. */
export type WatchReceipt<F = WatchHostFacts> = {facts: F; live: boolean};
export type WatchSurfaceReceipt = {host: WatchReceipt; live: boolean};

export const hostFactsCurrent = (held: WatchHostFacts): boolean => {
  const value = held.runtime.getSnapshot();
  return !value.disposed && value.authority === held.authority && !!value.fence && fenceKey(value.fence) === held.fenceAt
    && value.playback.playbackRun === held.run && value.presentation?.kind === 'music'
    && !!value.presentation.fence && fenceKey(value.presentation.fence) === held.presentationAt
    && held.runtime.equalizerPort === held.port;
};
const hostPredicates = (held: WatchHostFacts) => (!held.isCurrent || held.isCurrent()) && (!held.scope || held.scope.current());
/** Host receipt: published identity, predicates and facts, rechecked after each external read. */
export const hostAdmit = (held: WatchReceipt | null, published: RefObject<WatchReceipt | null>, rendered: WatchHostFacts | null): held is WatchReceipt => {
  const local = () => !!held && held.live && published.current === held && held.facts === rendered;
  if (!held || !local()) return false;
  if (!hostPredicates(held.facts)) return false;
  if (!local()) return false;
  if (!hostFactsCurrent(held.facts)) return false;
  if (!local() || !hostPredicates(held.facts)) return false;
  if (!hostFactsCurrent(held.facts)) return false;
  return local();
};
/** A More/EQ surface receipt proves its own opening AND the host receipt it was drawn under. */
export const surfaceAdmit = (held: WatchSurfaceReceipt | null, published: RefObject<WatchSurfaceReceipt | null>,
  publishedHost: RefObject<WatchReceipt | null>, host: WatchReceipt | null, renderFacts: WatchHostFacts | null): held is WatchSurfaceReceipt => {
  // The surface captures the exact host receipt object, and that receipt must still be THIS
  // render's facts — a published pair matching itself is not enough after a facts change.
  const local = () => !!held && held.live && published.current === held && !!host && held.host === host
    && !!renderFacts && host.facts === renderFacts && publishedHost.current === host && host.live;
  if (!held || !local()) return false;
  if (!hostPredicates(host!.facts)) return false;
  if (!local()) return false;
  if (!hostFactsCurrent(host!.facts)) return false;
  if (!local() || !hostPredicates(host!.facts)) return false;
  if (!hostFactsCurrent(host!.facts)) return false;
  return local();
};

/** The drawn More › Equalizer panel (music-plus frame 12). The parent owns the host and EQ
 * receipts; this view shows them only while they are the published pair and every action closes
 * over that EQ receipt. */
export function WatchEqualizerPanel({receipt, rendered, published, host, hostReceipt, publishedHost, runtime, port, isCurrent, scope, close, openSettings}: Readonly<{
  receipt: WatchSurfaceReceipt | null; rendered: WatchSurfaceReceipt | null;
  published: RefObject<WatchSurfaceReceipt | null>;
  host: WatchHostFacts | null; hostReceipt: WatchReceipt | null; publishedHost: RefObject<WatchReceipt | null>;
  runtime: WebShellPlaybackRuntime; port: EqualizerPort;
  isCurrent?: () => boolean; scope?: Readonly<{current(): boolean}>;
  close: () => void; openSettings: () => void;
}>) {
  const [error, setError] = useState<unknown>();
  const matches = (value: WatchHostFacts | null) => !!value && value.runtime === runtime && value.port === port
    && value.isCurrent === isCurrent && value.scope === scope;
  const shown = matches(host) && matches(receipt?.host?.facts ?? null) && receipt === rendered && published.current === receipt
    && hostReceipt === receipt?.host && publishedHost.current === hostReceipt && hostReceipt.facts === host ? receipt : null;
  const guarded = useMemo<EqualizerPort>(() => ({
    getSnapshot: () => port.getSnapshot(), subscribe: listener => port.subscribe(listener), start: () => port.start(),
    change: async value => {
      if (!surfaceAdmit(receipt, published, publishedHost, hostReceipt, host)) throw new DOMException('Equalizer opening retired', 'AbortError');
      return port.change(value);
    },
    reset: async () => {
      if (!surfaceAdmit(receipt, published, publishedHost, hostReceipt, host)) throw new DOMException('Equalizer opening retired', 'AbortError');
      return port.reset();
    },
  }), [receipt, published, publishedHost, hostReceipt, host, port]);
  const value = useSyncExternalStore(guarded.subscribe, guarded.getSnapshot, () => null);
  const back = () => {if (surfaceAdmit(receipt, published, publishedHost, hostReceipt, host)) close();};
  const settings = () => {if (surfaceAdmit(receipt, published, publishedHost, hostReceipt, host)) openSettings();};
  const report = (failure: unknown) => {if (surfaceAdmit(receipt, published, publishedHost, hostReceipt, host)) setError(failure);};
  return <div data-watch-equalizer data-layout="panel">
    {shown && hostReceipt && value ? <DeviceEqualizer port={guarded} layout="panel" reportError={report} onBack={back} onOpenSettings={settings} />
      : shown ? <div><p role="status">{t('next.states.loading')}</p><Button size="small" onClick={back}>{t('next.media.eq.backQueue')}</Button></div> : null}
    {error !== undefined && shown && <p role="alert">{t('next.states.pageFailedBody')}</p>}
  </div>;
}
