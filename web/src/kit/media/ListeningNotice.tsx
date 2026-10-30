import {useEffect, useMemo, useRef, useState, type RefObject} from 'react';
import {Icon} from '../Icon';
import {listeningNoticeKey} from '@core/audio-effects.ts';
import type {ListeningNoticeCode} from '../../platform/playback/listening-effects';
import type {MediaLayout} from './types';
import styles from './media.module.css';
import {useNoticePresence, type NoticeSlot} from './notice-presence';

export type ListeningNoticeProps = Readonly<{
  notice?: Readonly<{code: ListeningNoticeCode; sequence: number}>;
  /** Playback commands/error clear the canonical notice; this also fences late UI timers. */
  paused: boolean; stopped: boolean; errorShown: boolean; expanded: boolean;
  layout?: MediaLayout;
  translate(key: 'web.player.pressPlay' | 'listening.paused'): string;
}>;

type ListeningPaint = Readonly<{code: ListeningNoticeCode; expanded: boolean; layout: MediaLayout; text: string}>;
/** One visual slot: a replacement changes the content without restarting paint. */
const listeningKey = () => 'listening';

/** The only two actionable quiet notices. Transient engine diagnostics have no input here. */
export function ListeningNotice({notice, paused, stopped, errorShown, expanded, layout = 'desktop', translate}: ListeningNoticeProps) {
  const [expired, setExpired] = useState<number | undefined>(), collapsedAt = useRef<{sequence: number; at: number} | undefined>(undefined);
  const sequence = notice?.sequence;
  useEffect(() => {
    if (sequence === undefined || expanded) { collapsedAt.current = undefined; setExpired(undefined); return; }
    if (collapsedAt.current?.sequence !== sequence) collapsedAt.current = {sequence, at: Date.now()};
    const timer = setTimeout(() => setExpired(sequence), Math.max(0, 8000 - (Date.now() - collapsedAt.current.at)));
    return () => clearTimeout(timer);
  }, [sequence, expanded]);
  const code = notice?.code;
  // Stable across parent renders: a fresh notice object with the same committed
  // code/sequence must not allocate a new slot value on every render.
  const live = useMemo(() => code === undefined || sequence === undefined || !paused || stopped || errorShown || (!expanded && expired === sequence)
    ? undefined
    : Object.freeze({code, expanded, layout, text: translate(listeningNoticeKey(code))}),
  [code, sequence, paused, stopped, errorShown, expanded, expired, layout, translate]);
  // Replacements coalesce in place: one visual notice owns the slot, and a newer
  // code changes its content without restarting the paint or any notice clock.
  const paint = useNoticePresence(live, listeningKey);
  const body = (slot: NoticeSlot<ListeningPaint>, ref: RefObject<HTMLDivElement | null>, outgoing: boolean) => {
    const value = slot.value;
    return <div key={slot.key} ref={ref} className={styles.listeningNotice} data-layout={value.layout} data-expanded={value.expanded} data-paint={outgoing ? 'outgoing' : undefined}
      aria-hidden={outgoing || undefined} inert={outgoing || undefined}
      role={outgoing ? undefined : 'status'} aria-live={outgoing ? undefined : 'polite'} aria-atomic={outgoing ? undefined : 'true'}>
      <Icon name="i-pause" size={value.layout === 'tv' ? 28 : value.layout === 'phone' && value.expanded ? 14 : 18} />
      {value.text}
    </div>;
  };
  if (!paint.current && !paint.outgoing) return null;
  return <>{paint.outgoing && body(paint.outgoing, paint.outgoingRef, true)}{paint.current && body(paint.current, paint.currentRef, false)}</>;
}
