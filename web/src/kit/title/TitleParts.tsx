import type {HTMLAttributes, ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {Button} from '../controls';
import {Artwork, type ArtworkSource} from '../artwork';
import type {FactPart} from '@core/presentation/kit-model';
import {inlineMarkup} from '@core/presentation/inline-markup.ts';
import styles from './title-parts.module.css';

/** Exact title/biography prose from desktop-detail and detail-phone. `markup`
 * reads the catalog string through the closed bold/italic/line-break reader, so
 * a description that carried tags draws them instead of showing them. */
export function TitleProse({variant = 'body', className, markup, children, ...props}: HTMLAttributes<HTMLParagraphElement> & {variant?: 'body' | 'hero'; markup?: string}) {
  return <p {...props} data-variant={variant} className={[styles.prose, className].filter(Boolean).join(' ')}>
    {markup === undefined ? children : inlineMarkup(markup).map((run, index) => run.kind === 'break' ? <br key={index}/>
      : run.strong && run.emphasis ? <strong key={index}><em>{run.text}</em></strong>
        : run.strong ? <strong key={index}>{run.text}</strong>
          : run.emphasis ? <em key={index}>{run.text}</em> : run.text)}
  </p>;
}
/** Title pages and Top result put a border around kind/rating only, never runtime. */
export function TitleFactsLine({parts, genres}: {parts: readonly FactPart[]; genres?: ReactNode}) {
  return <div className={styles.facts}>{parts.map(part => <span key={part.id} data-fact={part.id} data-role={part.role}>{part.id === 'genres' && genres ? genres : part.text}</span>)}</div>;
}
/** Artwork-led Top result, desktop-search frame 4. Its heading scale is specific to this frame. */
export function SearchResultHero({title, eyebrow, artwork, facts, synopsis, actions}: {title: string; eyebrow: string; artwork: ReactNode; facts: ReactNode; synopsis?: string; actions: ReactNode}) {
  return <section className={styles.searchHero} aria-label={eyebrow}>
    {artwork}<div className={styles.searchCopy}><p className={styles.searchKind}>{eyebrow}</p><h3>{title}</h3>{facts}{synopsis && <TitleProse variant="hero" className={styles.searchSynopsis}>{synopsis}</TitleProse>}{actions}</div>
  </section>;
}
/** An episode opens inline under its row, on the page background. Never an overlay. */
export function EpisodeDetailsLayout({title, artwork, backdrop, context, facts, synopsis, actions, adjacent, credits, onClose}: {
  title: string; artwork?: ArtworkSource; backdrop?: boolean; context: ReactNode; facts: ReactNode;
  synopsis?: string; actions: ReactNode; adjacent?: ReactNode; credits?: ReactNode; onClose: () => void;
}) {
  return <section className={styles.episode} aria-label={defaultI18n.t('title.episodeDetails')}>
    <span className={styles.caret} aria-hidden="true" />
    <Button className={styles.close} variant="icon" icon="i-x" iconOnly aria-label={defaultI18n.t('action.close')} onClick={onClose} />
    <div className={styles.side}><div className={styles.still} data-backdrop={backdrop || undefined}><Artwork source={artwork} alt="" /></div>{adjacent}</div>
    <div className={styles.main}><div className={styles.context}>{context}</div><h3>{title}</h3>{facts}{synopsis && <TitleProse>{synopsis}</TitleProse>}{actions}</div>
    {credits && <div className={styles.credits}>{credits}</div>}
  </section>;
}
