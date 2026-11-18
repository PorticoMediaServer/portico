import type {ReactNode} from 'react';
import {Artwork, type ArtworkSource} from '../artwork';
import {Button} from '../controls';
import styles from './search.module.css';

export type TopResultCardProps = Readonly<{
  /** "Top result". */
  eyebrow: string; title: string;
  /** The facts line (kit `TitleFactsLine`). */
  facts: ReactNode;
  /** The title's own picture, blurred behind the card. */
  background?: ArtworkSource;
  /** An artist's circle: their picture, or their initials when there is none. */
  ring?: Readonly<{source?: ArtworkSource; initials: string}>;
  onOpen: () => void;
  /** Only where the title really plays. */
  play?: Readonly<{label: string; onPress: () => void}>;
}>;

/** The Top result at phone width (phone-app 8): one card with the title's own background, the artist's circle when it
 * has one, its facts, and a round Play. From 600 the Top result is the kit title's `SearchResultHero`. Moved from
 * `screens/search/TopResult.tsx`; the Search screen draws this same part. */
export function TopResultCard({eyebrow, title, facts, background, ring, onOpen, play}: TopResultCardProps) {
  return <section className={styles.phoneTop} aria-label={eyebrow}>
    {background && <Artwork className={styles.phoneTopBg} source={background} alt=""/>}
    <button type="button" className={styles.phoneTopOpen} aria-label={title} onClick={onOpen}/>
    {ring && <span className={styles.phoneTopRing}>{ring.source ? <Artwork source={ring.source} alt=""/> : ring.initials}</span>}
    <span className={styles.phoneTopCopy}><small>{eyebrow}</small><b>{title}</b>{facts}</span>
    {play && <Button className={styles.phoneTopPlay} variant="primary" icon="i-play" iconOnly aria-label={play.label} onClick={play.onPress}/>}
  </section>;
}
