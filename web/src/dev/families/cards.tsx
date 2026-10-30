import {useEffect, useRef, useState, useSyncExternalStore, type ComponentType, type ReactNode} from 'react';
import {defaultI18n, type MessageId} from '@i18n';
import {cardsFixtureText, isCardsFixtureId, type CardsFixtureId} from '../../../../packages/i18n/src/catalog/en-US/next/dev/cards-fixtures';
import {ArtworkScheduler} from '@core/artwork/index.ts';
import {cardText, type CardSubject, type FactPart} from '@portico/client-core/kit-model';
import {GeneratedArt, GeneratedCover, ProfileFace, type ArtworkSource, type ArtworkImage, type AmbientArtSource} from '../../kit/artwork';
import {PosterCard, WideCard, SquareCard, CircleCard, EpisodeTile, CollectionBanner, CollectionTile, GenreTile, RankedCard, LibraryCard, LibraryRow, CoverCard, ReadResumeCard, ResumeCard, SeriesTile, QuickTile, PlaylistMosaic, NewPlaylistCard, SessionCard, LiveCard, NotInterestedButton, type MediaCardProps, type CardContent, type CardProps} from '../../kit/cards';
import {referenceColours} from '../../kit/cards/dev-fixtures';
import {SelectionCircle} from '../../kit/selection';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import styles from '../../kit/cards/cards.module.css';

type FamilyKind = 'poster' | 'wide' | 'square' | 'circle' | 'live' | 'episode' | 'collection-banner' | 'collection-tile' | 'genre-video' | 'genre-music' | 'ranked' | 'library-card' | 'library-row' | 'cover' | 'read-resume' | 'resume' | 'series' | 'quick' | 'playlist' | 'session' | 'not-interested';
type ScenarioState = 'default' | 'hover' | 'focus' | 'pressed' | 'new' | 'watched' | 'progress' | 'next' | 'expanded' | 'long' | 'empty' | 'error' | 'loading' | 'playing' | 'paused' | 'generated' | 'magazine' | 'newspaper' | 'date' | 'absolute' | 'special' | 'mixed' | 'two' | 'three' | 'four' | 'create' | 'audio' | 'channel' | 'selection-entry' | 'selected' | 'selecting' | 'hold' | 'disabled' | 'future' | 'poster' | 'person-covers';
const needsInteractionMargin = (kind: FamilyKind, state: ScenarioState) => kind !== 'not-interested' && ['hover', 'focus', 'pressed', 'next', 'expanded', 'selection-entry', 'selected', 'selecting', 'hold'].includes(state);
const t = (key: MessageId | CardsFixtureId, values?: Parameters<typeof defaultI18n.t>[1]): string => isCardsFixtureId(key) ? cardsFixtureText(key) : defaultI18n.t(key, values);
const fact = (id: string, text: string, role: FactPart['role'] = 'supplement'): FactPart => ({id, text, role});
const asset = (name: string) => `http://100.120.50.24:8787/assets/${name}`;
const paths = {poster: 'pool/movies/barbie.jpg', long: 'pool/movies/the-lord-of-the-rings-the-fellowship-of-the-ring.jpg', backdrop: 'bd-tlou.jpg', album: 'sq-afterhours.jpg', book: 'sq-martian.jpg', read: 'read/covers/great-gatsby.jpg', magazine: 'read/magazines/strand-1891-07.jpg', paper: 'read/newspapers/nyt-1918-10-21.jpg', collection: 'bd-toystory.jpg'} as const;

