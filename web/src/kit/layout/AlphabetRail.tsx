import {useLayoutEffect, useRef, useState, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {alphabetKeys, railBand, railIndexAt, railPitch, type PositionAnchor, type VisibleRange} from './geometry';
import styles from './layout.module.css';

export type AlphabetRailProps = {anchors: readonly PositionAnchor[]; visibleRange: VisibleRange; onJump: (anchor: PositionAnchor) => void; platform?: 'desktop' | 'phone'; className?: string; style?: CSSProperties};
export function AlphabetRail({anchors, visibleRange, onJump, platform = 'desktop', className, style}: AlphabetRailProps) {
  const keys = alphabetKeys(anchors), band = railBand(keys, anchors, visibleRange), available = new Map(anchors.map(anchor => [anchor.key, anchor]));
  const [bubble, setBubble] = useState<string | null>(null), dragging = useRef(false), root = useRef<HTMLElement>(null), buttons = useRef<(HTMLButtonElement | null)[]>([]);
  // J-4: desktop-browse draws 21px rows for # and A–Z. A library that also files titles under other
  // letters (Cyrillic, Greek, Arabic…) appends those keys, so the column tightens to stay in the
  // window instead of running its last letters off the bottom edge.
  const [desktopPitch, setDesktopPitch] = useState(21);
  useLayoutEffect(() => {
    if (platform === 'phone') return;
    const fit = () => {
      const element = root.current;
      if (!element) return;
      const top = element.getBoundingClientRect().top + window.scrollY;
      const room = Math.max(0, window.innerHeight - top - 12);
      const next = railPitch(room, keys.length);
      setDesktopPitch(current => Math.abs(current - next) < .01 ? current : next);
    };
    fit();
    window.addEventListener('resize', fit);
    return () => window.removeEventListener('resize', fit);
  }, [platform, keys.length]);
  const itemHeight = platform === 'phone' ? Math.max(514, keys.length * 17) / keys.length : desktopPitch;
  const jump = (key: string | undefined) => { const anchor = key === undefined ? undefined : available.get(key); if (anchor) { setBubble(anchor.key); onJump(anchor); } };
  const point = (y: number) => keys[railIndexAt(y, buttons.current.map(button => button?.getBoundingClientRect()))];
  return <nav ref={root} className={[styles.alphabet, className].filter(Boolean).join(' ')} style={style} data-platform={platform} aria-label={defaultI18n.t('next.layout.jumpToTitle')}
    onPointerDown={event => { if (event.button !== 0) return; dragging.current = true; event.currentTarget.setPointerCapture(event.pointerId); jump(point(event.clientY)); event.preventDefault(); }}
    onPointerMove={event => { const key = point(event.clientY); if (dragging.current) jump(key); else setBubble(key !== undefined && available.has(key) ? key : null); }}
    onPointerUp={event => { dragging.current = false; event.currentTarget.releasePointerCapture(event.pointerId); setBubble(null); }}
    onPointerCancel={() => { dragging.current = false; setBubble(null); }} onPointerLeave={() => { if (!dragging.current) setBubble(null); }}>
    {band && <span className={styles.band} style={{top: band.first * itemHeight, height: (band.last - band.first + 1) * itemHeight}} />}
    {keys.map((key, index) => <button type="button" key={key} ref={element => { buttons.current[index] = element; }} disabled={!available.has(key)} style={{height: itemHeight}} data-current={band && index === band.first || undefined} data-compact-hit-target
      aria-label={defaultI18n.t('next.layout.jumpTo', {letter: key})} onClick={event => { if (event.detail === 0) jump(key); }}
      onFocus={() => setBubble(key)} onBlur={() => setBubble(null)} onKeyDown={event => {
        if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
        event.preventDefault();
        const enabled = keys.map((letter, i) => available.has(letter) ? i : -1).filter(i => i >= 0);
        const at = enabled.indexOf(index), next = event.key === 'Home' ? enabled[0] : event.key === 'End' ? enabled[enabled.length - 1] : enabled[Math.max(0, Math.min(enabled.length - 1, at + (event.key === 'ArrowDown' ? 1 : -1)))];
        if (next !== undefined) buttons.current[next]?.focus();
      }}>{key}</button>)}
    {bubble && <span className={styles.bubble} aria-hidden="true" style={{top: keys.indexOf(bubble) * itemHeight - (58 - itemHeight) / 2} as CSSProperties}>{bubble}</span>}
  </nav>;
}
