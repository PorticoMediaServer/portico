import {useEffect, useMemo, useRef, useState, type RefObject} from 'react';
import {Icon} from '../Icon';
import type {MediaLayout} from './types';
import styles from './media.module.css';
import {useNoticePresence, type NoticeSlot} from './notice-presence';

export type PlaybackLossNotice = Readonly<{
  /** Stable playback-run identity, not generation: track/quality changes share this fence. */
  playbackId: string;
  cause: 'initial' | 'automatic' | 'user-choice';
  kind: 'picture' | 'sound';
  /** Canonical localized loss/replacement sentences, built from structured losses only. */
  loss: string; replacement: string;
}>;

type LossPaint = Readonly<{notice: PlaybackLossNotice; layout: MediaLayout}>;
const lossKey = (value: LossPaint) => value.notice.playbackId;

/** Once per playback, six seconds, never focusable. A player choice cannot trigger a notice. */
export function LossNotice({notice, enabled, layout = 'desktop'}: {notice?: PlaybackLossNotice; enabled: boolean; layout?: MediaLayout}) {
  const shown = useRef(new Set<string>()), [visible, setVisible] = useState<PlaybackLossNotice | undefined>();
  useEffect(() => {
    if (!notice || !enabled || notice.cause === 'user-choice' || shown.current.has(notice.playbackId) || !notice.loss) return;
    shown.current.add(notice.playbackId); setVisible(notice);
  }, [notice, enabled]);
  useEffect(() => { if (!visible) return; const timer = setTimeout(() => setVisible(undefined), 6000); return () => clearTimeout(timer); }, [visible]);
  const live = useMemo(() => enabled && visible && notice?.playbackId === visible.playbackId
    ? Object.freeze({notice: visible, layout}) : undefined,
  [enabled, visible, layout, notice?.playbackId]);
  const paint = useNoticePresence(live, lossKey, 'immediate');
  const body = (slot: NoticeSlot<LossPaint>, ref: RefObject<HTMLDivElement | null>, outgoing: boolean) => {
    const value = slot.value.notice, drawn = slot.value.layout;
    return <div key={slot.key} ref={ref} className={styles.lossNotice} data-layout={drawn} data-paint={outgoing ? 'outgoing' : undefined}
      aria-hidden={outgoing || undefined} inert={outgoing || undefined}
      role={outgoing ? undefined : 'status'} aria-live={outgoing ? undefined : 'polite'} aria-atomic={outgoing ? undefined : 'true'}>
      <span className={styles.lossIcon}><Icon name={value.kind === 'picture' ? 'i-image' : 'i-vol'} size={drawn === 'tv' ? 28 : drawn === 'phone' ? 16 : 20} /></span>
      <span><b>{value.loss}</b>{value.replacement && <> {value.replacement}</>}</span>{!outgoing && <i className={styles.lossCountdown} />}
    </div>;
  };
  if (!paint.current && !paint.outgoing) return null;
  return <>{paint.outgoing && body(paint.outgoing, paint.outgoingRef, true)}{paint.current && body(paint.current, paint.currentRef, false)}</>;
}
