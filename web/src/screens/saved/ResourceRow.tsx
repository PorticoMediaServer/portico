import {useRef, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {ContentEntry} from '@core/library-content.ts';
import {shapeFor, shapeAspect} from '@core/presentation/card-shape.ts';
import {timelineFacts} from '@portico/client-core/kit-model';
import {durationText} from '@core/presentation/kit-model/text.ts';
import type {ShellArtwork} from '../../shell/artwork';
import {Artwork} from '../../kit/artwork';
import {CardFrame, Facts, Progress} from '../../kit/cards/CardFrame';
import {withSourceArtActivation} from '../../kit/cards/source-art-activation';
import type {CardProps} from '../../kit/cards/types';
import styles from './saved.module.css';

const t = defaultI18n.t;
/** collections.html frame 4: ordered mixed-media rows retain the shared card's
 * selection, long-press, keyboard and action admission. Only the paint differs. */
export function ResourceRow({entryId, media, artwork, ordinal, library, handle, ...props}: CardProps & {
  entryId: string; media: ContentEntry; artwork: ShellArtwork; ordinal?: number; library?: string; handle?: ReactNode;
}) {
  const art = useRef<HTMLSpanElement>(null);
  const shape = shapeFor(media.kind, 'list'), timeline = timelineFacts(media.duration, media.progressSeconds);
  const remaining = (media.progressSeconds ?? 0) > 0 ? durationText(timeline.remainingSeconds, t) : undefined;
  const length = durationText(media.duration, t);
  const status = media.watched ? t('next.cards.watched') : remaining ? t('next.kit.remaining', {time: remaining}) : length;
  const navigate = () => {
    const origin = art.current;
    withSourceArtActivation(origin, props.onNavigate, () => art.current === origin && origin?.isConnected === true);
  };
  return <div className={styles.resourceEntry} data-resource-entry={entryId} data-reordering={!!handle || undefined}>
    <CardFrame {...props} onNavigate={navigate} selection={handle ? undefined : props.selection} onPlay={handle ? undefined : props.onPlay} kind="resourceRow" className={styles.resourceEntryCard} primaryClassName={styles.resourceEntryPrimary}
      overlay={handle ? <span className={styles.resourceOrderHandle}>{handle}</span> : undefined}>
      <span className={styles.resourceOrdinal}>{ordinal === undefined ? '' : defaultI18n.number(ordinal)}</span>
      <span ref={art} className={styles.resourceEntryArt} data-shape={shape}>
        <Artwork source={artwork.foreground(media.posterUrl, 128, shapeAspect(shape))} alt=""/>
        <Progress value={timeline.progress} className={styles.resourceEntryProgress}/>
      </span>
      <span className={styles.resourceEntryCopy}><b>{props.model.title}</b><Facts parts={props.model.caption}/><small className={styles.resourceEntryCompactStatus}>{status}</small></span>
      <span className={styles.resourceEntryLibrary}>{library}</span>
      <span className={styles.resourceEntryStatus} data-progress={!!remaining || undefined}>{status}</span>
    </CardFrame>
  </div>;
}
