import {useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode, type HTMLAttributes} from 'react';
import {heroSpec} from '@design';
import type {FactPart} from '@portico/client-core/kit-model';
import {defaultI18n} from '@i18n';
import {Lockup, layoutType, platformType, type LayoutPlatform} from './Lockup';
import {boundedIndex} from './geometry';
import {useLayoutMotion} from './motion';
import styles from './layout.module.css';

export type HeroCandidate = Readonly<{id: string; title: string; navigationLabel?: string; long?: boolean; reason?: string; overview?: string; facts?: readonly FactPart[]; thumbnail?: string}>;
export type HeroRenderContext = Readonly<{index: number; active: boolean; visible: boolean; paused: boolean; reducedMotion: boolean; platform: LayoutPlatform; onReady?: () => void; onError?: () => void}>;
export type HeroProps<T extends HeroCandidate> = Omit<HTMLAttributes<HTMLElement>, 'children' | 'onChange'> & {
  /** A bounded, server-authored spotlight set. This primitive never chooses library entries. */
  candidates: readonly T[];
  renderArtwork: (candidate: T, context: HeroRenderContext) => ReactNode;
  renderThumbnail?: (candidate: T, context: HeroRenderContext) => ReactNode;
  renderActions?: (candidate: T, context: HeroRenderContext) => ReactNode;
  renderMetadata?: (candidate: T, context: HeroRenderContext) => ReactNode;
  renderReason?: (candidate: T, context: HeroRenderContext) => ReactNode;
  renderSupplement?: (candidate: T, context: HeroRenderContext) => ReactNode;
  /** The title opens the candidate's page: a real link (new tab, copy link) that the screen opens in place. */
  titleLink?: (candidate: T) => {href: string; open: () => void} | undefined;
  activeIndex?: number; defaultIndex?: number; onIndexChange?: (index: number) => void;
  active?: boolean; visible?: boolean; heroFocused?: boolean; reducedMotion?: boolean; platform?: LayoutPlatform;
  /** tablet.html's centred medium-width Home composition. */
  sizeClass?: 'medium';
  showNavigation?: boolean;
  contentVisible?: boolean;
  onArtworkReady?: (candidate: T) => void;
  onArtworkError?: (candidate: T) => void;
};

export function HeroFacts({parts, platform = 'desktop', sizeClass, children}: {parts: readonly FactPart[]; platform?: LayoutPlatform; sizeClass?: 'medium'; children?: ReactNode}) {
  return <div className={styles.facts} style={{...layoutType(platformType(platform).meta), ...(sizeClass === 'medium' ? {fontSize: 15} : {})}}>{parts.map(part =>
    <span key={part.id} className={part.id === 'content-rating' || part.id === 'season-count' || part.id === 'duration' ? styles.tag : undefined} data-fact-role={part.role}>{part.text}</span>)}{children}</div>;
}

export function HeroProgress({progress, remaining, label = defaultI18n.t('next.layout.progress')}: {progress: number; remaining: ReactNode; label?: string}) {
  if (!Number.isFinite(progress)) return null;
  const value = Math.max(0, Math.min(1, progress));
  return <span className={styles.heroProgress}><span className={styles.progressTrack} role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={1} aria-valuenow={value}><i style={{transform: `scaleX(${value})`}} /></span>{remaining}</span>;
}