export type CardsProofSource = Readonly<{artwork: ArtworkSource; ambient: AmbientArtSource; model: CardContent; location?: FactPart; remaining?: FactPart; onNavigate: () => void; onPlay: () => void}>;
let proofSource: CardsProofSource | undefined;
const proofListeners = new Set<() => void>();
const subscribeProof = (listener: () => void) => { proofListeners.add(listener); return () => { proofListeners.delete(listener); }; };
const readProof = () => proofSource;
/** Root supplies authenticated, versioned handles in memory; credentials never enter this module. */
export function setCardsProofSource(source: CardsProofSource | undefined) { proofSource = source; proofListeners.forEach(listener => listener()); }
function RealAmbientCase() {
  const proof = useSyncExternalStore(subscribeProof, readProof, readProof);
  return <div data-real-artwork-source-ready={!!proof}>{proof && <ResumeCard {...proof} />}</div>;
}
let referenceCase: ReactNode = null;
const referenceListeners = new Set<() => void>();
/** Evidence callers supply authored reference fixtures in memory, outside production chunks. */
export function setCardsReferenceCase(node: ReactNode) { referenceCase = node; referenceListeners.forEach(listener => listener()); }
function ReferenceCase() {
  return useSyncExternalStore(listener => {referenceListeners.add(listener); return () => {referenceListeners.delete(listener);};}, () => referenceCase, () => referenceCase);
}

function useFixtureSources(state: ScenarioState) {
  const [scheduler] = useState(() => { return new ArtworkScheduler<ArtworkImage>({
    load: async key => {
      if (state === 'error') throw new Error('Gallery artwork failure.');
      if (state === 'loading') return new Promise<ArtworkImage>(() => {});
      return {src: asset(key)};
    }, classify: () => ({retryable: false}), concurrency: 1, priorityExtra: 0, maxEntries: 16,
  }); });
  useEffect(() => () => scheduler.dispose(), [scheduler]);
  return (key: string): ArtworkSource | undefined => state === 'empty' ? undefined : {scheduler, key};
}

function useActions() {
  const [action, setAction] = useState('');
  const [events, setEvents] = useState<readonly {action: string; at: number; anchor?: boolean; intent?: unknown}[]>([]);
  const record = (key: CardsFixtureId | MessageId, detail?: {anchor?: boolean; intent?: unknown}) => {const action = t(key); setAction(action); setEvents(previous => [...previous, {action, at: performance.now(), ...detail}]);};
  return {action, events, onNavigate: () => record('next.cards.fixture.actionNavigate'), onPlay: () => record('next.cards.fixture.actionPlay'), onPause: () => record('next.cards.fixture.actionPause'), onMore: (anchor: HTMLButtonElement) => record('next.cards.fixture.actionMore', {anchor: anchor.isConnected && anchor.tagName === 'BUTTON' && anchor.getAttribute('aria-haspopup') === 'menu'}), onStop: () => record('next.cards.fixture.actionStop'), onNotInterested: () => record('next.cards.notInterested'), onRemind: () => record('guide.remind'), onSelect: (intent: unknown) => record('next.cards.fixture.actionSelect', {intent})};
}

