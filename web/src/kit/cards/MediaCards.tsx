import {useCallback, useRef, useState, type ReactNode, type CSSProperties} from 'react';
import {defaultI18n} from '@i18n';
import {Icon} from '../Icon';
import type {FactPart} from '@portico/client-core/kit-model';
import {Artwork, AmbientArt, CircleArt, PersonCircle, GeneratedCover, CoverMosaic, useCoverAspect, wholeCoverBox, type AmbientArtTreatment, type ArtworkSource, type ArtworkStatus} from '../artwork';
import {CardBadges, CardCaption, CardFrame, Facts, Progress, cx} from './CardFrame';
import type {MediaCardProps} from './types';
import styles from './cards.module.css';

export function CardArtwork({artwork, fallback, position, visible, arrivalDurationMs, onReady, fit}: Pick<MediaCardProps, 'artwork' | 'fallback' | 'position' | 'visible' | 'arrivalDurationMs'> & {onReady?: (source: ArtworkSource) => void; fit?: 'cover' | 'contain'}) {
  const [read, setRead] = useState<{key?: string; status: ArtworkStatus}>({status: artwork ? 'loading' : 'empty'});
  const key = artwork?.key;
  const onStatusChange = useCallback((status: ArtworkStatus) => setRead(previous => previous.key === key && previous.status === status ? previous : {key, status}), [key]);
  // J-25: while an image loads or waits for its retry, a card with placeholder art (the generated cover)
  // shows it instead of a blank tile; only a card without one draws the loading surface.
  const loading = !!artwork && (read.key !== key || read.status === 'loading');
  return <Artwork source={artwork} visible={visible} arrivalDurationMs={arrivalDurationMs} alt="" fit={fit} position={position} onStatusChange={onStatusChange} onReady={onReady} fallback={loading && fallback == null ? <span className={styles.loadingArt} /> : fallback} />;
}

/** Art within this share of the 2:3 slot's shape is a poster and fills it as before. */
const posterShape = 2 / 3, sameShape = .08;
export function PosterCard({titleLines, progressPlacement = 'edge', ...props}: MediaCardProps & {titleLines?: 1 | 2; progressPlacement?: 'edge' | 'inset'}) {
  const artworkRef = useRef<HTMLSpanElement>(null), cover = useCoverAspect(props.artwork, artworkRef);
  // Real art is never cut (owner): art of another shape than the slot (an audiobook's square cover in a mixed row) is drawn
  // whole and centred over its own blurred wash: the server's ambient for the item when the card has it, else the art itself.
  const whole = cover.aspect !== undefined && Math.abs(cover.aspect / posterShape - 1) > sameShape;
  return <CardFrame {...props} artworkRef={artworkRef} playIconSize={20} className={cx(progressPlacement === 'inset' && styles.insetProgress, props.className)} kind="poster"><span ref={artworkRef} className={styles.art}><CardArtwork {...props} fit={whole ? 'contain' : 'cover'} onReady={cover.onReady} />{whole && <span className={styles.artWash} aria-hidden="true"><Artwork source={props.ambient ?? props.artwork} alt="" /></span>}<CardBadges watched={props.watched} isNew={props.isNew} progress={props.model.progress} /></span><CardCaption model={props.model} extraCaption={props.extraCaption} textPresentation={props.textPresentation} titleLines={titleLines} /></CardFrame>;
}

export function WideCard(props: MediaCardProps) {
  const artworkRef = useRef<HTMLSpanElement>(null);
  return <CardFrame {...props} artworkRef={artworkRef} kind="wide"><span ref={artworkRef} className={styles.art}><CardArtwork {...props} /><span className={styles.wideCopy}><span className={styles.wideTitle}>{props.model.title}</span><Facts parts={props.model.caption} className={styles.wideSubtitle} /><Progress value={props.model.progress} /></span></span><Facts parts={props.model.detail} className={styles.wideDetail} /></CardFrame>;
}