export function Hero<T extends HeroCandidate>({candidates, renderArtwork, renderThumbnail, renderActions, renderMetadata, renderReason, renderSupplement, titleLink, activeIndex, defaultIndex = 0, onIndexChange,
  active = true, visible = true, heroFocused = false, reducedMotion = false, showNavigation = true, contentVisible = true, onArtworkReady, onArtworkError, platform = 'desktop', sizeClass, className, onMouseEnter, onMouseLeave, onFocusCapture, onBlurCapture, ...props}: HeroProps<T>) {
  const [ownIndex, setOwnIndex] = useState(defaultIndex), [hovered, setHovered] = useState(false), [focused, setFocused] = useState(false);
  const motion = useLayoutMotion(reducedMotion), index = boundedIndex(activeIndex ?? ownIndex, candidates.length), id = useId();
  const paused = !active || !visible || !contentVisible || !motion.visible || motion.reduced || hovered || (platform === 'tv' ? !heroFocused : focused);
  const choose = (next: number) => { const value = boundedIndex(next, candidates.length); if (value >= 0 && value !== index) { if (activeIndex === undefined) setOwnIndex(value); onIndexChange?.(value); } };
  const chooseRef = useRef(choose); chooseRef.current = choose;
  const remaining = useRef<number>(heroSpec.rotation.intervalMs), clockId = useRef('');
  const currentId = candidates[index]?.id ?? '';
  const [copyId, setCopyId] = useState(currentId);
  useLayoutEffect(() => {
    const frame = requestAnimationFrame(() => setCopyId(currentId));
    return () => cancelAnimationFrame(frame);
  }, [currentId]);
  const priorId = useRef(currentId), [exitingId, setExitingId] = useState<string | null>(null);
  const fadeMs = motion.reduced ? heroSpec.rotation.reducedMs : heroSpec.rotation.crossfadeMs;
  useLayoutEffect(() => {
    const previous = priorId.current; priorId.current = currentId;
    if (previous === currentId) return;
    setExitingId(previous || null);
  }, [currentId]);
  useEffect(() => {
    if (!exitingId) return;
    const timer = window.setTimeout(() => setExitingId(null), fadeMs);
    return () => window.clearTimeout(timer);
  }, [exitingId, fadeMs]);
  useEffect(() => {
    if (clockId.current !== currentId) { remaining.current = heroSpec.rotation.intervalMs; clockId.current = currentId; }
    if (paused || candidates.length < 2) return;
    const started = performance.now();
    const timer = window.setTimeout(() => { remaining.current = heroSpec.rotation.intervalMs; chooseRef.current((index + 1) % candidates.length); }, remaining.current);
    return () => { window.clearTimeout(timer); remaining.current = Math.max(0, remaining.current - (performance.now() - started)); };
  }, [currentId, index, paused, candidates.length]);
  const thumbRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const type = platformType(platform), phone = platform === 'phone-app' || platform === 'phone-web';
  const contextFor = (candidate: T, candidateIndex: number): HeroRenderContext => ({index: candidateIndex, active: candidateIndex === index && contentVisible,
    visible: visible && motion.visible && (candidateIndex === index || candidate.id === exitingId || (active || showNavigation) && candidateIndex === (index + 1) % candidates.length), paused, reducedMotion: motion.reduced, platform,
    onReady: onArtworkReady ? () => onArtworkReady(candidate) : undefined, onError: onArtworkError ? () => onArtworkError(candidate) : undefined});
  const entry = candidates[index], context = entry ? contextFor(entry, index) : undefined;
  const reason = entry && context ? renderReason ? renderReason(entry, context) : entry.reason : undefined;
  const link = entry ? titleLink?.(entry) : undefined;
  const title = entry && link ? <a className={styles.titleLink} href={link.href} onClick={event => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault(); link.open();
  }}>{entry.title}</a> : entry?.title;
  return <section {...props} className={[styles.hero, className].filter(Boolean).join(' ')} data-platform={platform} data-size-class={sizeClass} data-paused={paused} data-reduced-motion={motion.reduced} data-navigation={showNavigation && candidates.length > 1}
    aria-label={props['aria-label'] ?? defaultI18n.t('next.layout.featured')} aria-roledescription={defaultI18n.t('next.layout.carousel')}
    onMouseEnter={event => { setHovered(true); onMouseEnter?.(event); }} onMouseLeave={event => { setHovered(false); onMouseLeave?.(event); }}
    onFocusCapture={event => { setFocused(true); onFocusCapture?.(event); }} onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setFocused(false); onBlurCapture?.(event); }}>
    {candidates.map((candidate, candidateIndex) => {
      return <div key={candidate.id} className={styles.slide} data-active={candidateIndex === index && contentVisible} aria-hidden="true"
        onTransitionEnd={event => { if (event.target === event.currentTarget && event.propertyName === 'opacity' && candidate.id === exitingId) setExitingId(null); }}>
        <div className={styles.heroArtwork} data-hero-artwork data-drift={!motion.reduced && platform !== 'tv'}>{renderArtwork(candidate, contextFor(candidate, candidateIndex))}{platform === 'tv' && <div className={styles.tvScrim} />}</div>
      </div>;
    })}
    {entry && context && <div id={`${id}-panel`} className={styles.copy} data-active={contentVisible && copyId === currentId} inert={!contentVisible} aria-hidden={!contentVisible || undefined}
      role={platform !== 'tv' && showNavigation && candidates.length > 1 ? 'tabpanel' : undefined} aria-labelledby={platform !== 'tv' && showNavigation && candidates.length > 1 ? `${id}-tab-${index}` : undefined}>
      {reason && <p className={styles.reason} style={{...layoutType(type.eyebrow), ...(sizeClass === 'medium' ? {fontSize: 15} : {})}}>{reason}</p>}
      <Lockup title={title} long={entry.long} platform={platform} style={sizeClass === 'medium' ? {fontSize: 62, lineHeight: .94} : undefined} />
      {renderMetadata ? renderMetadata(entry, context) : entry.facts?.length ? <HeroFacts parts={entry.facts} platform={platform} /> : null}
      {entry.overview && <p className={styles.overview} style={layoutType(type.synopsis)}>{entry.overview}</p>}
      {renderActions && <div className={styles.actions}>{renderActions(entry, context)}</div>}
      {renderSupplement?.(entry, context)}
      {platform === 'tv' && showNavigation && candidates.length > 1 && <div className={styles.tvDots} aria-hidden="true">{candidates.map((candidate, candidateIndex) => <i key={candidate.id} data-active={candidateIndex === index} />)}</div>}
    </div>}
    {platform !== 'tv' && showNavigation && candidates.length > 1 && <div className={phone ? styles.dots : styles.thumbnails} role="tablist" aria-label={defaultI18n.t('next.layout.featuredTitles')}>
      {candidates.map((candidate, candidateIndex) => <button type="button" key={candidate.id} ref={element => { thumbRefs.current[candidateIndex] = element; }}
        id={`${id}-tab-${candidateIndex}`} className={phone ? styles.dot : styles.thumbnail} role="tab" tabIndex={candidateIndex === index ? 0 : -1}
        aria-label={candidate.title} aria-controls={`${id}-panel`} aria-selected={candidateIndex === index} data-active={candidateIndex === index} data-compact-hit-target={phone || undefined}
        onClick={() => choose(candidateIndex)} onKeyDown={event => {
          let next = candidateIndex;
          if (event.key === 'ArrowRight') next = (candidateIndex + 1) % candidates.length;
          else if (event.key === 'ArrowLeft') next = (candidateIndex - 1 + candidates.length) % candidates.length;
          else if (event.key === 'Home') next = 0;
          else if (event.key === 'End') next = candidates.length - 1;
          else return;
          event.preventDefault(); choose(next); thumbRefs.current[next]?.focus();
        }}>
        {!phone && <>{renderThumbnail && <span className={styles.thumbnailImage}>{renderThumbnail(candidate, {index: candidateIndex, active: candidateIndex === index, visible: visible && motion.visible, paused, reducedMotion: motion.reduced, platform})}</span>}<span className={styles.timer}><i key={currentId} /></span><span className={styles.thumbnailTitle}>{candidate.navigationLabel ?? candidate.title}</span></>}
      </button>)}
    </div>}
  </section>;
}
