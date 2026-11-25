import type {HTMLAttributes, MouseEvent, ReactNode} from 'react';
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
/** `lead` is drawn as the line's first fact (an album's artist link, with its picture). */
export function TitleFactsLine({parts, genres, lead}: {parts: readonly FactPart[]; genres?: ReactNode; lead?: ReactNode}) {
  return <div className={styles.facts}>{lead && <span data-fact="lead">{lead}</span>}{parts.map(part => <span key={part.id} data-fact={part.id} data-role={part.role}>{part.id === 'genres' && genres ? genres : part.text}</span>)}</div>;
}
/** Artwork-led Top result, desktop-search frame 4. Its heading scale is specific to this frame. */
export function SearchResultHero({title, eyebrow, artwork, facts, synopsis, actions, href, onOpen}: {
  title: string; eyebrow: string; artwork: ReactNode; facts: ReactNode; synopsis?: string; actions: ReactNode;
  href?: string; onOpen?: () => void;
}) {
  const open = (event: MouseEvent<HTMLAnchorElement>) => {
    if (!onOpen || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    onOpen();
  };
  const heading = href ? <a className={styles.searchTitleLink} href={href} onClick={open}>{title}</a>
    : onOpen ? <button type="button" className={styles.searchTitleLink} onClick={onOpen}>{title}</button>
    : title;
  return <section className={styles.searchHero} aria-label={eyebrow}>
    {artwork}
    <div className={styles.searchCopy}><p className={styles.searchKind}>{eyebrow}</p><h3>{heading}</h3>{facts}{synopsis && <TitleProse variant="hero" className={styles.searchSynopsis} markup={synopsis} />}{actions}</div>
    {href ? <a className={styles.searchBackdropLink} href={href} aria-hidden="true" tabIndex={-1} onClick={open} />
      : onOpen ? <span className={styles.searchBackdropLink} aria-hidden="true" onClick={onOpen} /> : null}
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
    <div className={styles.main}><div className={styles.context}>{context}</div><h3>{title}</h3>{facts}{synopsis && <TitleProse markup={synopsis} />}{actions}</div>
    {credits && <div className={styles.credits}>{credits}</div>}
  </section>;
}
