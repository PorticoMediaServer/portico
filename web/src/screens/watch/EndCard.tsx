import {useRef, useState} from 'react';
import {MetadataFacts} from '../../kit/MetadataFacts';
import {defaultI18n} from '@i18n';
import {Artwork} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {focusRadioChoice} from '../../kit/controls/radio-navigation';
import {Icon} from '../../kit/Icon';
import type {WatchEndCard} from './end-card';
import type {ArtworkSource} from '../../kit/artwork/types';
import styles from './end-card.module.css';

const t = defaultI18n.t;

/** desktop-player f4b. Drawn parts only: Finished, the title, the viewer's rating, Replay,
 * a way back to the title page and the server's related titles as a full row. */
export function WatchEndCardView({card, canReplay, onReplay, onClose, reportError, artwork, layout = 'desktop'}: {
  card: WatchEndCard; canReplay: boolean; onReplay: () => void; onClose?: () => void; reportError: (error: unknown) => void;
  artwork: (url: string | undefined) => ArtworkSource | undefined; layout?: 'desktop' | 'phone';
}) {
  const [hover, setHover] = useState<number | null>(null), [pending, setPending] = useState(false);
  const stored = card.rating?.value == null ? null : card.rating.value / 2, shown = hover ?? stored ?? 0;
  const [focusedStar, setFocusedStar] = useState(() => Math.max(1, Math.min(5, Math.round(stored ?? 1))));
  const stars = useRef<(HTMLButtonElement | null)[]>([]);
  const rate = (stars: number) => {
    if (!card.rating || pending) return;
    setPending(true);
    void card.rating.set(stars * 2).catch(reportError).finally(() => setPending(false));
  };
  const act = (action: () => void | Promise<void>) => { try { void Promise.resolve(action()).catch(reportError); } catch (error) { reportError(error); } };
  return <div className={styles.card} data-watch-end-card data-layout={layout}>
    <div className={styles.left}>
      <p className={styles.kicker}>{t('player.post.finished')}</p>
      <h2 className={styles.title}>{card.title}</h2>
      {layout === 'phone' && (card.year !== undefined || card.genres?.length) && <div className={styles.meta}><MetadataFacts parts={[card.year !== undefined && <strong>{card.year}</strong>, card.genres?.length ? card.genres.join(', ') : null]} /></div>}
      {card.rating && <div className={styles.rate}>
        <span>{stored == null ? t('player.rateThis') : t('player.rated', {stars: t('title.stars', {count: stored})})}</span>
        <div className={styles.stars} role="radiogroup" aria-label={t('player.rateThis')} onMouseLeave={() => setHover(null)}>
          {[1, 2, 3, 4, 5].map(star => <button key={star} ref={element => {stars.current[star - 1] = element;}} type="button" role="radio" aria-checked={stored === star} tabIndex={focusedStar === star ? 0 : -1} aria-label={t('title.stars', {count: star})}
            disabled={pending} data-lit={shown >= star || undefined} onMouseEnter={() => setHover(star)} onFocus={() => {setFocusedStar(star); setHover(star);}} onBlur={() => setHover(null)}
            onKeyDown={event => focusRadioChoice(event, star - 1, 5, index => stars.current[index]?.focus())} onClick={() => rate(star)}
            ><Icon name={shown >= star ? 'i-star-f' : 'i-star'} size={layout === 'phone' ? 22 : 28} /></button>)}
        </div>
      </div>}
      <div className={styles.actions}>
        {canReplay && <Button variant={layout === 'phone' ? 'primary' : 'glass'} size="small" icon={layout === 'phone' ? 'i-restart' : 'i-back'} onClick={onReplay}>{t('player.post.replay')}</Button>}
        {layout === 'phone'
          ? onClose && <Button variant="glass" size="small" onClick={onClose}>{t('player.close')}</Button>
          : <Button size="small" onClick={() => act(card.back)}>{t('player.post.backTo', {title: card.title})}</Button>}
      </div>
    </div>
    {card.collection && <section className={styles.feature} aria-label={t('player.collection.next', {collection: card.collection.collectionName})}>
      <div className={styles.featureArt}><Artwork source={card.collection.posterUrl ? artwork(card.collection.posterUrl) : undefined} alt="" width="100%" height="100%" />
        <span className={styles.featureText}><small>{t('player.collection.next', {collection: card.collection.collectionName})}</small>
          <b>{card.collection.title}</b>{(card.collection.year !== undefined || card.collection.genres?.length) && <MetadataFacts parts={[card.collection.year, card.collection.genres?.length ? card.collection.genres.join(', ') : null]} />}</span></div>
      <div className={styles.actions}><Button variant="primary" size="small" icon="i-play" onClick={() => act(() => card.collection!.play())}>{t('player.collection.play', {title: card.collection.title})}</Button>
        {card.collection.myList && <Button variant="glass" size="small" icon={card.collection.myList.listed ? 'i-check' : 'i-plus'} aria-pressed={card.collection.myList.listed} disabled={card.collection.myList.pending}
          onClick={() => {const myList = card.collection!.myList!;void myList.set(!myList.listed);}}>{t('next.controls.myList')}</Button>}</div>
    </section>}
    {card.related.length > 0 && <section className={styles.row} aria-label={t('player.moreLike', {title: card.title})}>
      <h2>{layout === 'phone' ? <>{t('player.moreLike', {title: card.title})}<Icon name="i-right" size={18} aria-hidden="true" /></> : t('player.moreLike', {title: card.title})}</h2>
      <div className={styles.track}>
        {(layout === 'phone' ? card.related.slice(0, 4) : card.related).map(entry => <button key={entry.id} type="button" className={styles.related} onClick={() => act(() => card.openRelated(entry.id))} aria-label={entry.title}>
          <span className={styles.art}><Artwork source={entry.poster} alt="" width="100%" height="100%" /></span>
          <span className={styles.name}>{entry.title}</span>
          {entry.year !== undefined && <span className={styles.year}>{entry.year}</span>}
        </button>)}
      </div>
    </section>}
  </div>;
}
