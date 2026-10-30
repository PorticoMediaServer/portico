import type {CSSProperties, ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {AmbientArt, type AmbientArtProps, type AmbientArtSource, type ArtworkSource} from '../artwork';
import {loadingPhase, skeletonGridWindow, type SkeletonCaptionLines, type SkeletonWindow} from './model';
import styles from './skeletons.module.css';

export type SkeletonProps = Readonly<{width?: CSSProperties['width']; height?: CSSProperties['height']; radius?: number; line?: boolean; small?: boolean; sheen?: boolean; className?: string; style?: CSSProperties;
  /** A page placeholder with no gate of its own stays invisible for its first 300 ms (motion.html 2.14). */
  quiet?: boolean}>;
/** A still shape. No pulse, shimmer, timer or focus target. `quiet` delays its fade-in by 300 ms
 * in CSS (no timer), for callers that do not already wait out the quiet period themselves. */
export function Skeleton({quiet, ...props}: SkeletonProps) {
  return <Block {...props} className={[quiet && styles.deferred, props.className].filter(Boolean).join(' ')}/>;
}
/** The composite skeletons below already wait out the quiet 300 ms before they draw. */
function Block({width, height, radius, line, small, sheen, className, style}: SkeletonProps) {
  return <div aria-hidden="true" className={[styles.block, line && styles.line, small && styles.small, sheen && styles.sheen, className].filter(Boolean).join(' ')} style={{width, height, borderRadius: radius, ...style}}/>;
}

export type GridSkeletonProps = Readonly<{
  elapsedMs: number;
  window: SkeletonWindow;
  itemCount: number;
  columns?: number;
  gutter?: number;
  columnGap?: number;
  rowGap?: number;
  ratio?: number;
  captions?: boolean;
  leadingCaptionLines?: readonly SkeletonCaptionLines[];
  /** Only arrived cells in the supplied visible window are mounted. */
  arrived?: ReadonlyMap<number, ReactNode>;
  className?: string;
}>;
export function GridSkeleton(props: GridSkeletonProps) {
  const {elapsedMs, window, itemCount, captions = true, arrived, className} = props;
  const geometry = skeletonGridWindow(window, itemCount, props);
  const visible = loadingPhase(elapsedMs) !== 'waiting';
  return <div className={[styles.grid, className].filter(Boolean).join(' ')} style={{height: geometry.totalHeight}} aria-busy="true" aria-label={defaultI18n.t('next.states.gridSkeleton')} data-skeleton-grid data-phone={window.width < 1024 || undefined} data-visible-count={geometry.cells.length}>
    {geometry.cells.map(cell => {
      const content = arrived?.get(cell.index);
      if (content == null && !visible) return null;
      return <div key={cell.index} className={content == null ? styles.cell : `${styles.cell} ${styles.arrived}`} style={{left: cell.x, top: cell.y, width: cell.width}} data-skeleton-index={cell.index}>
        {content ?? <><Block height={cell.height} sheen={cell.index === 2}/>{(props.leadingCaptionLines?.[Math.floor(cell.index / geometry.columns)] ?? (captions ? 2 : 0)) > 0 && <Block line small={window.width < 1024} width={`${window.width < 1024 ? [70,60,75,50][Math.max(0,cell.index-2)%4] : [70,60,80,55,70][Math.max(0,cell.index-2)%5]}%`} className={styles.caption}/>}</>}
      </div>;
    })}
  </div>;
}

export type SkeletonRow = Readonly<{
  id: string;
  shape: 'wide' | 'poster' | 'square';
  heading?: ReactNode;
  arrived?: ReadonlyMap<number, ReactNode>;
}>;
/** Actual sources and decode callbacks remain owned by the artwork renderer. */
export type SkeletonArtwork = Pick<AmbientArtProps, 'source' | 'foreground' | 'palette' | 'visible' | 'onReady' | 'onError'>;
export type HomeSkeletonProps = Readonly<{
  elapsedMs: number;
  window: SkeletonWindow;
  header?: ReactNode;
  art?: SkeletonArtwork;
  visible?: boolean;
  hero?: ReactNode;
  thumbnails?: ReactNode;
  rows?: readonly SkeletonRow[];
  /** The notice belongs to the overlay family and appears only after eight seconds. */
  slowNotice?: ReactNode;
  className?: string;
}>;
const defaultRows: readonly SkeletonRow[] = [{id: 'continue', shape: 'wide'}, {id: 'posters', shape: 'poster'}];

export function HomeSkeleton({elapsedMs, window, header, art, visible = true, hero, thumbnails, rows = defaultRows, slowNotice, className}: HomeSkeletonProps) {
  const phase = loadingPhase(elapsedMs);
  const phone = window.width < 1024;
  const gutter = phone ? 20 : window.width < 1440 ? 32 + (window.width - 1024) / 13 : 64;
  let nextRowTop = phone ? 586 : 668;
  return <div className={[styles.page, styles.home, phone && styles.phone, className].filter(Boolean).join(' ')} aria-busy="true" aria-label={defaultI18n.t('next.states.homeSkeleton')}>
    {art ? <div className={styles.homeArt}><AmbientArt {...art} visible={visible && art.visible !== false} arrivalDurationMs={240} treatment={phone ? 'phone' : 'hero'}/></div> : null}{header}
    {phase === 'slow' && slowNotice}
    {hero ?? (phase !== 'waiting' && <div className={styles.heroWords}>
      <Block line small={phone} width={phone ? 170 : 230} height={phone ? 11 : 15} className={styles.accentLine}/>
      <Block width={phone ? 270 : 540} height={phone ? 44 : 66} radius={phone ? 12 : 14} className={styles.heroTitle}/>
      {!phone && <Block width={360} height={66} radius={14} sheen/>}
      <Block line small={phone} width={phone ? 180 : 280} className={styles.heroMeta}/>
      {!phone && <div className={styles.synopsis}><Block line width={520}/><Block line width={490}/><Block line width={360}/></div>}
      <div className={styles.heroActions}><Block width={phone ? 98 : 124} height={phone ? 46 : 54} radius={phone ? 10 : 12}/><Block width={phone ? 118 : 136} height={phone ? 46 : 54} radius={phone ? 10 : 12}/></div>
    </div>)}
    {!phone && (thumbnails ?? (phase !== 'waiting' && <div className={styles.thumbnails}>{Array.from({length: 4}, (_, index) => <Block key={index} width={132} height={74} radius={8}/>)}</div>))}
    {rows.map(row => {
      const cardWidth = row.shape === 'wide' ? phone ? 262 : 316 : row.shape === 'square' ? phone ? 150 : 192 : phone ? 118 : 172;
      const cardHeight = row.shape === 'wide' ? cardWidth * 9 / 16 : row.shape === 'square' ? cardWidth : cardWidth * 1.5;
      const gap = phone ? 12 : 16;
      const top = nextRowTop;
      nextRowTop += 44 + cardHeight + (phone ? 26 : 34) + (row.shape === 'wide' ? 0 : 40);
      if (top >= window.scrollTop + window.height || top + cardHeight + 44 < window.scrollTop) return null;
      const count = Math.max(0, phone ? Math.ceil((window.width - gutter + gap) / (cardWidth + gap)) : Math.floor((window.width - gutter * 2 + gap) / (cardWidth + gap)));
      return <section key={row.id} className={styles.row} style={{top: top - window.scrollTop}}>
        <div className={styles.rowHeading} style={{paddingLeft: gutter}}>{row.heading ?? (phase !== 'waiting' && <Block line width={phone ? 170 : 210} height={phone ? 18 : 20}/>)}</div>
        <div className={styles.track} style={{paddingLeft: gutter, gap}}>{Array.from({length: count}, (_, index) => {
          const content = row.arrived?.get(index);
          return <div key={index} className={content == null ? styles.rowCell : `${styles.rowCell} ${styles.arrived}`} style={{width: cardWidth}}>
            {content ?? (phase !== 'waiting' && <Block height={cardHeight} sheen={index === 2}/>)}
          </div>;
        })}</div>
      </section>;
    })}
  </div>;
}

export type PressedTitle = Readonly<{title: ReactNode; artwork?: ArtworkSource; ambient?: AmbientArtSource; palette?: AmbientArtProps['palette']; position?: string}> & Pick<SkeletonArtwork, 'visible' | 'onReady' | 'onError'>;
export type TitleSkeletonProps = Readonly<{
  elapsedMs: number;
  window: SkeletonWindow;
  pressed: PressedTitle;
  visible?: boolean;
  header?: ReactNode;
  tabs?: ReactNode;
  arrivedFacts?: ReactNode;
  arrivedDescription?: ReactNode;
  arrivedActions?: ReactNode;
  episodes?: ReactNode;
  className?: string;
}>;
export function TitleSkeleton({elapsedMs, window, pressed, visible = true, header, tabs, arrivedFacts, arrivedDescription, arrivedActions, episodes, className}: TitleSkeletonProps) {
  const phone = window.width < 1024;
  const showShapes = loadingPhase(elapsedMs) !== 'waiting';
  const episodeCount = phone ? 1 : Math.max(0, Math.floor((window.width - 128 + 16) / 332));
  return <div className={[styles.page, styles.titlePage, phone && styles.phone, className].filter(Boolean).join(' ')} aria-busy="true" aria-label={defaultI18n.t('next.states.titleSkeleton')}>
    <div className={styles.titleArt}><AmbientArt className={phone ? styles.pressedArtwork : undefined} source={pressed.ambient} foreground={pressed.artwork} palette={pressed.palette} position={pressed.position ?? (phone ? '44% 20%' : '30% 25%')} visible={visible && pressed.visible !== false} arrivalDurationMs={240} onReady={pressed.onReady} onError={pressed.onError} treatment={phone ? 'phone' : 'title'}/></div>
    {header}
    <div className={styles.titleWords}><h1>{pressed.title}</h1>
      {arrivedFacts ?? (showShapes && <Block line small={phone} width={phone ? 210 : 320} height={phone ? 11 : 16}/>)}
      {!phone && <>
        {arrivedDescription ?? (showShapes && <div className={styles.titleSynopsis}><Block line width={540}/><Block line width={520}/><Block line width={380}/></div>)}
        {arrivedActions ?? (showShapes && <div className={styles.titleActions}><Block width={160} height={54} radius={12} sheen/><Block width={140} height={54} radius={12}/><Block width={54} height={54} radius={12}/><Block width={54} height={54} radius={12}/></div>)}
      </>}
    </div>
    {phone && <div className={styles.phoneDetails}>
      {arrivedActions ?? (showShapes && <div className={styles.titleActions}><Block height={46} sheen className={styles.grow}/>{Array.from({length: 3}, (_, index) => <Block key={index} width={46} height={46}/>)}</div>)}
      {arrivedDescription ?? (showShapes && <div className={styles.phoneSynopsis}><Block line width="100%"/><Block line width="70%"/></div>)}
      {tabs && <div className={styles.phoneTabs}>{tabs}</div>}
      {episodes ?? (showShapes && <div className={styles.episodeList}><Block width={136} style={{aspectRatio: '16 / 9'}}/><div className={styles.grow}><Block line width="80%"/><Block line small width="40%"/></div></div>)}
    </div>}
    {!phone && <><div className={styles.titleTabs}>{tabs}</div><div className={styles.titleEpisodes}>{episodes ?? (showShapes && Array.from({length: episodeCount}, (_, index) => <Block key={index} width={316} style={{aspectRatio: '16 / 9', flex: 'none'}}/>))}</div></>}
  </div>;
}
