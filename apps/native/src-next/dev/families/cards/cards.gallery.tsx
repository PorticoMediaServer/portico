import React, {useState, type ReactNode} from 'react';
import {Pressable, Text, View} from 'react-native';
import type {Gallery} from '../../gallery-registry';
import {cardText, episodeLabel, sessionFactParts, titleFactParts, type FactPart} from '../../../../../../packages/client-core/src/presentation/kit-model/index';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {cardsFixtureText as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/cards-fixtures';
import {GeneratedCover, ProfileFace, type ArtworkSource} from '../../../kit/artwork/index';
import {CircleCard, CollectionBanner, CollectionTile, CoverCard, EpisodeTile, GenreTile, LibraryCard, LibraryRow, LiveCard, NotInterestedButton, PlaylistMosaic, PosterCard, QuickTile, RankedCard, ReadResumeCard, ResumeCard, SeriesTile, SessionCard, SquareCard, WideCard, type CardProps} from '../../../kit/cards/index';
import {useTheme} from '../../../kit/theme';

// Reference media belongs only to this __DEV__ gallery. Production cards never compose fixtures.
const referenceRoot = 'http://100.120.50.24:8787/';
const artwork = (path: string): ArtworkSource => ({uri: `${referenceRoot}${path}`, cacheKey: `cards-reference:${path}`});
const toy = artwork('assets/pool/movies/toy-story.jpg');
const inside = artwork('assets/pool/movies/inside-out.jpg');
const dune = artwork('assets/pool/movies/dune-part-two.jpg');
const backdrop = artwork('assets/bd-tlou.jpg');
const album = artwork('assets/pool/albums/after-hours.jpg');
const audiobook = artwork('assets/pool/books/the-martian-unabridged.jpg');
const albums = [album, artwork('assets/pool/albums/damn.jpg'), artwork('assets/pool/albums/random-access-memories.jpg'), artwork('assets/pool/albums/thriller.jpg')] as const;
const readCover = artwork('assets/read/covers/great-gatsby.jpg');
const magazine = artwork('assets/read/magazines/amazing-1926-04.jpg');
const newspaper = artwork('assets/read/newspapers/nyt-1918-10-21.jpg');
const covers = [toy, inside, dune] as const;
const seriesCovers = ['dune', 'dune-messiah', 'children-of-dune'].map(name => artwork(`assets/read/covers/${name}.jpg`));
const sessionAlbum = artwork('assets/pool/albums/abbey-road-2019-mix.jpg');
const env = {t: defaultI18n.t};
const episode = cardText({id: 'tlou-e3', kind: 'episode', title: fixture('next.cards.fixture.episode'), show: {id: 'tlou', title: fixture('next.cards.fixture.show')}, episode: {mode: 'seasonal', season: 1, episode: 3}, durationSeconds: 81 * 60, positionSeconds: 17 * 60}, 'continuation', env);
const movie = cardText({id: 'inside-out', kind: 'movie', title: fixture('next.cards.fixture.insideOut'), year: 2015, durationSeconds: 95 * 60, positionSeconds: 9 * 60}, 'continuation', env);
const book = cardText({id: 'martian', kind: 'book', title: fixture('next.cards.fixture.book'), author: fixture('next.cards.fixture.author'), chapterNumber: 15, durationSeconds: 10 * 3600 + 52 * 60, positionSeconds: 6 * 3600 + 18 * 60}, 'continuation', env);
const length = (seconds: number) => titleFactParts({durationSeconds: seconds}, env)[0]?.text;
const fact = (id: string, text: string, role: FactPart['role'] = 'supplement'): FactPart => ({id, text, role});

type GalleryActions = {onPause: () => void; onStop: () => void; onDownload: () => void; isTV: boolean};
type Scenario = {id: string; platform?: 'read' | 'handheld'; render: (props: CardProps, actions: GalleryActions) => ReactNode; selected?: boolean};
function Specimen({scenario}: {scenario: Scenario}) {
  const t = useTheme(), [active, setActive] = useState(!!scenario.selected), [selected, setSelected] = useState(!!scenario.selected), [event, setEvent] = useState<string>();
  const props: CardProps = {
    title: fixture('next.cards.fixture.collection'), testID: `card-${scenario.id}`, hasTVPreferredFocus: t.isTV,
    onNavigate: () => setEvent(fixture('next.cards.fixture.actionNavigate')),
    onPlay: () => setEvent(fixture('next.cards.fixture.actionPlay')),
    onMore: () => setEvent(fixture('next.cards.fixture.actionMore')),
    selection: {active, selected, onToggle: () => {setSelected(value => !value); setEvent(fixture('next.cards.fixture.actionSelect'));}, onEnter: () => {setActive(true); setSelected(true); setEvent(fixture('next.cards.fixture.actionSelect'));}},
  };
  return <View style={{padding: t.isTV ? 96 : 20, gap: 24, backgroundColor: t.colors.ink}}>
    {scenario.render(props, {
      onPause: () => setEvent(fixture('next.cards.fixture.actionPause')),
      onStop: () => setEvent(fixture('next.cards.fixture.actionStop')),
      onDownload: () => setEvent(defaultI18n.t('next.cards.download')),
      isTV: t.isTV,
    })}
    {event && <Text testID="cards-last-action" style={[t.type.body, {color: t.colors.text}]}>{event}</Text>}
    {active && <Pressable accessibilityRole="button" accessibilityLabel={defaultI18n.t('action.cancel')} onPress={() => {setActive(false); setSelected(false);}}><Text style={[t.type.bodyStrong, {color: t.colors.accentStrong}]}>{defaultI18n.t('action.cancel')}</Text></Pressable>}
  </View>;
}

const scenarios: readonly Scenario[] = [
  {id: 'poster', render: p => <PosterCard {...p} artwork={toy} caption={titleFactParts({year: 1995}, env)} />},
  {id: 'poster-new', render: p => <PosterCard {...p} artwork={toy} isNew caption={titleFactParts({year: 1995}, env)} />},
  {id: 'poster-watched', render: p => <PosterCard {...p} artwork={toy} watched />},
  {id: 'poster-progress', render: p => <PosterCard {...p} artwork={toy} progress={0.38} />},
  {id: 'poster-selected', selected: true, render: p => <PosterCard {...p} artwork={toy} />},
  {id: 'poster-grid', render: (p, a) => <PosterCard {...p} width={a.isTV ? 200 : 108} density="grid" artwork={toy} caption={titleFactParts({year: 1995}, env)} />},
  {id: 'poster-long', render: p => <PosterCard {...p} artwork={dune} title={fixture('next.cards.fixture.longTitle')} titleLines={2} />},
  {id: 'poster-missing', render: p => <PosterCard {...p} />},
  {id: 'poster-error', render: p => <PosterCard {...p} artwork={artwork('assets/missing-card-reference.jpg')} />},
  {id: 'poster-offscreen', render: p => <PosterCard {...p} artwork={toy} visible={false} />},
  {id: 'wide-episode', render: p => <WideCard {...p} {...episode} artwork={backdrop} primaryAction="play" onPlay={p.onPlay!} />},
  {id: 'wide-film', render: p => <WideCard {...p} {...movie} artwork={inside} primaryAction="play" onPlay={p.onPlay!} />},
  {id: 'wide-selected', selected: true, render: p => <WideCard {...p} {...episode} artwork={backdrop} />},
  {id: 'square', render: p => <SquareCard {...p} title={fixture('next.cards.fixture.album')} artwork={album} caption={[fact('artist', fixture('next.cards.fixture.artist'), 'identity'), ...titleFactParts({year: 2020}, env)]} />},
  {id: 'square-progress', render: p => <SquareCard {...p} {...book} artwork={audiobook} progress={0.58} />},
  {id: 'circle-one', render: p => <CircleCard {...p} title={fixture('next.cards.fixture.artist')} art={{covers: [album]}} />},
  {id: 'circle-two', render: p => <CircleCard {...p} title={fixture('next.cards.fixture.artist')} art={{covers: albums.slice(0, 2)}} />},
  {id: 'circle-three', render: p => <CircleCard {...p} title={fixture('next.cards.fixture.artist')} art={{covers: albums.slice(0, 3)}} />},
  {id: 'circle-four', render: p => <CircleCard {...p} title={fixture('next.cards.fixture.artist')} art={{covers: albums}} />},
  {id: 'circle-person', render: p => <CircleCard {...p} title={fixture('next.cards.fixture.person')} person art={{initials: fixture('next.cards.fixture.personInitials'), covers: [dune, toy]}} />},
  ...(['seasonal', 'date', 'absolute', 'special'] as const).map(mode => ({id: `episode-${mode}`, render: (p: CardProps) => {
    const numbering = mode === 'seasonal' ? {mode, season: 1, episode: 3} : mode === 'date' ? {mode, day: '2026-10-21'} : mode === 'absolute' ? {mode, number: 37} : {mode};
    const label = episodeLabel(numbering, 'in-show', env);
    return <EpisodeTile {...p} title={fixture('next.cards.fixture.episode')} artwork={backdrop} backdropFallback label={label ? [label] : []} length={length(76 * 60)} />;
  }})),
  {id: 'episode-still', render: p => <EpisodeTile {...p} title={fixture('next.cards.fixture.episode')} artwork={backdrop} label={[fact('episode-label', defaultI18n.t('next.kit.episode', {number: 3}), 'context')]} length={length(76 * 60)} />},
  {id: 'episode-progress', render: p => <EpisodeTile {...p} title={episode.title} artwork={backdrop} backdropFallback label={episode.caption} progress={0.67} length={length(81 * 60)} />},
  {id: 'episode-download', render: (p, a) => <EpisodeTile {...p} title={episode.title} artwork={backdrop} backdropFallback label={episode.caption} status={fixture('next.cards.fixture.upNext')} length={length(81 * 60)} synopsis={fixture('next.cards.fixture.overview')} onDownload={a.onDownload} />},
  {id: 'episode-mixed-show', render: p => <EpisodeTile {...p} title={episode.title} artwork={backdrop} backdropFallback label={cardText({id: 'mixed-tlou', kind: 'episode', title: episode.title, show: {title: fixture('next.cards.fixture.show')}, episode: {mode: 'seasonal', season: 1, episode: 3}}, 'search', env).caption} />},
  {id: 'episode-watched', render: p => <EpisodeTile {...p} title={fixture('next.cards.fixture.infected')} artwork={backdrop} backdropFallback label={[fact('episode-label', defaultI18n.t('next.kit.episode', {number: 2}), 'context')]} watched />},
  {id: 'live', render: p => <LiveCard {...p} title={fixture('next.cards.fixture.programme')} channel={fixture('next.cards.fixture.channel')} number={fixture('next.cards.fixture.sessionChannelNumber')} mark={{sourceId: 'northwind', id: 'news24', text: fixture('next.cards.fixture.channelMark'), seed: 'northwind:news24'}} flags={[fact('live', defaultI18n.t('next.kit.live'), 'status')]} range={fixture('next.cards.fixture.timeRange')} progress={0.58} />},
  {id: 'collection-tile', render: p => <CollectionTile {...p} title={fixture('next.cards.fixture.collection')} covers={covers} backdrop={inside} caption={[fact('count', fixture('next.cards.fixture.collectionFacts')), fact('years', fixture('next.cards.fixture.collectionYears'))]} detail={[fact('watched', fixture('next.cards.fixture.collectionWatched'), 'status')]} progress={0.5} />},
  {id: 'collection-banner', render: p => <CollectionBanner {...p} title={fixture('next.cards.fixture.collection')} covers={covers} backdrop={inside} />},
  {id: 'collection-link', platform: 'handheld', render: p => <CollectionTile {...p} layout="link" title={fixture('next.cards.fixture.collection')} kindLabel={fixture('next.cards.fixture.collectionKind')} covers={covers.slice(0, 2)} caption={[fact('count', fixture('next.cards.fixture.collectionFacts'))]} />},
  {id: 'genre-video', render: p => <GenreTile {...p} title={fixture('next.cards.fixture.genreVideo')} variant="video" covers={covers} backdrop={inside} />},
  {id: 'genre-music', render: p => <GenreTile {...p} title={fixture('next.cards.fixture.genreMusic')} variant="music" covers={albums.slice(0, 3)} />},
  {id: 'ranked', render: p => <RankedCard {...p} artwork={toy} rank={1} />},
  {id: 'library-poster', render: p => <LibraryCard {...p} title={fixture('next.cards.fixture.library')} covers={covers} shape="poster" />},
  {id: 'library-square', render: p => <LibraryRow {...p} title={fixture('next.cards.fixture.musicLibrary')} covers={albums.slice(0, 3)} shape="square" />},
  {id: 'library-empty', render: p => <LibraryRow {...p} title={fixture('next.cards.fixture.library')} covers={[]} shape="poster" />},
  {id: 'cover-generated', platform: 'read', render: p => <CoverCard {...p} title={fixture('next.cards.fixture.generatedBook')} artworkFallback={<GeneratedCover title={fixture('next.cards.fixture.generatedBook')} author={fixture('next.cards.fixture.generatedAuthor')} glyph={fixture('next.cards.fixture.generatedGlyph')} tint={{first: '#a8892a', second: '#5a3a1a'}} />} />},
  {id: 'cover-book', platform: 'read', render: p => <CoverCard {...p} title={fixture('next.cards.fixture.readBook')} artwork={readCover} progress={0.27} />},
  {id: 'cover-periodical', platform: 'read', render: p => <CoverCard {...p} title={fixture('next.cards.fixture.magazine')} artwork={magazine} kind="periodical" />},
  {id: 'cover-newspaper', platform: 'read', render: p => <CoverCard {...p} title={fixture('next.cards.fixture.newspaper')} artwork={newspaper} kind="newspaper" watched />},
  {id: 'read-resume', platform: 'read', render: p => <ReadResumeCard {...p} title={fixture('next.cards.fixture.generatedBook')} caption={[fact('author', fixture('next.cards.fixture.generatedAuthor'), 'identity')]} detail={[fact('chapter', defaultI18n.t('next.kit.chapter', {number: 4}), 'context'), fact('remaining', fixture('next.cards.fixture.generatedReadRemaining'))]} progress={0.26} palette={{first: '#a8892a', second: '#5a3a1a'}} artworkFallback={<GeneratedCover title={fixture('next.cards.fixture.generatedBook')} author={fixture('next.cards.fixture.generatedAuthor')} glyph={fixture('next.cards.fixture.generatedGlyph')} fontScale={15.5} tint={{first: '#a8892a', second: '#5a3a1a'}} />} />},
  {id: 'series', platform: 'read', render: p => <SeriesTile {...p} title={fixture('next.cards.fixture.series')} covers={seriesCovers} caption={[fact('count', fixture('next.cards.fixture.seriesCount')), fact('status', fixture('next.cards.fixture.seriesStatus'), 'status')]} segments={[{id: '1', progress: 0.27}, {id: '2'}, {id: '3'}, {id: '4'}, {id: '5'}, {id: '6'}]} />},
  {id: 'resume', render: p => <ResumeCard {...p} {...book} artwork={audiobook} primaryAction="play" onPlay={p.onPlay!} />},
  {id: 'quick-rest', render: p => <QuickTile {...p} playing={false} onPause={undefined} title={fixture('next.cards.fixture.album')} artwork={album} caption={[fact('artist', fixture('next.cards.fixture.artist'), 'identity')]} />},
  {id: 'quick-playing', render: (p, a) => <QuickTile {...p} title={fixture('next.cards.fixture.album')} artwork={album} playing onPause={a.onPause} caption={[fact('artist', fixture('next.cards.fixture.artist'), 'identity')]} />},
  {id: 'playlist', render: p => <PlaylistMosaic {...p} title={fixture('next.cards.fixture.familyNight')} covers={[toy, inside, dune, album]} />},
  {id: 'playlist-create', render: p => <PlaylistMosaic {...p} title={defaultI18n.t('web.saved.newPlaylist')} covers={[]} create />},
  ...([false, true] as const).map(paused => ({id: paused ? 'session-paused' : 'session', render: (p: CardProps, a: GalleryActions) => <SessionCard {...p} title={fixture('next.cards.fixture.show')} artwork={backdrop} artworkProps={{position: {x: 0.4, y: 0.3}}} identity={[fact('episode-code', defaultI18n.t('next.kit.episodeCode', {season: 1, episode: 3}), 'context'), fact('episode-title', fixture('next.cards.fixture.episode'), 'identity')]} facts={sessionFactParts({kind: 'vod', profile: fixture('next.cards.fixture.viewer'), device: fixture('next.cards.fixture.device'), state: paused ? 'paused' : 'playing', positionMs: 1915000, durationMs: 4560000, location: 'local', decision: {video: {action: 'direct'}}}, env)} viewerBadge={<ProfileFace initial={fixture('next.cards.fixture.viewerInitial')} size={22} />} onStop={a.onStop} paused={paused} progress={0.42} />})),
  {id: 'session-audio', render: (p, a) => <SessionCard {...p} kind="audio" title={fixture('next.cards.fixture.sessionSong')} artwork={sessionAlbum} palette={{first: '#697275', second: '#243432'}} identity={[fact('artist', fixture('next.cards.fixture.sessionArtist'), 'identity'), fact('album', fixture('next.cards.fixture.sessionAlbum'), 'context')]} facts={sessionFactParts({kind: 'audio', profile: fixture('next.cards.fixture.demo'), device: fixture('next.cards.fixture.sessionAudioDevice'), positionMs: 184000, durationMs: 259000, location: 'local', decision: {audio: {action: 'direct'}}}, env)} viewerBadge={<ProfileFace initial={fixture('next.cards.fixture.demoInitial')} size={22} />} onStop={a.onStop} progress={0.71} />},
  {id: 'session-channel', render: (p, a) => <SessionCard {...p} kind="channel" title={fixture('next.cards.fixture.sessionProgramme')} channelMark={{sourceId: 'northwind', id: 'northwind-news', text: fixture('next.cards.fixture.sessionChannelMark'), seed: 'northwind:northwind-news'}} channelName={fixture('next.cards.fixture.sessionLive')} channelNumber={fixture('next.cards.fixture.sessionChannelNumber')} palette={{first: '#7a3b3b', second: '#132838'}} identity={[fact('channel', fixture('next.cards.fixture.sessionChannel'), 'identity'), fact('ends', fixture('next.cards.fixture.sessionEnds'), 'context')]} facts={sessionFactParts({kind: 'channel', profile: fixture('next.cards.fixture.sessionViewer'), device: fixture('next.cards.fixture.sessionDevice'), startedAt: '2026-10-21T19:52:00Z', location: 'local', decision: {video: {action: 'copy'}}}, {...env, timeZone: 'America/New_York', hourCycle: 'h12'})} viewerBadge={<ProfileFace initial={fixture('next.cards.fixture.sessionViewerInitial')} size={22} />} onStop={a.onStop} progress={0.5} />},
  {id: 'not-interested', render: p => <NotInterestedButton title={p.title} onPress={p.onNavigate!} />},
];

export default scenarios.map(scenario => ({
  id: `cards-${scenario.id}`, title: 'next.cards.native.galleryTitle',
  render: function CardsGalleryCase() {return <Specimen scenario={scenario} />;},
  platforms: scenario.platform ? ['phone', 'tablet'] : ['phone', 'tablet', 'tv'],
})) satisfies readonly Gallery[];
