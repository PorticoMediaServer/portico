import type {ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {ChannelMarkModel, FactPart} from '@portico/client-core/kit-model';
import {Artwork, ChannelMark, type ArtworkSource} from '../artwork';
import {Icon} from '../Icon';
import {Button} from '../controls';
import {usePhone} from '../overlays/shared';
import {differingProfileName} from '../shell/model';
import {CardFrame, Facts, Progress, cx, tintStyle} from './CardFrame';
import {BoundedCovers, CardArtwork, CardAmbient, type CardCover} from './MediaCards';
import {boundedProgress} from './interaction';
import type {CardProps, MediaCardProps} from './types';
import styles from './cards.module.css';

export type LiveCardProps = Omit<CardProps, 'model'> & Readonly<{
  channel: Readonly<{sourceId: string; id: string; name: string; number?: string; mark?: ChannelMarkModel; favorite?: boolean}>;
  programme?: Readonly<{title: string; facts: readonly FactPart[]; extra?: readonly FactPart[]; progress?: number; remaining?: string; live?: boolean; isNew?: boolean; premiere?: boolean; recording?: boolean}>;
  logo?: ArtworkSource;
  tint?: string;
  results?: boolean;
  large?: boolean;
  onRemind?: () => void;
  reminderSet?: boolean;
}>;
export function LiveCard({channel, programme, logo, tint, results, large, onRemind, reminderSet, ...props}: LiveCardProps) {
  return <CardFrame {...props} className={cx(results && styles.liveResults, large && styles.liveLarge, props.className)} model={{title: programme?.title ?? channel.name, caption: []}} additionalDescription={[channel.name, channel.number, ...(programme?.facts.map(part => part.text) ?? []), ...(programme?.extra?.map(part => part.text) ?? []), programme?.remaining, programme?.live ? defaultI18n.t('next.cards.live') : undefined, programme?.isNew ? defaultI18n.t('next.cards.new') : undefined, programme?.premiere ? defaultI18n.t('next.cards.premiere') : undefined, programme?.recording ? defaultI18n.t('next.cards.recording') : undefined]} kind="live" style={{...tintStyle(tint), ...props.style}} overlay={onRemind && <button type="button" disabled={props.disabled} className={styles.remind} aria-label={defaultI18n.t('guide.remind')} aria-pressed={reminderSet} onClick={onRemind}><Icon name="i-bell" size={16} /></button>}>
    <span className={styles.channel}>{channel.mark ? <ChannelMark mark={channel.mark} source={logo} size={34} visible={props.visible} /> : <span className={styles.channelPlaceholder}>{logo && <Artwork source={logo} fit="contain" alt="" visible={props.visible}/>}</span>}<span className={styles.channelName}>{channel.name}{channel.favorite && <Icon name="i-star" size={13}/>}{channel.number && <span className={styles.channelNumber}>{channel.number}</span>}</span>
      {programme?.live && <span className={styles.liveFlag}>{defaultI18n.t('next.cards.live')}</span>}
    </span>
    <span className={styles.programme}>{programme?.title ?? channel.name}</span><span className={styles.programmeFlags}>{programme?.isNew && <span>{defaultI18n.t('next.cards.new')}</span>}{programme?.premiere && <span>{defaultI18n.t('next.cards.premiere')}</span>}{programme?.recording && <span className={styles.recording}><Icon name="i-rec" size={12} />{defaultI18n.t('next.cards.recording')}</span>}</span><Facts parts={programme?.facts} className={styles.liveWhen} /><Facts parts={programme?.extra} className={styles.liveExtra} />{programme?.remaining ? <span className={styles.liveProgress}><Progress value={programme.progress}/><span>{programme.remaining}</span></span> : <Progress value={programme?.progress} />}
  </CardFrame>;
}

type NavigationCardProps = Omit<MediaCardProps, 'onPlay' | 'playLabel'>;
export type CollectionCardProps = NavigationCardProps & Readonly<{
  covers: readonly ArtworkSource[];
  kindLabel?: string;
  description?: string;
  actionLabel?: string;
  onAction?: () => void;
  actionVariant?: 'primary' | 'glass';
}>;
function Collection({variant, covers, kindLabel, description, actionLabel, onAction, actionVariant = 'primary', ...props}: CollectionCardProps & {variant: 'collectionBanner' | 'collectionTile'}) {
  return <CardFrame {...props} onPlay={undefined} kind={variant} additionalDescription={[kindLabel, description]} footer={onAction && <span className={styles.collectionAction}><Button disabled={props.disabled} variant={actionVariant} size="compact" icon={actionVariant === 'primary' ? 'i-play' : undefined} aria-label={`${actionLabel ?? defaultI18n.t('next.cards.playAll')}: ${props.model.title}`} onClick={onAction}>{actionLabel ?? defaultI18n.t('next.cards.playAll')}</Button></span>}>
    <CardAmbient {...props} treatment={variant === 'collectionTile' ? 'collection-tile' : 'cover'} /><span className={styles.collectionBackdrop}><CardArtwork {...props} /></span><span className={styles.collectionCopy}>{kindLabel && <span className={styles.kindLabel}>{kindLabel}</span>}{variant === 'collectionTile' && <BoundedCovers covers={covers} visible={props.visible} arrivalDurationMs={props.arrivalDurationMs} limit={4} className={styles.collectionMinis} />}<span className={styles.collectionTitle}>{props.model.title}</span>{description && <span className={styles.collectionDescription}>{description}</span>}{variant === 'collectionBanner' ? props.context !== 'detail' && <BoundedCovers covers={covers} visible={props.visible} arrivalDurationMs={props.arrivalDurationMs} limit={4} className={styles.collectionMinis} /> : <span className={styles.collectionMeta}><Facts parts={props.model.caption} className={styles.collectionFacts} /><Facts parts={props.model.detail} className={styles.collectionDetail} /></span>}</span>{variant === 'collectionBanner' && props.context === 'detail' && <BoundedCovers covers={covers} visible={props.visible} arrivalDurationMs={props.arrivalDurationMs} limit={4} className={styles.collectionMinis} />}{variant === 'collectionTile' && <Progress value={props.model.progress} edge />}
  </CardFrame>;
}
export function CollectionBanner(props: CollectionCardProps) { return <Collection {...props} variant="collectionBanner" />; }
export function CollectionTile(props: CollectionCardProps) { return <Collection {...props} variant="collectionTile" />; }

export type GenreTileProps = NavigationCardProps & Readonly<{variant: 'video' | 'music' | 'poster'; covers?: readonly ArtworkSource[]; tint?: string}>;
export function GenreTile({variant, covers = [], tint, ...props}: GenreTileProps) {
  return <CardFrame {...props} onPlay={undefined} kind={variant === 'music' ? 'musicGenre' : variant === 'poster' ? 'posterGenre' : 'videoGenre'} style={{...tintStyle(tint), ...props.style}}>{variant === 'video' ? <span className={styles.genreBackdrop}><CardArtwork {...props} /></span> : <BoundedCovers covers={variant === 'poster' ? covers.slice(0, 2) : covers} shape={variant === 'poster' ? 'poster' : 'square'} visible={props.visible} arrivalDurationMs={props.arrivalDurationMs} className={styles.genreFan} />}<span className={styles.genreCopy}><span className={styles.genreTitle}>{props.model.title}</span><Facts parts={props.model.caption} className={styles.genreFacts} /></span></CardFrame>;
}

export type LibraryCardProps = NavigationCardProps & Readonly<{covers: readonly ArtworkSource[]; shape: 'poster' | 'square' | 'periodical'}>;
function Library({row, covers, shape, ...props}: LibraryCardProps & {row: boolean}) {
  return <CardFrame {...props} onPlay={undefined} kind={row ? 'libraryRow' : 'libraryCard'}><CardAmbient {...props} treatment={row ? 'library-row' : 'library'} /><span className={styles.libraryCopy}><span className={styles.libraryTitle}>{props.model.title}</span><Facts parts={props.model.caption} className={styles.libraryFacts} /></span><BoundedCovers covers={covers} shape={shape} visible={props.visible} arrivalDurationMs={props.arrivalDurationMs} className={styles.libraryFan} /></CardFrame>;
}
export function LibraryCard(props: LibraryCardProps) { return <Library {...props} row={false} />; }
export function LibraryRow(props: LibraryCardProps) { return <Library {...props} row />; }

export type ResumeCardProps = MediaCardProps & Readonly<{location?: FactPart; remaining?: FactPart; kind?: 'book' | 'magazine' | 'newspaper'; generated?: ReactNode; ambientTreatment?: 'resume' | 'resume-phone'}>;
function Resume({read, location, remaining, kind = 'book', generated, ambientTreatment, ...props}: ResumeCardProps & {read: boolean}) {
  const phone = usePhone();
  return <CardFrame {...props} additionalDescription={[location?.text, remaining?.text]} kind={read ? 'readResume' : 'resume'} playIcon={read ? 'i-book' : 'i-play'} className={cx(read && kind === 'book' && !generated && styles.resumeSpine, kind !== 'book' && styles.resumePeriodical, props.className)}><CardAmbient {...props} treatment={ambientTreatment ?? (phone ? 'resume-phone' : 'resume')} /><span className={styles.resumeCover}><CardArtwork {...props} fallback={generated ?? props.fallback} position={kind === 'newspaper' ? 'top' : props.position} /></span><span className={styles.resumeCopy}><span className={styles.resumeTitle}>{props.model.title}</span><Facts parts={props.model.caption} className={styles.resumeByline} /><span className={styles.resumeWhere}>{location && <span>{location.text}</span>}{remaining && <span>{remaining.text}</span>}</span><Progress value={props.model.progress} /></span></CardFrame>;
}
export function ResumeCard(props: ResumeCardProps) { return <Resume {...props} read={false} />; }
export function ReadResumeCard(props: ResumeCardProps) { return <Resume {...props} read />; }

export type SeriesSegment = Readonly<{id: string; state: 'read' | 'in-progress' | 'unread'; progress?: number}>;
export type SeriesTileProps = NavigationCardProps & Readonly<{covers: readonly CardCover[]; segments: readonly SeriesSegment[]; tint?: string}>;
export function SeriesTile({covers, segments, tint, ...props}: SeriesTileProps) {
  return <CardFrame {...props} onPlay={undefined} kind="series" style={{...tintStyle(tint), ...props.style}}><BoundedCovers covers={covers} visible={props.visible} arrivalDurationMs={props.arrivalDurationMs} className={styles.seriesFan} /><span className={styles.seriesCopy}><span className={styles.seriesTitle}>{props.model.title}</span><Facts parts={props.model.caption} className={styles.seriesFacts} /><span className={styles.segments} aria-hidden="true">{segments.map(segment => <span key={segment.id}><span style={{width: `${(segment.state === 'read' ? 1 : segment.state === 'in-progress' ? boundedProgress(segment.progress) ?? 0 : 0) * 100}%`}} /></span>)}</span></span></CardFrame>;
}

export type QuickTileProps = MediaCardProps & Readonly<{playing?: boolean; onPause?: () => void}>;
export function QuickTile({playing, onPause, ...props}: QuickTileProps) {
  return <CardFrame {...props} onPlay={playing ? onPause : props.onPlay} kind="quick" playIconSize={16} className={cx(playing && styles.playing, props.className)} playIcon={playing ? 'i-pause' : 'i-play'}><span className={styles.quickCover}><CardArtwork {...props} /></span><span className={styles.quickCopy}><span className={styles.quickTitle}>{props.model.title}</span><span className={styles.quickSubtitle}>{playing && <span className={styles.levelMeter} aria-hidden="true"><i /><i /><i /><i /></span>}<Facts parts={props.model.caption} /></span></span></CardFrame>;
}

export type SessionChannel = Readonly<{mark: ChannelMarkModel; logo?: ArtworkSource; tint?: string; facts: readonly FactPart[]}>;
export type SessionCardProps = NavigationCardProps & Readonly<{
  /** name remains the actual profile label; username is genuine supplied account identity. */
  viewer: Readonly<{name: string; username?: string; device: string; avatar?: ReactNode}>;
  facts: readonly FactPart[];
  paused?: boolean;
  onStop?: () => void;
}> & (Readonly<{media?: 'video' | 'audio'; channel?: never}> | Readonly<{media: 'channel'; channel: SessionChannel}>);
export function SessionCard({viewer, facts, paused, media = 'video', channel, onStop, onMore, moreExpanded, ...props}: SessionCardProps) {
  const username = viewer.username?.trim() ? viewer.username : undefined;
  const profile = username ? differingProfileName(viewer.name, username) : undefined;
  return <CardFrame {...props} onPlay={undefined} onMore={undefined} additionalDescription={[username ?? viewer.name, profile, viewer.device, ...facts.map(part => part.text), ...(channel?.facts.map(part => part.text) ?? []), paused ? defaultI18n.t('next.cards.paused') : undefined]} kind="session" className={cx(paused && styles.paused, props.className)} overlay={<>
    <Progress value={props.model.progress} edge />
    {paused && <span className={styles.sessionState}><Icon name="i-pause" size={12} />{defaultI18n.t('next.cards.paused')}</span>}
    {(onStop || onMore) && <span className={styles.sessionActions}>{onStop && <Button disabled={props.disabled} variant="glass" size="xs" icon="i-stop" onClick={onStop}>{defaultI18n.t('next.cards.stop')}</Button>}{onMore && <Button disabled={props.disabled} variant="glass" size="xs" icon="i-more" iconOnly aria-label={`${defaultI18n.t('next.cards.more')}: ${props.model.title}`} aria-haspopup="menu" aria-expanded={moreExpanded} onClick={event => onMore(event.currentTarget)} />}</span>}
  </>}>
    <span className={cx(styles.sessionArt, media === 'audio' && styles.sessionAudio, media === 'channel' && styles.sessionChannel)} style={tintStyle(channel?.tint)}>{channel ? <><ChannelMark mark={channel.mark} source={channel.logo} size={32} visible={props.visible} /><Facts parts={channel.facts} /></> : <>{media === 'audio' && <CardAmbient {...props} treatment="session-audio" />}<span className={styles.sessionForeground}><CardArtwork {...props} /></span></>}</span><span className={styles.sessionCopy}><span className={styles.sessionTitle}>{props.model.title}</span><Facts parts={props.model.caption} className={styles.sessionSubtitle} /><span className={styles.viewer}>{viewer.avatar}<strong>{username ?? viewer.name}</strong>{profile && <span className={styles.sessionProfile}>{profile}</span>}<span>{viewer.device}</span></span><Facts parts={facts} className={styles.sessionFacts} /></span>
  </CardFrame>;
}