export function SquareCard({variant = 'music', progressPlacement = 'edge', ...props}: MediaCardProps & {variant?: 'music' | 'audiobook'; progressPlacement?: 'edge' | 'below' | 'inset'}) {
  const artworkRef = useRef<HTMLSpanElement>(null), cover = useCoverAspect(props.artwork, artworkRef);
  // A cover that is not square is drawn whole in its own shape, at the foot of the square slot (owner's rule: a cover is never cropped).
  return <CardFrame {...props} artworkRef={artworkRef} watchedLabel={variant === 'audiobook' ? defaultI18n.t('next.cards.finished') : props.watchedLabel} className={cx(progressPlacement === 'below' && styles.belowProgress, progressPlacement === 'inset' && styles.insetProgress, props.className)} kind="square"><span ref={artworkRef} className={styles.art} style={wholeCoverBox(cover.aspect, '100%', {align: 'end'})}><CardArtwork {...props} onReady={cover.onReady} /><CardBadges watched={props.watched} completionLabel={variant === 'audiobook' ? defaultI18n.t('next.cards.finished') : undefined} isNew={props.isNew} progress={progressPlacement === 'below' ? undefined : props.model.progress} /></span>{progressPlacement === 'below' && <Progress value={props.model.progress} />}<CardCaption model={props.model} extraCaption={props.extraCaption} textPresentation={props.textPresentation} /></CardFrame>;
}

export type CircleCardProps = Omit<MediaCardProps, 'artwork' | 'fallback' | 'ambient' | 'palette' | 'position' | 'arrivalDurationMs'> & {covers: readonly ArtworkSource[]; initials?: string; variant?: 'artist' | 'person'; coverZoom?: number; coverTransformOrigin?: CSSProperties['transformOrigin']};
export function CircleCard({covers, initials, variant = initials ? 'person' : 'artist', coverZoom, coverTransformOrigin, ...props}: CircleCardProps) {
  const initialsSize = props.context === 'search' ? 34 : props.context === 'detail' ? 30 : undefined;
  const artworkRef = useRef<HTMLSpanElement>(null);
  return <CardFrame {...props} artworkRef={artworkRef} kind="circle"><span ref={artworkRef} className={styles.art}>{variant === 'person' ? <PersonCircle covers={covers} initials={initials ?? ''} alt="" initialsSize={initialsSize} visible={props.visible} /> : <CircleArt covers={covers} coverZoom={coverZoom} coverTransformOrigin={coverTransformOrigin} initials={initials} alt="" treatment={props.context === 'search' ? 'search' : 'artist'} initialsSize={initialsSize} visible={props.visible} />}</span><CardCaption model={props.model} extraCaption={props.extraCaption} textPresentation={props.textPresentation} /></CardFrame>;
}

export type EpisodeTileProps = MediaCardProps & Readonly<{
  artworkKind: 'still' | 'backdrop';
  length?: string;
  status?: readonly FactPart[];
  airDate?: FactPart;
  cropOrigin?: string;
  description?: string;
  expanded?: boolean;
  playing?: boolean;
  playback?: readonly FactPart[];
}>;
export function EpisodeTile({artworkKind, length, status, airDate, cropOrigin, description, expanded, playing, playback, ...props}: EpisodeTileProps) {
  const artworkRef = useRef<HTMLSpanElement>(null);
  return <CardFrame {...props} artworkRef={artworkRef} additionalDescription={[length, ...(status?.map(part => part.text) ?? []), airDate?.text, ...(playback?.map(part => part.text) ?? []), description]} className={cx(expanded && styles.expanded, playing && styles.episodePlaying, props.className)} kind="episode"><span ref={artworkRef} className={styles.art} data-part="art"><span className={cx(styles.episodePicture, artworkKind === 'backdrop' && styles.backdropCrop)} style={{transformOrigin: cropOrigin}}><CardArtwork {...props} /></span>{length && <span className={styles.length}>{length}</span>}<CardBadges watched={props.watched} progress={props.model.progress ?? (props.next ? 1 : undefined)} />{playing && <span className={styles.episodeMeter} aria-hidden="true"><span className={styles.episodeLevels}><i /><i /><i /></span></span>}</span>
    {/* `data-part` names the tile's parts for layouts that place them (the title page's episode list). */}
    <span className={styles.episodeCopy} data-part="copy"><span className={styles.episodeTitle} data-part="title">{props.model.title}</span><span className={styles.episodeMeta}>{status && <Facts parts={status} className={styles.episodeStatus} />}<Facts parts={airDate ? [...props.model.caption, airDate] : props.model.caption} className={styles.episodeLabel} /></span>{playback && <Facts parts={playback} className={styles.episodePlayback} />}</span>{description && <span className={styles.description} data-part="description">{description}</span>}
  </CardFrame>;
}