function Scenario({kind, state, context}: {kind: FamilyKind; state: ScenarioState; context?: CardProps['context']}) {
  const source = useFixtureSources(state), actions = useActions();
  const primaryRef = useRef<HTMLButtonElement>(null);
  const [selecting, setSelecting] = useState(state === 'selected' || state === 'selecting');
  const [checked, setChecked] = useState(state === 'selected');
  const selection = ['selection-entry', 'selected', 'selecting', 'hold'].includes(state) ? {active: selecting, checked, control: <SelectionCircle interactive={false} selected={checked} selecting={selecting} accessibilityLabel={t('next.cards.fixture.actionSelect')} />, onToggle: (intent: Parameters<NonNullable<CardProps['selection']>['onToggle']>[0]) => {setSelecting(true); setChecked(previous => !previous); actions.onSelect(intent);}, onExit: () => {setSelecting(false); setChecked(false);}} : undefined;
  const subject: CardSubject = {id: 'reference-title', kind: 'movie', title: t(state === 'long' ? 'next.cards.fixture.longTitle' : 'next.cards.fixture.poster'), year: 2023};
  const ordinary = cardText(subject, 'ordinary', {t});
  const common: MediaCardProps = {model: state === 'progress' ? {...ordinary, progress: .38} : ordinary, artwork: source(state === 'long' ? paths.long : paths.poster), fallback: <GeneratedArt glyph={t('next.cards.fixture.posterGlyph')} />, onNavigate: actions.onNavigate, onPlay: actions.onPlay, onMore: actions.onMore, watched: state === 'watched', isNew: state === 'new', next: state === 'next', context, primaryRef, selection, disabled: state === 'disabled'};
  const covers = ['sq-afterhours.jpg', 'sq-abbey.jpg', 'sq-thriller.jpg', 'sq-martian.jpg'].map(key => source(key)).filter((item): item is ArtworkSource => !!item);
  const posters = ['p-ts1.jpg', 'p-ts2.jpg', 'p-ts3.jpg', 'p-ts5.jpg'].map(key => source(key)).filter((item): item is ArtworkSource => !!item);
  const album = cardText({id: 'reference-album', kind: 'album', title: t('next.cards.fixture.album'), artist: t('next.cards.fixture.artist')}, 'ordinary', {t});
  const book = cardText({id: 'reference-book', kind: 'book', title: t('next.cards.fixture.book'), author: t('next.cards.fixture.author'), chapterNumber: 15, durationSeconds: 39143, positionSeconds: 22702}, 'continuation', {t});
  const read = {title: t(state === 'generated' ? 'next.cards.fixture.generatedBook' : state === 'magazine' ? 'next.cards.fixture.magazine' : state === 'newspaper' ? 'next.cards.fixture.newspaper' : 'next.cards.fixture.readBook'), caption: [fact('author', t(state === 'generated' ? 'next.cards.fixture.generatedAuthor' : state === 'magazine' ? 'next.cards.fixture.issue' : state === 'newspaper' ? 'next.cards.fixture.issueDate' : 'next.cards.fixture.readAuthor'), 'identity')], progress: state === 'generated' ? .26 : .22};
  const readSource = source(state === 'magazine' ? paths.magazine : state === 'newspaper' ? paths.paper : paths.read);
  const generated = {title: t('next.cards.fixture.generatedBook'), author: t('next.cards.fixture.generatedAuthor'), tint: referenceColours.generated, glyph: t('next.cards.fixture.generatedGlyph'), fontScale: 17.1};
  const episodeSubject: CardSubject = {id: 'reference-episode', kind: 'episode', title: t('next.cards.fixture.episode'), show: {id: 'reference-show', title: t('next.cards.fixture.show')}, episode: state === 'date' ? {mode: 'date', day: '2026-10-21'} : state === 'special' ? {mode: 'special'} : state === 'absolute' ? {mode: 'absolute', number: 37} : {mode: 'seasonal', season: 1, episode: 3}, durationSeconds: 4500, positionSeconds: state === 'progress' ? 1800 : undefined};
  const continuation = cardText({...episodeSubject, title: t('next.cards.fixture.episodeOne'), show: {id: 'reference-show', title: t(state === 'long' ? 'next.cards.fixture.longTitle' : 'next.cards.fixture.show')}, episode: {mode: 'seasonal', season: 1, episode: 1}, durationSeconds: 4920, positionSeconds: 1080}, 'continuation', {t});
  const collection = {title: t('next.cards.fixture.collection'), caption: [fact('count', t('next.cards.fixture.collectionFacts')), fact('years', t('next.cards.fixture.collectionYears'))], detail: [fact('watched', t('next.cards.fixture.collectionWatched'), 'status')], progress: .5};
  const location = fact('location', state === 'generated' ? t('next.kit.chapter', {number: 4}) : t(state === 'magazine' ? 'next.cards.fixture.pageLocation' : 'next.cards.fixture.readLocation'), 'context');
  const remaining = fact('remaining', t(state === 'generated' ? 'next.cards.fixture.generatedReadRemaining' : state === 'magazine' ? 'next.cards.fixture.pageRemaining' : 'next.cards.fixture.readRemaining'));
  const count = state === 'two' ? 2 : state === 'three' ? 3 : state === 'four' ? 4 : 1;
  let content: ReactNode;
  switch (kind) {
    case 'poster': content = <PosterCard {...common} titleLines={state === 'long' ? 2 : undefined} onNotInterested={state === 'hover' ? actions.onNotInterested : undefined} />; break;
    case 'wide': content = <WideCard {...common} artwork={source(paths.backdrop)} position="20% 30%" model={continuation} />; break;
    case 'square': content = <SquareCard {...common} model={state === 'audio' || state === 'watched' ? book : {...album, title: state === 'long' ? t('next.cards.fixture.longTitle') : album.title, progress: state === 'progress' ? .38 : undefined}} variant={state === 'audio' || state === 'watched' ? 'audiobook' : 'music'} artwork={source(state === 'audio' || state === 'watched' ? paths.book : paths.album)} />; break;
    case 'circle': content = <CircleCard {...common} model={state === 'mixed' || state === 'person-covers' ? {title: t('next.cards.fixture.person'), caption: [fact('role', t('next.cards.fixture.role'))]} : cardText({id: 'reference-artist', kind: 'artist', title: t(state === 'long' ? 'next.cards.fixture.longTitle' : 'next.cards.fixture.artist'), albumCount: count}, 'ordinary', {t})} covers={state === 'mixed' ? [] : covers.slice(0, state === 'person-covers' ? 2 : count)} initials={state === 'mixed' || state === 'person-covers' ? t('next.cards.fixture.personInitials') : state === 'empty' ? t('next.cards.fixture.artistInitials') : undefined} variant={state === 'mixed' || state === 'person-covers' ? 'person' : 'artist'} />; break;
    case 'live': content = <LiveCard {...common} channel={{sourceId: 'reference-source', id: 'reference-channel', name: t('next.cards.fixture.channel'), number: '24', mark: {sourceId: 'reference-source', id: 'reference-channel', text: t('next.cards.fixture.channelMark'), seed: 'reference-source:reference-channel'}}} tint={referenceColours.channel} onRemind={state === 'future' ? actions.onRemind : undefined} results={state === 'future'} programme={state === 'empty' ? undefined : {title: t(state === 'future' ? 'next.cards.fixture.futureProgramme' : 'next.cards.fixture.programme'), facts: state === 'future' ? [fact('range', t('next.cards.fixture.futureRange')), fact('duration', t('next.cards.fixture.futureDuration'))] : [fact('range', t('next.cards.fixture.timeRange')), fact('remaining', t('next.cards.fixture.timeLeft'))], extra: state === 'future' ? [fact('repeats', t('next.cards.fixture.futureTimes'))] : undefined, progress: state === 'future' ? undefined : .58, live: state !== 'default' && state !== 'future', isNew: state === 'new', premiere: state === 'four', recording: state === 'playing'}} />; break;
    case 'episode': content = <EpisodeTile {...common} artwork={source(paths.backdrop)} artworkKind={state === 'new' ? 'still' : 'backdrop'} cropOrigin="55% 75%" model={cardText(episodeSubject, state === 'mixed' ? 'search' : 'in-show', {t})} length={cardText(episodeSubject, 'in-show', {t}).detail.find(part => part.id === 'duration')?.text} airDate={state === 'date' || state === 'absolute' || state === 'special' ? undefined : fact('air-date', t('next.cards.fixture.episodeDate'))} description={t('next.cards.fixture.overview')} status={state === 'progress' ? [fact('up-next', t('next.cards.fixture.upNext'), 'status')] : undefined} expanded={state === 'expanded'} playing={state === 'playing'} playback={state === 'playing' ? [fact('playing', t('next.browse.playing'), 'status')] : undefined} />; break;
    case 'collection-banner': content = <CollectionBanner {...common} model={collection} artwork={source(paths.collection)} position="60% 30%" covers={posters} kindLabel={t('next.cards.fixture.collectionKind')} description={t('next.cards.fixture.collectionDescription')} actionLabel={t('next.cards.openCollection')} actionVariant="glass" onAction={actions.onNavigate} />; break;
    case 'collection-tile': content = <CollectionTile {...common} model={collection} artwork={source(paths.collection)} covers={posters} />; break;
    case 'genre-video': case 'genre-music': content = <GenreTile {...common} variant={state === 'poster' ? 'poster' : kind === 'genre-video' ? 'video' : 'music'} artwork={source(paths.backdrop)} model={{title: t(state === 'poster' ? 'next.cards.fixture.genrePhone' : kind === 'genre-video' ? 'next.cards.fixture.genreVideo' : 'next.cards.fixture.genreMusic'), caption: [fact('count', t(kind === 'genre-video' ? 'next.cards.fixture.videoCount' : 'next.cards.fixture.genreCount'))]}} covers={state === 'poster' ? posters.slice(0, 2) : covers.slice(0, 3)} tint={state === 'poster' ? referenceColours.genrePhone : referenceColours.genre} />; break;
    case 'ranked': content = <RankedCard {...common} rank={state === 'long' ? 10 : 1} />; break;
    case 'library-card': case 'library-row': {const Component = kind === 'library-card' ? LibraryCard : LibraryRow; content = <Component {...common} model={{title: t(state === 'audio' ? 'next.cards.fixture.musicLibrary' : 'next.cards.fixture.library'), caption: [fact('count', t(state === 'audio' ? 'next.cards.fixture.musicCount' : 'next.cards.fixture.libraryCount'))]}} covers={state === 'audio' ? covers : posters} shape={state === 'audio' ? 'square' : 'poster'} palette={referenceColours.albumGlow} />; break;}
    case 'cover': content = <CoverCard {...common} model={read} artwork={state === 'generated' ? undefined : readSource} generated={state === 'generated' ? generated : undefined} kind={state === 'magazine' ? 'magazine' : state === 'newspaper' ? 'newspaper' : 'book'} />; break;
    case 'read-resume': content = <ReadResumeCard {...common} model={read} artwork={state === 'generated' ? undefined : readSource} generated={state === 'generated' ? <GeneratedCover {...generated} /> : undefined} location={location} remaining={remaining} kind={state === 'magazine' ? 'magazine' : 'book'} palette={referenceColours.coverGlow} />; break;
    case 'resume': content = <ResumeCard {...common} model={book} artwork={source(paths.book)} location={book.detail.find(part => part.id === 'chapter')} remaining={book.detail.find(part => part.id === 'remaining')} palette={referenceColours.albumGlow} />; break;
    case 'series': content = <SeriesTile {...common} model={{title: t(state === 'long' ? 'next.cards.fixture.longTitle' : 'next.cards.fixture.series'), caption: [fact('count', t('next.cards.fixture.seriesCount')), fact('status', t('next.cards.fixture.seriesStatus'))]}} covers={state === 'generated' ? [posters[0], {id: 'generated-cover', generated}, posters[2]].filter((cover): cover is NonNullable<typeof cover> => !!cover) : posters.slice(0, count === 1 ? 3 : count)} segments={Array.from({length: 6}, (_, index) => ({id: String(index), state: index < 2 ? 'read' : index === 2 ? 'in-progress' : 'unread', progress: .4}))} tint={referenceColours.series} />; break;
    case 'quick': content = <QuickTile {...common} model={state === 'long' ? {...album, title: t('next.cards.fixture.longTitle')} : album} artwork={source(paths.album)} playing={state === 'playing'} onPause={actions.onPause} />; break;
    case 'playlist': content = state === 'create' ? <NewPlaylistCard model={{title: t('next.cards.fixture.newPlaylist'), caption: [fact('hint', t('next.cards.fixture.newPlaylistHint'))]}} onCreate={actions.onNavigate} /> : <PlaylistMosaic {...common} model={{title: t('next.cards.fixture.playlist'), caption: [fact('count', t('next.cards.fixture.playlistCount')), fact('owner', t('next.cards.fixture.playlistOwner'))]}} covers={covers} />; break;
    case 'session': content = <SessionCard {...common} model={state === 'channel' ? {title: t('next.cards.fixture.sessionProgramme'), caption: [fact('channel', t('next.cards.fixture.sessionChannel'), 'identity'), fact('ends', t('next.cards.fixture.sessionEnds'))], progress: .5} : state === 'audio' ? cardText({id: 'reference-song', kind: 'song', title: t('next.cards.fixture.sessionSong'), artist: t('next.cards.fixture.sessionArtist'), album: t('next.cards.fixture.sessionAlbum'), durationSeconds: 260, positionSeconds: 185}, 'song-row', {t}) : {...cardText(episodeSubject, 'continuation', {t}), progress: .42}} artwork={source(state === 'audio' ? 'sq-abbey.jpg' : paths.backdrop)} position="40% 30%" viewer={{name: t(state === 'channel' ? 'next.cards.fixture.sessionViewer' : 'next.cards.fixture.viewer'), device: t(state === 'channel' ? 'next.cards.fixture.sessionDevice' : 'next.cards.fixture.device'), avatar: <ProfileFace initial={t(state === 'channel' ? 'next.cards.fixture.sessionViewerInitial' : 'next.cards.fixture.viewerInitial')} size={22} art={state === 'channel' ? 'gold' : 'mint'} tint={state === 'channel' ? referenceColours.sessionChannelFace : referenceColours.sessionFace} className={styles.sessionFace} />}} facts={[fact('network', t('next.cards.fixture.network')), fact('delivery', t(state === 'channel' ? 'next.cards.fixture.sessionDelivery' : 'next.cards.fixture.delivery'))]} paused={state === 'paused'} {...(state === 'channel' ? {media: 'channel' as const, channel: {mark: {sourceId: 'reference-source', id: 'reference-session-channel', text: t('next.cards.fixture.sessionChannelMark'), seed: 'reference-source:reference-session-channel'}, tint: referenceColours.channel, facts: [fact('kind', t('next.cards.fixture.sessionLive')), fact('number', t('next.cards.fixture.sessionChannelNumber'))]}} : {media: state === 'audio' ? 'audio' as const : 'video' as const})} onStop={actions.onStop} />; break;
    case 'not-interested': content = <NotInterestedButton onClick={actions.onNotInterested} />; break;
  }
  return <div data-card-scenario={kind} data-card-state={state} className={[styles.specimen, needsInteractionMargin(kind, state) && styles.specimenInset, kind === 'not-interested' && styles.notInterestedSpecimen].filter(Boolean).join(' ')}>{content}<output className={styles.actionOutput} data-card-action={actions.action} data-card-events={JSON.stringify(actions.events)} data-primary-ref={!!primaryRef.current} aria-live="polite">{actions.action}</output></div>;
}

