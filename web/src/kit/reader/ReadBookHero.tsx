import {useState, type ReactNode} from 'react';
import type {FactPart} from '@portico/client-core/kit-model';
import {generatedCoverTint} from '@design';
import {Artwork, GeneratedCover, type ArtworkSource} from '../artwork';
import {Tabs} from '../controls';
import {useWindowWidth} from '../theme';
import {readBookTitleStyle} from './book-title';
import styles from './book.module.css';

/** A presentation model, not a transport DTO. Unknown fields remain absent. */
export type ReadBookHeroProps = Readonly<{
  title: string; author?: Readonly<{name: string; onOpen?: () => void; face?: ReactNode}>;
  series?: Readonly<{label: string; onOpen: () => void}>; facts: readonly FactPart[];
  cover?: ArtworkSource; periodical?: boolean; generatedFontScale?: number; ambient?: ReactNode;
  /** Real volume covers only; an ordinary generated cover remains the empty fallback. */
  seriesCovers?: readonly ArtworkSource[];
  titleStyle?: 'standard' | 'long' | 'sparse';
  description?: string; opening?: Readonly<{label: string; text: string}>;
  actions: ReactNode; under?: ReactNode;
}>;

/** desktop-read 3/3c/5; phone-read 3/3c/12; tablet-read 2/2c. */
export function ReadBookHero({title, author, series, facts, cover, seriesCovers, periodical = false, generatedFontScale, ambient, titleStyle, description, opening, actions, under}: ReadBookHeroProps) {
  const generated = <GeneratedCover title={title} author={author?.name} glyph={Array.from(title.trim())[0] ?? ''} tint={generatedCoverTint(title)} fontScale={generatedFontScale} />;
  const stack = seriesCovers?.slice(0, 3);
  return <div className={styles.heroRegion}>
    {ambient && <div className={styles.ambient} aria-hidden="true">{ambient}</div>}
    <header className={styles.hero} data-stack={stack?.length || undefined}>
      <div className={styles.cover} data-periodical={periodical || undefined} data-stack={stack?.length || undefined}>
        {stack?.length ? stack.map((source, index) => <Artwork key={`${source.key}:${index}`} source={source} alt="" fit="cover" className={`${styles.coverImage} ${styles.stackedCover}`} />)
          : <Artwork source={cover} alt={title} fallback={generated} fit="cover" className={styles.coverImage} />}
      </div>
      <div className={styles.copy}>
        {series && <button type="button" className={styles.series} onClick={series.onOpen}>{series.label}</button>}
        <h1 className={styles.title} data-style={titleStyle ?? readBookTitleStyle(title, !description && !!opening)}>{title}</h1>
        {(author || facts.length > 0) && <div className={styles.facts}>
          {author && (author.onOpen ? <button type="button" className={styles.author} onClick={author.onOpen}>{author.face}{author.name}</button> : <span className={styles.author}>{author.face}{author.name}</span>)}
          {facts.map(fact => <span key={fact.id} data-fact={fact.id}>{fact.text}</span>)}
        </div>}
        {description && <p className={styles.description}>{description}</p>}
        {!description && opening && <div className={styles.opening}><span>{opening.label}</span><p>{opening.text}</p></div>}
        <div className={styles.actions}>{actions}</div>
        {under && <div className={styles.under}>{under}</div>}
      </div>
    </header>
  </div>;
}

export type ReadDetailFact = Readonly<{id: string; label: string; value: ReactNode; source?: string}>;
export function ReadBookFacts({heading, facts}: Readonly<{heading: string; facts: readonly ReadDetailFact[]}>) {
  if (facts.length === 0) return null;
  return <section className={styles.details}><h2>{heading}</h2><dl>{facts.map(fact => <div key={fact.id}><dt>{fact.label}</dt><dd>{fact.value}{fact.source && <small>{fact.source}</small>}</dd></div>)}</dl></section>;
}

export function ReadBookSections({contents, details, labels}: Readonly<{contents?: ReactNode; details?: ReactNode; labels: Readonly<{contents: string; details: string}>}>) {
  const width = useWindowWidth(), [selected, setSelected] = useState('contents');
  if (!contents && !details) return null;
  if (width < 1024) return <Tabs className={styles.compactSections} listClassName={styles.sectionTabs} phone={width < 600}
    value={selected === 'contents' && contents ? 'contents' : details ? 'details' : 'contents'} onValueChange={setSelected}
    items={[...(contents ? [{value: 'contents', label: labels.contents, panel: contents}] : []),
      ...(details ? [{value: 'details', label: labels.details, panel: details}] : [])]}/>;
  return <div className={styles.sections}>{contents && <section>{contents}</section>}{details}</div>;
}
