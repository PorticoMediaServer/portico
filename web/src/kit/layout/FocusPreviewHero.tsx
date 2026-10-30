import {useEffect, useRef, useState, type ReactNode} from 'react';
import {Hero, type HeroCandidate, type HeroProps} from './Hero';
import {useLayoutMotion} from './motion';
import styles from './layout.module.css';

function useDebouncedValue<T>(value: T, key: string | null, hold: boolean, initialValue: T, initialKey: string | null): T {
  const [settled, setSettled] = useState(initialValue);
  const latest = useRef(value), settledKey = useRef(initialKey); latest.current = value;
  useEffect(() => { if (key === settledKey.current) setSettled(value); }, [value, key]);
  useEffect(() => {
    if (hold) return;
    const timer = window.setTimeout(() => { settledKey.current = key; setSettled(latest.current); }, 250);
    return () => window.clearTimeout(timer);
  }, [key, hold]);
  return key === settledKey.current ? value : settled;
}

export function usePreviewDebounce<T>(value: T, key: string | null, hold = false): T {
  return useDebouncedValue(value, key, hold, value, key);
}

export function PreviewDebounce<T>({value, getKey, hold = false, children}: {value: T; getKey: (value: T) => string | null; hold?: boolean; children: (settled: T) => ReactNode}) {
  return children(usePreviewDebounce(value, getKey(value), hold));
}

export type FocusPreviewHeroProps<T extends HeroCandidate> = Omit<HeroProps<T>, 'candidates' | 'activeIndex' | 'onIndexChange' | 'renderActions' | 'platform' | 'onArtworkReady' | 'onArtworkError' | 'showNavigation' | 'contentVisible'> & {
  entry: T | null; holdingDirection?: boolean;
  onPreviewError?: () => void;
  onPreviewReady?: (entry: T) => void;
};

/** Only an actual bounded focus summary is accepted; artwork remains owned by its renderer. */
export function FocusPreviewHero<T extends HeroCandidate>({entry, holdingDirection = false, reducedMotion = false, renderArtwork, onPreviewError, onPreviewReady, className, ...props}: FocusPreviewHeroProps<T>) {
  const settled = useDebouncedValue(entry, entry?.id ?? null, holdingDirection, null, null), [layers, setLayers] = useState<readonly T[]>([]), [activeId, setActiveId] = useState<string | null>(null);
  const motion = useLayoutMotion(reducedMotion), active = useRef(activeId), incoming = useRef(settled);
  active.current = activeId; incoming.current = settled;
  useEffect(() => {
    if (!settled) { setLayers([]); setActiveId(null); return; }
    setLayers(previous => {
      const shown = previous.find(candidate => candidate.id === active.current);
      return shown && shown.id !== settled.id ? [shown, settled] : [settled];
    });
  }, [settled]);
  const renderedLayers = layers.map(candidate => candidate.id === settled?.id ? settled : candidate);
  return <div className={[styles.preview, className].filter(Boolean).join(' ')}>
    <Hero {...props} reducedMotion={motion.reduced} candidates={renderedLayers} activeIndex={Math.max(0, layers.findIndex(candidate => candidate.id === activeId))} contentVisible={activeId !== null} showNavigation={false}
      renderArtwork={(candidate, context) => renderArtwork(candidate, {...context, visible: props.visible !== false && motion.visible && (context.visible || candidate.id === settled?.id)})}
      onArtworkReady={candidate => { if (candidate.id === incoming.current?.id) { setActiveId(candidate.id); onPreviewReady?.(candidate); } }} onArtworkError={candidate => { if (candidate.id === incoming.current?.id) onPreviewError?.(); }} active={false} platform="tv" />
  </div>;
}
