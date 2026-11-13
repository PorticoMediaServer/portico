import type {ReactNode} from 'react';
import type {FactPart} from '@core/presentation/kit-model';
import {AmbientArt, Artwork, type AmbientArtSource, type ArtworkPalette, type ArtworkSource} from '../artwork';
import {TitleFactsLine} from './TitleParts';
import styles from './cover-hero.module.css';

/** portrait: a book or comic (2:3); periodical: a magazine or newspaper issue (3:4); square: an
 * audiobook or its series; artist: a person's page, a music artist or an audiobook author
 * (desktop-listen #artist: a 640px hero, the circle large on the right, the name lower left;
 * phone-app r5: a 164px circle over the name). */
export type CoverHeroShape = 'portrait' | 'periodical' | 'square' | 'artist';

/** A presentation model, not a transport DTO. Unknown fields remain absent. */
export type CoverHeroProps = Readonly<{
  title: string; titleStyle?: 'standard' | 'long' | 'sparse';
  /** The line above the title: a series and its number, or the kind of page. */
  eyebrow?: ReactNode;
  /** Drawn before the facts on their line: the author, the narrators. */
  byline?: ReactNode;
  facts: readonly FactPart[];
  cover?: ArtworkSource; shape?: CoverHeroShape;
  /** Drawn when there is no cover: the generated cover, or an author's initials. */
  fallback: ReactNode;
  /** Real volume covers only, fanned in place of one cover. */
  stack?: readonly ArtworkSource[];
  /** The page's colour behind the hero: the cover's own ambient art, else the palette. */
  ambient?: AmbientArtSource | null; palette?: ArtworkPalette;
  description?: ReactNode; opening?: Readonly<{label: string; text: string}>;
  actions: ReactNode;
  /** The title's ⋯ menu: it belongs on the actions row, after My List and Mark as read (W-4). */
  overflow?: ReactNode;
  under?: ReactNode;
}>;

/** The cover beside the copy, one element for every page whose subject is a cover: a book
 * (desktop-read 3/3c/5, phone-read 3/3c/12, tablet-read 2/2c), an audiobook, its series and its
 * author (listen 8). Phones stack it, centred. */
export function CoverHero({title, titleStyle, eyebrow, byline, facts, cover, shape = 'portrait', fallback, stack, ambient, palette, description, opening, actions, overflow, under}: CoverHeroProps) {
  const fanned = stack?.slice(0, 3);
  const artist = shape === 'artist';
  return <div className={styles.heroRegion} data-shape={shape}>
    {(ambient || palette) && <AmbientArt className={styles.ambient} treatment={artist ? 'artist' : 'wash'} source={ambient} palette={palette}/>}
    <header className={styles.hero} data-shape={shape} data-stack={fanned?.length || undefined}>
      <div className={styles.cover} data-shape={shape} data-stack={fanned?.length || undefined}>
        {artist && ambient && <AmbientArt className={styles.discGlow} treatment="cover" source={ambient}/>}
        {fanned?.length ? fanned.map((source, index) => <Artwork key={`${source.key}:${index}`} source={source} alt="" fit="cover" className={`${styles.coverImage} ${styles.stackedCover}`} />)
          : <Artwork source={cover} alt={title} fallback={fallback} fit="cover" className={styles.coverImage} />}
      </div>
      <div className={styles.copy}>
        {eyebrow && <div className={styles.eyebrow}>{eyebrow}</div>}
        <h1 className={styles.title} data-style={titleStyle}>{title}</h1>
        {(byline || facts.length > 0) && <div className={styles.meta}>{byline}{facts.length > 0 && <TitleFactsLine parts={facts}/>}</div>}
        {description && <div className={styles.description}>{description}</div>}
        {!description && opening && <div className={styles.opening}><span>{opening.label}</span><p>{opening.text}</p></div>}
        <div className={styles.actions}>{actions}{overflow}</div>
        {under && <div className={styles.under}>{under}</div>}
      </div>
    </header>
  </div>;
}

/** A link-like name in the byline (an author, a series in the eyebrow). */
export function CoverHeroLink({onOpen, children}: Readonly<{onOpen?: () => void; children: ReactNode}>) {
  return onOpen ? <button type="button" className={styles.link} onClick={onOpen}>{children}</button> : <span className={styles.link}>{children}</span>;
}
