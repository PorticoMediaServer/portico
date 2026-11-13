import type {InputHTMLAttributes, ReactNode, Ref} from 'react';
import styles from './seek-line.module.css';

/** A place on the line, as fractions of its length. The reader draws a chapter as a tick at its
 * start (and the current chapter as a band); the player draws a segment (an intro, the credits),
 * `fromEnd` keeping one that runs to the end anchored there. */
export type SeekLineMark = Readonly<{id: string; start: number; end?: number; kind?: string; current?: boolean; fromEnd?: boolean}>;
export type SeekLineProps = Readonly<{
  /** The two drawn lines: the video player's rail (desktop-player) and the reader's progress (desktop-read 6). */
  variant: 'player' | 'reader';
  /** Where it is, 0 to 1. */
  value: number;
  marks?: readonly SeekLineMark[];
  /** The player's buffered stretches, 0 to 1. */
  buffered?: readonly Readonly<{start: number; end: number}>[];
  /** The pointer's place over the line, 0 to 1, or null. */
  hover?: number | null;
  /** What is at the pointer (the player's time there), drawn above it when no richer overlay carries it. */
  hoverLabel?: string;
  /** Drawn over the line (the player's scrub preview). */
  overlay?: ReactNode;
  /** The line's range input: its value, labels and handlers stay the screen's own. */
  input?: InputHTMLAttributes<HTMLInputElement>;
  inputRef?: Ref<HTMLInputElement>;
  dir?: 'ltr' | 'rtl';
  /** The screen's own class for the line and for its bar, for the screen's own layouts of it. */
  className?: string; barClassName?: string;
}>;

const pct = (value: number) => `${Math.min(1, Math.max(0, value)) * 100}%`;
const cx = (...names: readonly (string | false | undefined)[]) => names.filter(Boolean).join(' ');

/** One seek line for the player and the reader: the bar, its fill, its marks, the buffered and hovered
 * stretches, and the range input that moves it. Each keeps its own look through the variant. */
export function SeekLine({variant, value, marks = [], buffered = [], hover = null, hoverLabel, overlay, input, inputRef, dir, className, barClassName}: SeekLineProps) {
  const player = variant === 'player';
  const range = input ? <input ref={inputRef} type="range" {...input} /> : null;
  return <div className={cx(player ? styles.player : styles.reader, className)} dir={dir}>
    <span className={cx(player ? styles.playerBar : styles.readerBar, barClassName)}>
      {buffered.map((stretch, index) => <span key={index} className={styles.buffered} aria-hidden="true" style={{insetInlineStart: pct(stretch.start), width: pct(stretch.end - stretch.start)}} />)}
      <i className={player ? styles.playerFill : styles.readerFill} style={{width: pct(value)}} />
      {hover !== null && <><span className={styles.hoverFill} aria-hidden="true" style={{width: pct(hover)}} /><span className={styles.hoverLine} aria-hidden="true" style={{insetInlineStart: pct(hover)}} />
        {hoverLabel && <span className={styles.hoverLabel} aria-hidden="true" style={{insetInlineStart: `clamp(var(--seek-label-half), ${pct(hover)}, calc(100% - var(--seek-label-half)))`}}>{hoverLabel}</span>}</>}
      {marks.map(mark => player
        ? <span key={mark.id} className={styles.segment} data-seek-mark data-kind={mark.kind}
          style={{...(mark.fromEnd ? {insetInlineEnd: pct(1 - (mark.end ?? mark.start))} : {insetInlineStart: pct(mark.start)}), width: pct((mark.end ?? mark.start) - mark.start)}} />
        : <span key={mark.id}>
          <i className={styles.tick} style={{insetInlineStart: pct(mark.start)}} />
          {mark.current && mark.end !== undefined && mark.end > mark.start && <i className={styles.current} style={{insetInlineStart: pct(mark.start), width: pct(mark.end - mark.start)}} />}
        </span>)}
      {!player && <i className={styles.knob} style={{insetInlineStart: pct(value)}} />}
      {overlay}
      {player && range}
    </span>
    {!player && range}
  </div>;
}
