import {useLayoutEffect, useMemo, useState, type RefObject} from 'react';
import type {I18n} from '@i18n';
import type {WebPlaybackRuntimeSnapshot, WebShellPlaybackRuntime} from '../../platform/playback';
import type {QueuePlaybackFence} from '../../../../packages/player-core/src/index.ts';
import {sameFence} from '../../../../packages/player-core/src/index.ts';
import {PlaybackInfo, type PlaybackInfoProjection} from '../../kit/media';
import {fenceKey} from '../../shell/playback-model';
import {playbackInfoCopy, watchPlaybackInfo} from './info-copy';

type Props = Readonly<{
  runtime: WebShellPlaybackRuntime; snapshot: WebPlaybackRuntimeSnapshot; fence: QueuePlaybackFence;
  title: string; deviceName?: string; i18n: I18n; layout: 'desktop' | 'phone'; phoneTop: number;
  returnFocusRef: RefObject<HTMLButtonElement | null>; close: () => void; reportError: (error: unknown) => void;
}>;

/** The stable facts a panel belongs to. Observation ticks refresh the objects
 * but not these scalars, so they keep one lease and one read. */
const sessionFacts = (session: WebPlaybackRuntimeSnapshot['session']) => session ? `${session.id}|${session.revision}|${session.presentation.generation}` : '';
type InfoTuple = Readonly<{runtime: WebShellPlaybackRuntime; fence: string; session: string; title: string; deviceName?: string; i18n: I18n}>;
/** One opening. `live` is the only mutable field: the accepted close and the
 * cleanup consume it in place before clearing, so nothing speaks afterwards. */
type InfoLease = Readonly<{id: number; tuple: InfoTuple; original: QueuePlaybackFence; projection?: PlaybackInfoProjection; reportError: (error: unknown) => void}> & {live: boolean};
let nextLeaseId = 1;

const sameTuple = (lease: InfoLease, tuple: InfoTuple) => lease.tuple.runtime === tuple.runtime && lease.tuple.fence === tuple.fence
  && lease.tuple.session === tuple.session && lease.tuple.title === tuple.title && lease.tuple.deviceName === tuple.deviceName && lease.tuple.i18n === tuple.i18n;

/** The drawn Playback info panel. It opens from More (frame 1), reads the current
 * committed comparison afresh once, and updates live when the route or track changes.
 * The media view stays mounted and playing underneath; no control is drawn over it. */
export function WatchInfoPanel(props: Props) {
  const [lease, setLease] = useState<InfoLease | null>(null);
  const committed = props.snapshot.session;
  const tuple: InfoTuple = {runtime: props.runtime, fence: fenceKey(props.fence), session: sessionFacts(committed), title: props.title, deviceName: props.deviceName, i18n: props.i18n};
  useLayoutEffect(() => {
    const projection = committed?.playbackInfo
      ? watchPlaybackInfo(committed.playbackInfo, committed.observedPlayback, props.i18n, props.title, props.deviceName) : undefined;
    const entry: InfoLease = {id: nextLeaseId++, tuple, original: props.fence, reportError: props.reportError, ...(projection ? {projection} : {}), live: true};
    setLease(entry);
    return () => {
      entry.live = false;
      setLease(current => current === entry ? null : current);
    };
    // The scalar tuple above is the identity; the whole objects are not.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.runtime, tuple.fence, tuple.session, props.title, props.deviceName, props.i18n]);
  // Withheld unless the lease is live and is this exact tuple.
  if (!lease || !lease.live || !sameTuple(lease, tuple)) return null;
  return <WatchInfoPanelBody key={lease.id} lease={lease} {...props}/>;
}

function WatchInfoPanelBody({lease, runtime, i18n, layout, phoneTop, returnFocusRef, close}: Props & Readonly<{lease: InfoLease}>) {
  const copy = useMemo(() => playbackInfoCopy(i18n), [i18n]);
  const [projection, setProjection] = useState<PlaybackInfoProjection | undefined>(lease.projection);
  /** Real only while this lease is live and the runtime still reports the same
   * fence and committed session, and is not disposed. */
  const owns = () => {
    if (!lease.live) return false;
    const now = runtime.getSnapshot();
    return !now.disposed && sameFence(now.fence, lease.original) && sessionFacts(now.session) === lease.tuple.session;
  };
  useLayoutEffect(() => {
    if (lease.original.kind !== 'queue') return;
    const controller = new AbortController();
    let live = true;
    void runtime.readPlaybackInfo(lease.original, controller.signal).then(read => {
      if (!live || !owns() || !read || !read.isCurrent() || !read.playbackInfo) return;
      const next = watchPlaybackInfo(read.playbackInfo, read.observedPlayback, i18n, lease.tuple.title, lease.tuple.deviceName);
      if (live && owns() && next) setProjection(next);
    }).catch(error => {
      if (!live || !owns() || (error instanceof DOMException && error.name === 'AbortError')) return;
      lease.reportError(error);
    });
    return () => { live = false; controller.abort(); };
    // One read per lease: the report callback and presentation inputs are the lease's.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lease, runtime]);
  if (!projection) return null;
  return <PlaybackInfo projection={projection} copy={copy} layout={layout} open
    onOpenChange={open => {
      if (open) return;
      if (!owns()) return;
      // The accepted close spends this exact lease before speaking.
      lease.live = false;
      close();
    }} returnFocusRef={returnFocusRef} phoneTop={phoneTop} />;
}