const definitions: readonly [FamilyKind, MessageId, number, number, readonly ScenarioState[]][] = [
  ['poster', 'next.cards.case.poster', 190, 340, ['default', 'hover', 'focus', 'pressed', 'new', 'watched', 'progress', 'long', 'empty', 'loading', 'error']],
  ['wide', 'next.cards.case.wide', 316, 215, ['default', 'hover', 'focus', 'progress', 'long', 'empty', 'error']],
  ['square', 'next.cards.case.square', 190, 235, ['default', 'hover', 'focus', 'audio', 'watched', 'progress', 'empty', 'error']],
  ['circle', 'next.cards.case.circle', 172, 230, ['default', 'two', 'three', 'four', 'mixed', 'person-covers', 'focus', 'empty', 'error']],
  ['live', 'next.cards.case.live', 316, 178, ['default', 'hover', 'focus', 'new', 'playing', 'four', 'empty', 'future']],
  ['episode', 'next.cards.case.episode', 316, 330, ['default', 'hover', 'focus', 'watched', 'progress', 'expanded', 'date', 'absolute', 'special', 'mixed', 'new', 'empty', 'error', 'playing']],
  ['collection-banner', 'next.cards.case.collection', 1312, 300, ['default', 'hover', 'focus', 'empty', 'error']],
  ['collection-tile', 'next.cards.case.collection', 422, 238, ['default', 'hover', 'focus', 'progress', 'empty', 'error']],
  ['genre-video', 'next.cards.case.genre', 316, 132, ['default', 'hover', 'focus', 'empty', 'error', 'poster']],
  ['genre-music', 'next.cards.case.genre', 250, 152, ['default', 'hover', 'focus', 'empty', 'error']],
  ['ranked', 'next.cards.case.ranked', 262, 305, ['default', 'hover', 'focus', 'long']],
  ['library-card', 'next.cards.case.library', 350, 108, ['default', 'audio', 'focus', 'empty', 'error']],
  ['library-row', 'next.cards.case.library', 350, 108, ['default', 'audio', 'focus', 'empty', 'error']],
  ['cover', 'next.cards.case.cover', 190, 340, ['default', 'generated', 'magazine', 'newspaper', 'new', 'watched', 'progress', 'next', 'focus', 'empty', 'error']],
  ['read-resume', 'next.cards.case.readResume', 440, 204, ['default', 'generated', 'magazine', 'hover', 'focus', 'empty', 'error']],
  ['resume', 'next.cards.case.resume', 456, 180, ['default', 'hover', 'focus', 'empty', 'error']],
  ['series', 'next.cards.case.series', 300, 196, ['default', 'hover', 'focus', 'generated', 'two', 'long', 'empty', 'error']],
  ['quick', 'next.cards.case.quick', 319, 76, ['default', 'hover', 'focus', 'playing', 'long', 'empty', 'error']],
  ['playlist', 'next.cards.case.playlist', 190, 235, ['default', 'create', 'hover', 'focus', 'empty', 'error']],
  ['session', 'next.cards.case.session', 488, 160, ['default', 'hover', 'focus', 'paused', 'audio', 'channel', 'empty', 'error']],
  ['not-interested', 'next.cards.case.notInterested', 200, 140, ['default', 'hover', 'focus']],
];
const cases: GalleryCase[] = definitions.flatMap(([kind, labelKey, width, height, states]) => states.map(state => {
  const Render: ComponentType = function CardScenario() { return <Scenario kind={kind} state={state} />; };
  const margin = needsInteractionMargin(kind, state) ? 64 : 0;
  return {id: `${kind}-${state}`, labelKey, width: width + margin, height: height + margin, render: Render};
}));
cases.push({id: 'resume-real-ambient', labelKey: 'next.cards.case.resume', width: 456, height: 180, render: RealAmbientCase});
cases.push({id: 'reference', labelKey: 'next.cards.family', width: 1440, height: 900, render: ReferenceCase});
const contexts: readonly [string, FamilyKind, ScenarioState, NonNullable<CardProps['context']>, MessageId, number, number][] = [
  ['poster-search', 'poster', 'default', 'search', 'next.cards.case.poster', 174, 310],
  ['poster-grid-long', 'poster', 'long', 'grid', 'next.cards.case.poster', 171, 320],
  ['square-search', 'square', 'default', 'search', 'next.cards.case.square', 150, 200],
  ['circle-search-artist', 'circle', 'default', 'search', 'next.cards.case.circle', 124, 170],
  ['circle-search-person', 'circle', 'mixed', 'search', 'next.cards.case.circle', 124, 170],
  ['circle-detail-person', 'circle', 'mixed', 'detail', 'next.cards.case.circle', 120, 155],
];
contexts.forEach(([id, kind, state, context, labelKey, width, height]) => {
  const Render: ComponentType = function CardContextScenario() { return <Scenario kind={kind} state={state} context={context} />; };
  cases.push({id, labelKey, width, height, render: Render});
});
definitions.filter(([kind]) => kind !== 'not-interested').forEach(([kind, labelKey, width, height]) => {
  for (const state of ['selection-entry', 'selected', 'selecting', 'hold', 'disabled'] as const) {
    const Render = () => <Scenario kind={kind} state={state} />;
    const margin = needsInteractionMargin(kind, state) ? 64 : 0;
    cases.push({id: `${kind}-${state}`, labelKey, width: width + margin, height: height + margin, render: Render});
  }
});
export default defineGalleryFamily({id: 'cards', labelKey: 'next.cards.family', cases});