export type CoverCardProps = MediaCardProps & Readonly<{
  kind?: 'book' | 'comic' | 'magazine' | 'newspaper';
  generated?: Omit<Parameters<typeof GeneratedCover>[0], 'className' | 'style'>;
  titleLines?: 1 | 2;
}>;
export function CoverCard({kind = 'book', generated, titleLines, watchedLabel, ...props}: CoverCardProps) {
  const book = kind === 'book' || kind === 'comic';
  // The cover is drawn whole in its own shape at the foot of its slot: 2:3 for a book, 3:4 for an issue.
  const artworkRef = useRef<HTMLSpanElement>(null), cover = useCoverAspect(props.artwork, artworkRef);
  return <CardFrame {...props} watchedLabel={watchedLabel} artworkRef={artworkRef} playIconSize={20} kind="cover" playIcon="i-book" className={cx(book && !generated && styles.spine, !book && styles.periodical, kind === 'newspaper' && styles.newspaper, props.className)}>
    <span ref={artworkRef} className={styles.art} style={wholeCoverBox(cover.aspect, '100%', {slot: book ? 2 / 3 : 3 / 4, align: 'end'})}><CardArtwork {...props} position={kind === 'newspaper' ? 'top' : props.position} onReady={cover.onReady} fallback={generated ? <GeneratedCover {...generated} selectionClearance={props.selection?.active || props.selection?.checked} /> : props.fallback} /><CardBadges watched={props.watched} watchedLabel={watchedLabel} isNew={props.isNew} progress={props.model.progress} /></span><CardCaption model={props.model} extraCaption={props.extraCaption} textPresentation={props.textPresentation} titleLines={titleLines} />
  </CardFrame>;
}

export function RankedCard({rank, style, ...props}: MediaCardProps & {rank: number}) {
  return <div className={styles.ranked} style={style}><span className={styles.rank} aria-hidden="true">{rank}</span><PosterCard {...props} /></div>;
}

export type PlaylistMosaicProps = Omit<MediaCardProps, 'arrivalDurationMs'> & Readonly<{covers: readonly ArtworkSource[]}>;
export function PlaylistMosaic({covers, ...props}: PlaylistMosaicProps) {
  return <SquareCard {...props} artwork={undefined} fallback={<CoverMosaic covers={covers} shape="square" alt="" visible={props.visible} />} />;
}

export function NewPlaylistCard({model, onCreate, className}: {model: MediaCardProps['model']; onCreate: () => void; className?: string}) {
  return <SquareCard model={model} onNavigate={onCreate} className={cx(styles.newPlaylist, className)} fallback={<span className={styles.createGlyph}><Icon name="i-plus" size={30} /></span>} />;
}

export type CardCover = ArtworkSource | Readonly<{id: string; generated: NonNullable<CoverCardProps['generated']>}>;
export function BoundedCovers({covers, shape = 'poster', limit = 3, visible, arrivalDurationMs, className}: {covers: readonly CardCover[]; shape?: 'poster' | 'square' | 'periodical'; limit?: 3 | 4; visible?: boolean; arrivalDurationMs?: number; className?: string}) {
  return <span className={cx(styles.coverFan, styles[`fan-${shape}`], className)} aria-hidden="true">{covers.slice(0, limit).map((cover, index) => <span key={`${'generated' in cover ? cover.id : cover.key}-${index}`} className={styles.fanItem}>{'generated' in cover ? <GeneratedCover {...cover.generated} /> : <Artwork source={cover} alt="" visible={visible} arrivalDurationMs={arrivalDurationMs} />}</span>)}</span>;
}

export type CardAmbientProps = Pick<MediaCardProps, 'ambient' | 'palette' | 'visible' | 'arrivalDurationMs'> & {treatment?: AmbientArtTreatment};
export function CardAmbient({ambient, palette, visible, arrivalDurationMs, treatment = 'cover'}: CardAmbientProps) {
  return ambient || palette ? <span className={styles.ambient}><AmbientArt source={ambient} palette={palette} treatment={treatment} visible={visible} arrivalDurationMs={arrivalDurationMs} /></span> : null;
}

export type CardArtSlot = ReactNode;
