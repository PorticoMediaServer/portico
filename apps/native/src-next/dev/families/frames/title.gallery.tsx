import React, {useEffect, useMemo, useRef, useState, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {titleFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/title-fixtures';
import {AmbientArt, Artwork, PersonCircle, type ArtworkSource} from '../../../kit/artwork/index';
import {EpisodeTile, PosterCard, SquareCard} from '../../../kit/cards';
import {Button, Chip} from '../../../kit/controls';
import {SongRow, TrackRow, type TrackRowData, type TrackSelection} from '../../../kit/browse/Tracks';
import {AboutTable, CastRow, ChoiceChips, EpisodeList, musicScroller, posterGridScroller, PosterWindowRow, titleRowsScroller, type EpisodePlateProps, EpisodeDisplay, EpisodeNeighbour, NumberedRow, OwnerCard, PanelBlock, PanelHeading, PeopleSheet, SheetFrame, TVEpisodeShelf, TVSeasonList, type EpisodeBandData, type TVEpisodeItem, TitleDetails, TitleFrame, TitleSection, TrackPicker, VersionRows, type CastPerson, type TitleFrameProps} from '../../../kit/frames';
import {fileInfoModel, type FileFacts} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {NativeFileInfoContent} from '../../../screens/watch/FileInfoContent';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {HeroMetadata, PosterRow, SeasonBar} from '../../../kit/layout';
import {SeasonSheet} from '../../../kit/layout/SeasonSheet';
import {Menu} from '../../../kit/overlays';
import {SelectionBar} from '../../../kit/selection';
import {useTheme} from '../../../kit/theme';
import {Page} from './frames.gallery';
import type {Gallery} from '../../gallery-registry';
import {referenceOrigin} from '../../reference-art';
import {createWindowedCollection} from '../../../../../../packages/client-core/src/collections/windowed';

/** Family C, the title page's forms: every kind of title page filled in full on the one `TitleFrame`, from the parts its
 * tab panel and side are built from. The same cases as the web gallery (`web/src/dev/families/frames-title.tsx`).
 * Development builds only. */
type Name = keyof typeof titleFixtureMessages extends `next.title.fixture.${infer N}` ? N : never;
const f = (name: Name) => formatMessage(titleFixtureMessages[`next.title.fixture.${name}`]);
const none = () => {};
const asset = (path: string): ArtworkSource => ({uri: `${referenceOrigin}/assets/${path}`, cacheKey: `frames-reference:${path}`});
/** Facts as the server sends them: the first is the identity; a rating and a season count are outlined, a runtime on a title page is not (phone-detail d1). */
function Meta({text, tags = []}: {text: string; tags?: string[]}) {
  // The frame centres the facts on a stacked head (HeroFactsCentred): the gallery, like a page, passes facts only.
  return <HeroMetadata facts={[...text.split(' · ').map((part, index) => ({id: index === 0 ? 'year' : `fact-${index}`, text: part, role: index === 0 ? 'identity' as const : 'context' as const})),
    ...tags.map(tag => ({id: / seasons?$/.test(tag) ? 'season-count' : 'content-rating', text: tag, role: 'context' as const}))]}/>;
}
const back = {label: f('back'), onPress: none}, playOn = {icon: 'i-cast' as const, label: f('playOn'), onPress: none};
const link = (text: string) => ({text, onPress: none});

function Posters({title, items, small}: {title: string; items: readonly (readonly [string, string, string])[]; small?: boolean}) {
  return <PosterRow title={title} small={small} items={items.map(item => ({key: item[0], title: item[1], year: item[2], artwork: asset(item[0]), onNavigate: none}))}/>;
}
const moreLikeDune = [['pool/movies/dune.jpg', 'Dune', '2021'], ['pool/movies/blade-runner.jpg', 'Blade Runner', '1982'], ['pool/movies/interstellar.jpg', 'Interstellar', '2014'], ['pool/movies/star-wars.jpg', 'Star Wars', '1977'],
  ['pool/movies/avatar.jpg', 'Avatar', '2009'], ['pool/movies/the-empire-strikes-back.jpg', 'The Empire Strikes Back', '1980'], ['pool/movies/the-matrix.jpg', 'The Matrix', '1999']] as const;
const moreLikeInsideOut = [['pool/movies/toy-story.jpg', 'Toy Story', '1995'], ['pool/movies/monsters-inc.jpg', 'Monsters, Inc.', '2001'], ['pool/movies/incredibles-2.jpg', 'Incredibles 2', '2018'],
  ['pool/movies/spirited-away.jpg', 'Spirited Away', '2001'], ['pool/movies/how-to-train-your-dragon.jpg', 'How to Train Your Dragon', '2010'], ['pool/movies/moana.jpg', 'Moana', '2016']] as const;
const tabs = (value: string, onValueChange: (value: string) => void, show = false) => ({value, onValueChange, items: [
  ...(show ? [{value: 'episodes', label: f('episodes')}] : []), {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]});

/** A film: the collection kicker, the facts, Play with My List, Watched, Download and More, the picker row and the tabs. */
function filmFrame(tab: string, setTab: (value: string) => void, tv = false): Omit<TitleFrameProps, 'children'> {
  return {back, topAction: playOn, art: <AmbientArt treatment="title" foreground={asset('pool/movies/dune-part-two--bd.jpg')}/>,
    kicker: {icon: 'i-layers', label: f('duneCollection'), detail: f('duneCollectionCount'), onPress: none}, title: f('dune2'), metadata: <Meta text={f('dune2Facts')} tags={[f('dune2Rating')]}/>,
    primary: {icon: 'i-play', label: f('play'), onPress: none},
    actions: [{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), labelled: true, wide: true, onPress: none}, ...(tv ? [] : [{icon: 'i-dl' as const, label: f('download'), labelled: true, onPress: none}]), {icon: 'i-more', label: f('more'), onPress: none}],
    tagline: f('dune2Tagline'), synopsis: f('dune2Synopsis'),
    credits: [{label: f('directedBy'), names: [link(f('dune2Director'))]}, {label: f('starring'), names: (['dune2Cast1', 'dune2Cast2', 'dune2Cast3', 'dune2Cast4'] as const).map(name => link(f(name)))}],
    picker: <TrackPicker audio={f('audioChoice')} subtitles={f('subtitlesChoice')} label={f('picker')} onPress={none}/>, tabs: tabs(tab, setTab)};
}
function Film({more = false, selecting = false}: {more?: boolean; selecting?: boolean}) {
  const [tab, setTab] = useState('more'), [open, setOpen] = useState(more), {isTV} = useTheme();
  // Two titles of More like this selected (Select in a card's menu): the page's selection bar at the top of the panel.
  const bar = selecting ? <SelectionBar count={2} selectionLabel={defaultI18n.t('next.selection.movie', {count: 2})} actions={[]} menu={null} onMenuChange={none} onAction={none} onCancel={none}/> : undefined;
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-film" {...filmFrame(tab, setTab, isTV)} selectionBar={bar}>
    <TitleSection tab="more"><Posters title={f('moreLikeDune')} items={moreLikeDune}/></TitleSection>
    {/* TV draws every section, and focus walks down them into Details. */}
    {isTV && <TitleSection tab="details"><DuneDetails/></TitleSection>}
  </TitleFrame>
  {more && <Menu open={open} onOpenChange={setOpen} title={f('dune2')} items={[
    {id: 'fav', icon: 'i-heart', label: f('menuFavourite'), onSelect: none}, {id: 'rate', icon: 'i-star', label: f('menuRate'), onSelect: none}, {id: 'playlist', icon: 'i-listadd', label: f('menuPlaylist'), onSelect: none},
    {id: 'sep', separator: true}, {id: 'version', icon: 'i-film', label: f('menuVersion'), onSelect: none}, {id: 'info', icon: 'i-info', label: f('menuFileInfo'), onSelect: none},
    {id: 'owner', section: true, label: f('ownerOnly'), tone: 'owner'}, {id: 'edit', icon: 'i-edit', label: f('menuEdit'), onSelect: none}, {id: 'refresh', icon: 'i-refresh', label: f('menuRefresh'), onSelect: none}]}/>}</Page>;
}
function FilmResume() {
  const [tab, setTab] = useState('more'), {isTV} = useTheme();
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-film-resume" back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={asset('bd-insideout.jpg')}/>} title={f('insideOut')} metadata={<Meta text={f('insideOutFacts')} tags={[f('insideOutRating')]}/>}
    primary={{icon: 'i-play', label: f('resume'), progress: .09, remaining: f('insideOutLeft'), startOver: {label: f('startOver'), onPress: none}, onPress: none}}
    actions={[{icon: 'i-check', label: f('myList'), labelled: true, active: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, ...(isTV ? [] : [{icon: 'i-dl' as const, label: f('download'), onPress: none}]), {icon: 'i-more', label: f('more'), onPress: none}]}
    tagline={f('insideOutTagline')} synopsis={f('insideOutSynopsis')} credits={[{label: f('directedBy'), names: 'Pete Docter'}, {label: f('starring'), names: 'Amy Poehler, Phyllis Smith, Richard Kind, Bill Hader'}]}
    picker={<TrackPicker audio={f('audioChoice')} subtitles={f('subtitlesChoice')} label={f('picker')} onPress={none}/>} tabs={tabs(tab, setTab)}>
    <Posters title={f('moreLikeInsideOut')} items={moreLikeInsideOut}/>
  </TitleFrame></Page>;
}

const duneCast: readonly CastPerson[] = (['1', '2', '3', '4', '5', '6'] as const).map(n => ({id: n, name: f(`dune2Cast${n}`), role: f(`dune2Role${n}`), onPress: none}));
const duneCrew: readonly CastPerson[] = (['1', '2', '3', '4'] as const).map(n => ({id: `crew-${n}`, name: f(`dune2Crew${n}`), role: f(`dune2CrewRole${n}`), onPress: none}));
function DuneDetails() {
  const {isTV} = useTheme();
  // TV's About carries the full synopsis and the tagline beside the facts (detail-tv-4).
  return <TitleDetails about={{title: f('about'), ...(isTV ? {body: f('dune2Synopsis'), note: f('dune2Tagline')} : {}), rows: [{label: f('released'), values: [{text: f('releasedValue')}]}, {label: f('runtime'), values: [{text: f('runtimeValue')}]},
      {label: f('genres'), values: [link(f('genre1')), link(f('genre2'))]}, {label: f('rated'), values: [{text: f('dune2Rating')}]}, {label: f('directedBy'), values: [link(f('dune2Director'))]},
      {label: f('studio'), values: [link(f('studioValue'))]}, {label: f('added'), values: [{text: f('addedValue')}]}]}}
    people={{title: f('castAndCrew'), groups: [duneCast, duneCrew], seeAll: {label: f('seeAllCount'), onPress: none},
      lines: [{label: f('directedBy'), names: f('dune2Director')}, {label: f('writtenBy'), names: `${f('dune2Crew1')}, ${f('dune2Crew2')}`}]}}
    collection={{title: f('duneCollectionName'), kindLabel: f('duneCollectionKind'), body: f('duneCollectionBody'), caption: [{id: 'count', text: defaultI18n.t('next.collection.filmsOnServer', {count: 2}), role: 'context'}], covers: [asset('pool/movies/dune.jpg'), asset('pool/movies/dune-part-two.jpg')],
      backdrop: asset('pool/movies/dune-part-two--bd.jpg'), onNavigate: none}}/>;
}
/** Cast & crew's See all: everyone on the full sheet over the Details tab, with Show more while the server has more. */
function FilmPeople() {
  const [open, setOpen] = useState(true);
  return <><FilmDetails/><PeopleSheet open={open} onOpenChange={setOpen} title={f('castAndCrew')} back={{label: f('details'), onPress: () => setOpen(false)}}
    groups={[duneCast, duneCrew]} more={{label: defaultI18n.t('action.showMore'), loading: false, onPress: none}}/></>;
}
/** Details with the hero gone: the collapsed bar (title, tabs, Play and +) as the page shows it once scrolled (desktop-detail f3; phone-detail d1 frame 2; detail-tv-4). */
function FilmDetails() {
  const [tab, setTab] = useState('details'), {isTV} = useTheme();
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-film-details" {...filmFrame(tab, setTab, isTV)} collapsed startFocus="cast-1"><DuneDetails/></TitleFrame></Page>;
}

type Episode = Readonly<{id: string; title: string; label: string; length: string; synopsis: string; progress?: number; watched?: boolean; status?: string; art: string}>;
const tlouEpisodes: readonly Episode[] = [
  {id: 'e1', title: f('ep1'), label: f('ep1Label'), length: f('ep1Length'), synopsis: f('ep1Synopsis'), progress: .67, status: f('ep1Status'), art: 'pool/tv/the-last-of-us--bd.jpg'},
  {id: 'e2', title: f('ep2'), label: `${f('ep2Label')}  ${f('ep2Date')}`, length: f('ep2Length'), synopsis: f('ep2Synopsis'), watched: true, art: 'bd-tlou.jpg'},
  {id: 'e3', title: f('ep3'), label: `${f('ep3Label')}  ${f('ep3Date')}`, length: f('ep3Length'), synopsis: f('ep3Synopsis'), art: 'pool/tv/the-last-of-us--bd.jpg'},
  {id: 'e4', title: f('ep4'), label: `${f('ep4Label')}  ${f('ep4Date')}`, length: f('ep4Length'), synopsis: f('ep4Synopsis'), art: 'bd-tlou.jpg'},
];
/** The episode rows (tablet f8, phone-detail d2, foldable f2): the kit's row, its synopsis beside the picture on a tablet. */
function EpisodeRows({episodes, opened}: {episodes: readonly Episode[]; opened?: string}) {
  const {isTV, form} = useTheme();
  return <EpisodeList items={episodes.map(episode => ({key: episode.id, title: episode.title, label: [{id: 'episode', text: episode.label, role: 'context'}], status: episode.status,
    length: episode.length, synopsis: episode.synopsis, artwork: asset(episode.art), watched: episode.watched, progress: episode.progress, onNavigate: none, onDownload: isTV ? undefined : none}))}
    opened={opened ? {key: opened, ...openedPlate(form !== 'phone')} : undefined}/>;
}
/** The episode opened in place under its row (phone-detail d2 frame 4; desktop-detail f5): its code and facts, synopsis, Play row, credits and neighbours. */
/** `pictured`: the neighbours carry their stills (desktop-detail f5); a phone's are small cards (frame 4). */
function openedPlate(pictured: boolean): EpisodePlateProps {
  return {still: asset('bd-tlou.jpg'), length: f('ep3Length'), code: f('ep3Code'), title: f('ep3'),
    facts: [{id: 'date', text: f('ep3Date'), role: 'identity'}, {id: 'content-rating', text: f('ep3Rating'), role: 'context'}],
    synopsis: f('ep3Synopsis'), primary: {icon: 'i-play', label: f('play'), onPress: none},
    actions: [{icon: 'i-plus', label: f('myList'), onPress: none}, {icon: 'i-dl', label: f('download'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}],
    credits: [{label: f('guestStars'), people: [{name: f('guest1'), role: `as ${f('guestRole1')}`, onPress: none}, {name: f('guest2'), role: `as ${f('guestRole2')}`, onPress: none}]},
      {label: f('directedBy'), people: [{name: f('ep3Director')}]}],
    showAll: count => defaultI18n.t('shelf.seeAllCount', {count}),
    previous: {label: f('previousEpisode'), title: f('ep2'), ...(pictured ? {detail: f('ep2Label'), artwork: asset('bd-tlou.jpg')} : {}), onPress: none},
    next: {label: f('nextEpisode'), title: f('ep4'), ...(pictured ? {detail: f('ep4Label'), artwork: asset('bd-tlou.jpg')} : {}), onPress: none}, close: {label: f('back'), onPress: none}};
}
/** TV, the Episodes tab's season column and episodes (detail-tv-5a): the seasons with the current one marked, Hold OK
 * under them; beside them the season's episodes as a row of stills and the band describing the season. */
type SeasonRowData = Readonly<{id: string; label: Name; detail: Name; progress?: number}>;
function tvSeasons(rows: readonly SeasonRowData[], current: string, band: {title: Name; count: Name; synopsis: Name}) {
  const t = defaultI18n.t;
  return {column: <TVSeasonList hint={{key: t('next.layout.holdOk'), label: t('next.watch.seasonOptions')}}
      items={rows.map(row => ({id: `title-season-${row.id}`, label: f(row.label), detail: f(row.detail), progress: row.progress, current: row.id === current, onFocus: none, onLongPress: none}))}/>,
    episodes: <TVEpisodes band={band}/>};
}
/** The episodes and the band as the TV page draws them: the band follows focus, a season's summary or the focused episode. */
function TVEpisodes({band}: {band: {title: Name; count: Name; synopsis: Name}}) {
  const t = defaultI18n.t, [focused, setFocused] = useState<Episode | undefined>(undefined);
  const items = tlouEpisodes.map((episode, index): TVEpisodeItem => ({key: `title-episode-${episode.id}`, title: episode.title, label: [{id: 'episode', text: f(`ep${index + 1}Label` as Name), role: 'context'}],
    length: episode.length, status: episode.status, watched: episode.watched, progress: episode.progress, artwork: asset(episode.art), onFocus: () => setFocused(episode), onPlay: none}));
  const data: EpisodeBandData = focused ? {kind: 'episode', title: focused.title, facts: [{id: 'code', text: f('ep1Code'), role: 'identity'}, {id: 'length', text: focused.length, role: 'value'}, {id: 'content-rating', text: f('tlouRating'), role: 'supplement'}],
      synopsis: focused.synopsis, ...(focused.progress !== undefined ? {state: {label: `${t('entry.resume')}, ${f('ep1Left')}`, progress: focused.progress}} : {}),
      guests: {label: t('next.watch.guestStars'), people: [{name: 'Nick Offerman', role: 'Bill'}, {name: 'Murray Bartlett', role: 'Frank'}]}, lines: [{label: f('directedBy'), names: 'Peter Hoar'}, {label: f('writtenBy'), names: 'Craig Mazin'}],
      hint: {key: t('next.layout.holdOk'), label: t('next.watch.episodeOptions')}}
    : {kind: 'season', title: f(band.title), facts: [f(band.count)], overview: f(band.synopsis)};
  return <TVEpisodeShelf items={items} entry={items[0]?.key} band={data}/>;
}
const moreLikeShows = [['pool/tv/severance.jpg', 'Severance', '2022'], ['pool/tv/silo.jpg', 'Silo', '2023'], ['pool/tv/stranger-things.jpg', 'Stranger Things', '2016'], ['pool/tv/the-walking-dead.jpg', 'The Walking Dead', '2010'],
  ['pool/tv/slow-horses.jpg', 'Slow Horses', '2022'], ['pool/tv/the-mandalorian.jpg', 'The Mandalorian', '2019'], ['pool/tv/breaking-bad.jpg', 'Breaking Bad', '2008']] as const;
const simpsonsSeasons: readonly SeasonRowData[] = [{id: 's1', label: 'season1', detail: 'count13', progress: 1}, {id: 's2', label: 'season2', detail: 'count22', progress: 1}, {id: 's3', label: 'season3', detail: 'season3Progress', progress: 4 / 24},
  {id: 's4', label: 'season4', detail: 'count22'}, {id: 's5', label: 'season5', detail: 'count22'}, {id: 's0', label: 'specials', detail: 'count12'}];
const showCast: readonly CastPerson[] = [['Pedro Pascal', 'Joel Miller'], ['Bella Ramsey', 'Ellie Williams'], ['Gabriel Luna', 'Tommy Miller'], ['Nick Offerman', 'Bill']].map(([name, role]) => ({id: name!, name: name!, role, onPress: none}));
function Show({opened, seasons = false}: {opened?: string; seasons?: boolean}) {
  const [tab, setTab] = useState('episodes'), [hide, setHide] = useState(false), [open, setOpen] = useState(seasons), [selected, setSelected] = useState<string | null>('s3');
  const simpsons = seasons, {isTV} = useTheme();
  // TV draws the Episodes tab as it opens (detail-tv-5a): the hero gone, the season column, the episodes; focus starts on the current season.
  const tv = isTV ? {collapsed: true, startFocus: `title-season-${simpsons ? 's3' : 's1'}`, seasons: tvSeasons(simpsons ? simpsonsSeasons : [{id: 's1', label: 'season1', detail: 'seasonProgress', progress: .25}], simpsons ? 's3' : 's1',
    {title: simpsons ? 'season3' : 'season1', count: simpsons ? 'season3Count' : 'episodeCount', synopsis: 'seasonSynopsis'})} : {};
  return <Page destination={{kind: 'library', libraryId: 'show'}}><TitleFrame testID={`frames-title-show${opened ? '-episode' : seasons ? '-seasons' : ''}`} back={back} topAction={playOn} {...tv}
    art={<AmbientArt treatment="title" foreground={asset(simpsons ? 'pool/tv/the-simpsons--bd.jpg' : 'pool/tv/the-last-of-us--bd.jpg')}/>} title={f(simpsons ? 'simpsons' : 'tlou')}
    metadata={<Meta text={f(simpsons ? 'simpsonsFacts' : 'tlouFacts')} tags={[f(simpsons ? 'simpsonsSeasons' : 'tlouSeasons')]}/>}
    primary={simpsons ? {icon: 'i-play', label: f('playS3E5'), onPress: none} : {icon: 'i-play', label: f('resumeEpisode'), progress: .67, remaining: f('ep1Left'), onPress: none}}
    actions={[{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-dl', label: f('download'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    synopsis={f(simpsons ? 'simpsonsSynopsis' : 'tlouSynopsis')} credits={[{label: f('createdBy'), names: [link('Craig Mazin'), link('Neil Druckmann')]}]}
    cast={<><PanelHeading title={f('cast')}/><CastRow groups={[showCast]}/></>} tabs={tabs(tab, setTab, true)}>
    {isTV ? <Posters title={f('moreLikeThis')} items={moreLikeShows}/> : <><PanelBlock><SeasonBar label={f(simpsons ? 'season3' : 'season1')} count={f(simpsons ? 'season3Count' : 'episodeCount')} onSeasons={() => setOpen(true)} hideWatched={hide} onHideWatched={setHide}/></PanelBlock>
      <EpisodeRows episodes={tlouEpisodes} opened={opened}/></>}
    {simpsons && !isTV && <SeasonSheet open={open} onOpenChange={setOpen} total={36} selectedId={selected} onSelect={setSelected} rows={[
      {id: 's1', number: 1, label: f('season1'), count: f('count13'), progress: f('watched'), complete: true}, {id: 's2', number: 2, label: f('season2'), count: f('count22'), progress: f('watched'), complete: true},
      {id: 's3', number: 3, label: f('season3'), count: f('season3Count'), progress: f('season3Progress'), fraction: 4 / 24}, {id: 's4', number: 4, label: f('season4'), count: f('count22')},
      {id: 's5', number: 5, label: f('season5'), count: f('count22')}, {id: 's0', number: 0, label: f('specials'), count: f('count12')}]}/>}
  </TitleFrame></Page>;
}
function Season() {
  const [tab, setTab] = useState('episodes'), [hide, setHide] = useState(false), {isTV} = useTheme();
  const tv = isTV ? {collapsed: true, startFocus: 'title-season-s1', seasons: tvSeasons([{id: 's1', label: 'season1', detail: 'seasonProgress', progress: .25}], 's1', {title: 'season1', count: 'episodeCount', synopsis: 'seasonSynopsis'})} : {};
  return <Page destination={{kind: 'library', libraryId: 'show'}}><TitleFrame testID="frames-title-season" back={back} topAction={playOn} {...tv} art={<AmbientArt treatment="title" foreground={asset('bd-tlou.jpg')}/>} kicker={{icon: 'i-tv', label: f('seasonKicker'), onPress: none}} title={f('season1')}
    metadata={<Meta text={f('seasonFacts')}/>} primary={{icon: 'i-play', label: f('resumeEpisode'), progress: .67, remaining: f('ep1Left'), onPress: none}}
    actions={[{icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-dl', label: f('download'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]} synopsis={f('seasonSynopsis')} tabs={tabs(tab, setTab, true)}>
    {isTV ? <Posters title={f('moreLikeThis')} items={moreLikeShows}/> : <><PanelBlock><SeasonBar label={f('season1')} count={f('episodeCount')} onSeasons={none} hideWatched={hide} onHideWatched={setHide}/></PanelBlock><EpisodeRows episodes={tlouEpisodes}/></>}
  </TitleFrame></Page>;
}
function EpisodePage() {
  const theme = useTheme();
  return <Page destination={{kind: 'library', libraryId: 'show'}}><TitleFrame testID="frames-title-episode" back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={asset('pool/tv/the-last-of-us--bd.jpg')}/>} kicker={{icon: 'i-tv', label: f('tlou'), detail: f('season1'), onPress: none}} title={f('ep3')}
    metadata={<Meta text={f('episodeFacts')} tags={[f('ep3Rating')]}/>} primary={{icon: 'i-play', label: f('play'), onPress: none}}
    actions={[{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-dl', label: f('download'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    synopsis={f('ep3Synopsis')} credits={[{label: f('directedBy'), names: [link(f('ep3Director'))]}, {label: f('writtenBy'), names: [link(f('ep3Writer'))]}]}
    picker={<TrackPicker audio={f('audioChoice')} subtitles={f('subtitlesChoice')} label={f('picker')} onPress={none}/>}>
    <PanelBlock><PanelHeading title={f('guestStars')}/><CastRow groups={[[{id: 'g1', name: f('guest1'), role: f('guestRole1'), onPress: none}, {id: 'g2', name: f('guest2'), role: f('guestRole2'), onPress: none}]]}/></PanelBlock>
    <PanelBlock><View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.sm}}>
      <View style={{flex: 1, minWidth: 260}}><EpisodeNeighbour direction="previous" label={f('previousEpisode')} title={f('ep2')} detail={f('ep2Label')} artwork={asset('bd-tlou.jpg')} onPress={none}/></View>
      <View style={{flex: 1, minWidth: 260}}><EpisodeNeighbour direction="next" label={f('nextEpisode')} title={f('ep4')} detail={f('ep4Label')} artwork={asset('bd-tlou.jpg')} onPress={none}/></View></View></PanelBlock>
  </TitleFrame></Page>;
}

/** A person (phone-detail d3 frame 7): their initials over a glow from their films, the name with their department and
 * birth, the biography, Known for, then every credit on this server as posters, filtered by All, Cast or Crew. The credits
 * are a windowed grid that scrolls with the page (`posterGridScroller`), as an actor with hundreds of titles needs. */
type PersonCreditFixture = Readonly<{id: string; art: string; title: Name; role: Name}>;
const personCredits: readonly PersonCreditFixture[] = [{id: 'd2', art: 'pool/movies/dune-part-two.jpg', title: 'dune2', role: 'asJessica'},
  {id: 'd1', art: 'pool/movies/dune.jpg', title: 'dune', role: 'asJessica'}, {id: 'silo', art: 'pool/tv/silo.jpg', title: 'silo', role: 'asJuliette'}];
function Person() {
  const [filter, setFilter] = useState('all'), [open, setOpen] = useState(false), t = defaultI18n.t, tv = useTheme().isTV;
  const collection = useMemo(() => createWindowedCollection({pageSize: 30, maxResidentPages: 4, maxConcurrent: 1, keyOf: (credit: PersonCreditFixture) => credit.id,
    fetchPage: async (start, count) => ({items: personCredits.slice(start, start + count), total: personCredits.length})}), []);
  useEffect(() => () => collection.dispose(), [collection]);
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-person" back={{label: f('dune2'), onPress: none}}
    topAction={{icon: 'i-more', label: f('more'), onPress: none}}
    // The served ambient image is not available to the gallery (artwork.gallery.tsx); its palette stands in for the glow.
    art={<AmbientArt treatment="library" palette={{first: '#a8692a', second: '#1d3848'}}/>}
    cover={{shape: 'circle', node: size => <PersonCircle size={size} initials="RF" alt=""/>}}
    title={f('person')} metadata={<Meta text={f('personRoleBorn')}/>} synopsis={f('personBio')} readMore={{label: t(open ? 'web.person.showLess' : 'web.person.readMore'), expanded: open, onPress: () => setOpen(value => !value)}}
    // TV (tv-detail frame 10): the circle at the left, and the credits as a row (one per department) instead of the grid.
    scroller={tv ? undefined : posterGridScroller({collection, getKey: credit => credit.id, testID: 'frames-title-person-credits', item: credit})}>
    <Posters small title={t('web.person.knownFor')} items={[['pool/movies/dune-part-two.jpg', f('dune2'), '2024'], ['pool/movies/dune.jpg', f('dune'), '2021'], ['pool/tv/silo.jpg', f('silo'), '2023']]}/>
    {tv ? <PosterWindowRow title={t('web.person.credits')} count={t('web.person.titles', {count: personCredits.length})} collection={collection} getKey={credit => credit.id}
      item={credit} testID="frames-title-person-credits"/> : <PanelBlock>
      <PanelHeading title={t('web.person.credits')} trailing={t('web.person.titlesOnServer', {count: personCredits.length})}/>
      <ChoiceChips items={([['all', 'title.creditsAll'], ['cast', 'web.person.cast'], ['crew', 'web.person.crew']] as const).map(([id, label]) => ({id, label: t(label), selected: filter === id, onPress: () => setFilter(id)}))}/>
    </PanelBlock>}
  </TitleFrame></Page>;
}
const credit = (value: PersonCreditFixture) => ({title: f(value.title), caption: f(value.role), artwork: asset(value.art), onNavigate: none, onMore: none});

/** A collection on a phone (phone-detail d3 frame 8): the films in one windowed list, each with Watched or how far in,
 * and the count with the sort on one line. */
type CollectionFilmFixture = Readonly<{id: string; art: string; title: Name; facts: Name; watched?: boolean; progress?: number; left?: Name}>;
const collectionFilms: readonly CollectionFilmFixture[] = [
  // A long title wraps to two lines and the row makes room for it (it is never cut).
  {id: 'f1', art: 'pool/movies/the-lord-of-the-rings-the-fellowship-of-the-ring.jpg', title: 'fellowshipFull', facts: 'fellowshipFacts', watched: true},
  {id: 'f2', art: 'pool/movies/the-lord-of-the-rings-the-two-towers.jpg', title: 'towers', facts: 'towersFacts', progress: .5, left: 'towersLeft'},
  {id: 'f3', art: 'pool/movies/the-lord-of-the-rings-the-return-of-the-king.jpg', title: 'king', facts: 'kingFacts'}];
function PhoneCollection() {
  const t = defaultI18n.t;
  const collection = useMemo(() => createWindowedCollection({pageSize: 30, maxResidentPages: 4, maxConcurrent: 1, keyOf: (film: CollectionFilmFixture) => film.id,
    fetchPage: async (start, count) => ({items: collectionFilms.slice(start, start + count), total: collectionFilms.length})}), []);
  useEffect(() => () => collection.dispose(), [collection]);
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-collection" back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={asset('bd-lotr.jpg')}/>}
    kicker={{label: f('collectionKicker')}} title={f('lotr')} metadata={<Meta text={`${t('next.browse.filmCount', {count: 3})} · 2001 to 2003`}/>}
    primary={{icon: 'i-play', label: f('playAll'), onPress: none}} actions={[{icon: 'i-shuffle', label: t('title.shuffle'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    note={t('next.collection.automaticDescription')}
    scroller={titleRowsScroller({collection, getKey: film => film.id, testID: 'frames-title-collection-films', item: film => ({title: f(film.title), facts: f(film.facts).split(' · '),
      watched: film.watched, progress: film.progress, remaining: film.left ? f(film.left) : undefined, artwork: asset(film.art), onNavigate: none, onMore: none})})}>
    <PanelBlock><PanelHeading quiet title={t('next.browse.filmCount', {count: 3})} trailing={<Chip variant="pill" trailingIcon="i-chdown" onPress={none}>{f('byRelease')}</Chip>}/></PanelBlock>
  </TitleFrame></Page>;
}
function Collection() {
  if (useFrameMetrics().form === 'phone') return <PhoneCollection/>;
  const film = (number: number, key: string, title: string, made: string, synopsis: string, status: {label: string; icon?: 'i-check'; accent?: boolean}, action: ReactNode, progress?: number) =>
    <NumberedRow key={key} number={number} artwork={asset(key)} title={title} facts={<Meta text={made} tags={['PG-13']}/>} synopsis={synopsis} status={status} action={action} progress={progress} onPress={none}/>;
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-collection" back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={asset('bd-lotr.jpg')}/>}
    kicker={{icon: 'i-layers', label: f('collectionKicker'), detail: f('collectionMade')}} title={f('lotr')} metadata={<Meta text={f('lotrFacts')} tags={['PG-13']}/>}
    primary={{icon: 'i-play', label: f('playAll'), onPress: none}} actions={[{icon: 'i-shuffle', label: f('shuffle'), labelled: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]} synopsis={f('lotrSynopsis')}
    aside={<><View><PanelHeading title={f('about')}/><AboutTable rows={[{label: f('films'), values: [{text: f('filmsValue')}]}, {label: f('years'), values: [{text: f('yearsValue')}]}, {label: f('directedBy'), values: [link(f('lotrDirector'))]}, {label: f('genres'), values: [link(f('lotrGenre1')), link(f('lotrGenre2'))]}]}/></View>
      <OwnerCard title={f('thisCollection')} mark={f('ownerOnly')} body={f('thisCollectionBody')} actions={[{icon: 'i-edit', label: f('rename'), onPress: none}, {icon: 'i-plus', label: f('addFilm'), onPress: none}, {icon: 'i-sort', label: f('changeOrder'), onPress: none}]}/></>}>
    <PanelBlock><PanelHeading title={f('filmsInOrder')} note={f('byRelease')} trailing={<Chip variant="pill">{f('collectionOrder')}</Chip>}/>
      {film(1, 'pool/movies/the-lord-of-the-rings-the-fellowship-of-the-ring.jpg', f('fellowship'), f('fellowshipFacts'), f('fellowshipSynopsis'), {label: f('watched'), icon: 'i-check'}, <Button variant="glass" size="small" icon="i-play" onPress={none}>{f('playAgain')}</Button>)}
      {film(2, 'pool/movies/the-lord-of-the-rings-the-two-towers.jpg', f('towers'), f('towersFacts'), f('towersSynopsis'), {label: f('upNext'), accent: true}, <Button variant="glass" size="small" icon="i-play" onPress={none}>{`${f('resume')} · ${f('towersLeft')}`}</Button>, .5)}
      {film(3, 'pool/movies/the-lord-of-the-rings-the-return-of-the-king.jpg', f('king'), f('kingFacts'), f('kingSynopsis'), {label: f('notStarted')}, <Button variant="glass" size="small" icon="i-play" onPress={none}>{f('play')}</Button>)}
    </PanelBlock>
  </TitleFrame></Page>;
}

const songs = (names: readonly Name[], album?: string): TrackRowData[] => names.map((name, index) => ({id: name, number: index + 1, text: {id: name, title: f(name), caption: album ? [{id: 'album', text: album, role: 'context'}] : [], detail: []},
  plays: [18, 9, 31, 12][index], duration: {id: 'duration', text: ['4:10', '3:59', '3:31', '3:11'][index]!, role: 'context'}, playing: !album && index === 2, favorite: !album && index === 2}));
function Songs({rows, ranked = false}: {rows: readonly TrackRowData[]; ranked?: boolean}) {
  return <View>{rows.map((row, index) => <SongRow key={row.id} row={row} index={index} ranked={ranked} onPlay={none} onMore={none} onEnter={none}/>)}</View>;
}
/** An album on a phone (phone-app frame 13): its cover over a glow from it, the artist as a link under the title, Play,
 * Shuffle and Download, ⋯ at the top, and its numbered songs (the playing one marked). */
const restingTracks: TrackSelection = {active: false, isSelected: () => false, onToggle: () => {}, onEnter: () => {}, renderControl: () => null};
function PhoneAlbum() {
  const rows = songs(['song1', 'song2', 'song3', 'song4']).map((row, index) => ({...row, number: index + 1}));
  return <Page destination={{kind: 'library', libraryId: 'music'}}><TitleFrame testID="frames-title-album" back={back}
    topAction={{icon: 'i-more', label: f('more'), onPress: none}}
    scroller={musicScroller({})}
    // The served ambient image is not available to the gallery (artwork.gallery.tsx); the cover's palette stands in for the glow.
    art={<AmbientArt treatment="library" palette={{first: '#8a5636', second: '#3b2418'}}/>}
    cover={{shape: 'square', node: <Artwork source={asset('pool/albums/after-hours.jpg')} style={{width: '100%', height: '100%'}}/>}}
    title={f('album')} byline={{text: f('albumArtist'), onPress: none}} metadata={<Meta text={f('albumFacts')}/>}
    primary={{icon: 'i-play', label: f('play'), onPress: none}}
    actions={[{icon: 'i-shuffle', label: f('shuffle'), labelled: true, split: true, onPress: none}, {icon: 'i-dl', label: f('download'), onPress: none}]}>
    <View>{rows.map((row, index) => <TrackRow key={row.id} row={row} variant="album" index={index} selection={restingTracks} onPlay={none} onMore={none}/>)}</View>
  </TitleFrame></Page>;
}
function Album() {
  if (useFrameMetrics().form === 'phone') return <PhoneAlbum/>;
  // The album page keeps its own scroller (song windowing, paging near the end): the frame hands it the page and the collapse point.
  return <Page destination={{kind: 'library', libraryId: 'music'}}><TitleFrame testID="frames-title-album" back={back}
    scroller={musicScroller({})} cover={{shape: 'square', node: <Artwork source={asset('pool/albums/after-hours.jpg')} style={{width: '100%', height: '100%'}}/>}} kicker={{label: f('albumKicker')}} title={f('album')}
    metadata={<Meta text={`${f('albumArtist')} · ${f('albumFacts')}`}/>} primary={{icon: 'i-play', label: f('play'), onPress: none}}
    actions={[{icon: 'i-shuffle', label: f('shuffle'), labelled: true, onPress: none}, {icon: 'i-heart', label: f('favourite'), onPress: none}, {icon: 'i-listadd', label: f('addToPlaylist'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    aside={<><View><PanelHeading title={f('details')}/><AboutTable rows={[{label: f('year'), values: [{text: '2020'}]}, {label: f('label'), values: [{text: f('labelValue')}]}, {label: f('type'), values: [{text: f('albumKicker')}]}, {label: f('genre'), values: [link('R&B/Soul')]}, {label: f('added'), values: [{text: 'Oct 17, 2026'}]}]}/></View>
      <OwnerCard title={f('files')} mark={f('ownerOnly')} body={f('filesBody')} actions={[{icon: 'i-folder', label: f('manage'), onPress: none}]}/></>}>
    <PanelBlock><Songs rows={songs(['song1', 'song2', 'song3', 'song4'])}/></PanelBlock>
  </TitleFrame></Page>;
}
function Artist() {
  const theme = useTheme();
  return <Page destination={{kind: 'library', libraryId: 'music'}}><TitleFrame testID="frames-title-artist" back={back} cover={{shape: 'circle', node: <Artwork source={asset('pool/albums/abbey-road-2019-mix.jpg')} style={{width: '100%', height: '100%'}}/>}} kicker={{label: f('artistKicker')}} title={f('artist')}
    metadata={<Meta text={f('artistFacts')}/>} primary={{icon: 'i-play', label: f('play'), onPress: none}}
    actions={[{icon: 'i-shuffle', label: f('shuffle'), labelled: true, onPress: none}, {icon: 'i-heart', label: f('favourite'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    aside={<><View><PanelHeading title={f('albums')}/><SquareCard title={f('abbeyRoad')} caption={[{id: 'year', text: '1969', role: 'context'}]} artwork={asset('pool/albums/abbey-road-2019-mix.jpg')} width={theme.spec.card.width.poster[theme.column] * 1.2} onNavigate={none}/></View>
      <View><PanelHeading title={f('details')}/><AboutTable rows={[{label: f('country'), values: [{text: f('countryValue')}]}, {label: f('active'), values: [{text: f('activeValue')}]}, {label: f('genre'), values: [link('Rock')]}, {label: f('type'), values: [{text: f('group')}]}]}/></View></>}>
    <PanelBlock><PanelHeading title={f('topSongs')}/><Songs ranked rows={songs(['artistSong1', 'artistSong2', 'artistSong3', 'artistSong4'], f('abbeyRoad')).map(row => ({...row, artwork: <Artwork source={asset('pool/albums/abbey-road-2019-mix.jpg')} width={40} height={40}/>}))}/></PanelBlock>
  </TitleFrame></Page>;
}
function Book() {
  const rows: TrackRowData[] = (['chapter1', 'chapter2', 'chapter3', 'chapter4', 'chapter5', 'chapter6', 'chapter7'] as const).map((name, index) => ({id: name, number: index + 1, text: {id: name, title: f(name), caption: [], detail: []},
    duration: {id: 'duration', text: ['27:43', '22:15', '25:10', '26:38', '39:47', '35:19', '27:29'][index]!, role: 'context'}, done: index < 5, current: index === 5, progress: index === 5 ? .52 : undefined}));
  return <Page destination={{kind: 'library', libraryId: 'audiobook'}}><TitleFrame testID="frames-title-book" back={back} cover={{shape: 'square', node: <Artwork source={asset('pool/books/harry-potter-and-the-philosopher-s-stone.jpg')} style={{width: '100%', height: '100%'}}/>}}
    kicker={{label: f('bookSeries'), detail: f('bookSeriesDetail'), onPress: none}} title={f('book')} metadata={<Meta text={f('bookFacts')}/>}
    primary={{icon: 'i-play', label: f('resumeChapter'), progress: .32, remaining: f('bookLeft'), startOver: {label: f('startOver'), onPress: none}, onPress: none}}
    actions={[{icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-plus', label: f('myList'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    aside={<><View><PanelHeading title={f('aboutBook')}/><AboutTable rows={[{label: f('author'), values: [link(f('authorValue'))]}, {label: f('narrator'), values: [link(f('narratorValue'))]}, {label: f('published'), values: [{text: f('publishedValue')}]}]}/></View>
      <OwnerCard title={f('files')} mark={f('ownerOnly')} body={f('filesBody')} actions={[{icon: 'i-folder', label: f('manage'), onPress: none}]}/></>}>
    <PanelBlock><PanelHeading title={f('chapters')}/><Songs rows={rows}/></PanelBlock>
  </TitleFrame></Page>;
}

function Missing() {
  const [tab, setTab] = useState('more');
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-missing" back={back} art={<AmbientArt treatment="title"/>} title={f('longTitle')} metadata={<Meta text={f('longFacts')}/>} primary={{icon: 'i-play', label: f('play'), onPress: none}} tabs={tabs(tab, setTab)}>
    <Posters title={f('moreLikeThis')} items={moreLikeDune}/>
  </TitleFrame></Page>;
}
/** Loading: the card's title and art are known; the facts and words are skeletons, the buttons are in place but say nothing they do not know, the tab panel keeps skeleton cards. */
function Loading() {
  const [tab, setTab] = useState('more'), {isTV} = useTheme();
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-loading" {...filmFrame(tab, setTab, isTV)} kicker={undefined} state={{kind: 'loading'}}/></Page>;
}

/** The overlays a title page opens, each on the R9 table's form for its task (SheetFrame `task`), over the page it opens from. */
const dune2Files: readonly FileFacts[] = [
  {id: 'v1', edition: "Director's Cut", available: true, width: 3840, height: 1608, videoCodec: 'hevc', dynamicRange: 'dolby_vision', bitDepth: 10, bitRate: 44_000_000, container: 'mkv', sizeBytes: 61.4 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'truehd', channels: 8, objectAudio: 'atmos'}, {id: 'a2', language: 'en', codec: 'ac3', channels: 6}, {id: 'a3', language: 'fr', codec: 'eac3', channels: 8}, {id: 'a4', language: 'es', codec: 'eac3', channels: 8}],
    subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'en', hearingImpaired: true}, {id: 's3', language: 'fr'}, {id: 's4', language: 'es'}]},
  {id: 'v2', available: true, width: 3840, height: 1608, videoCodec: 'hevc', dynamicRange: 'dolby_vision', bitDepth: 10, bitRate: 41_000_000, container: 'mkv', sizeBytes: 58.2 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'truehd', channels: 8, objectAudio: 'atmos'}, {id: 'a2', language: 'en', codec: 'ac3', channels: 6}], subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'en', hearingImpaired: true}, {id: 's3', language: 'fr'}]},
  {id: 'v3', available: true, width: 1920, height: 804, videoCodec: 'h264', bitRate: 7_000_000, container: 'mkv', sizeBytes: 9.8 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'ac3', channels: 6}], subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'es'}]},
];
/** Play a different version (detail-phone 9b, desktop-detail 9b, detail-tv-8b). */
function Versions() {
  const [tab, setTab] = useState('more'), [open, setOpen] = useState(true), {isTV} = useTheme();
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-versions" {...filmFrame(tab, setTab, isTV)}><Posters title={f('moreLikeDune')} items={moreLikeDune}/></TitleFrame>
    <SheetFrame kind="task" task="versions" open={open} onOpenChange={setOpen} title={f('versionTitle')} subtitle={f('dune2')} startFocus="version-v2">
      <VersionRows onChoose={() => setOpen(false)} rows={(['1', '2', '3'] as const).map(n => ({id: `v${n}`, heading: f(`version${n}`), detail: f(`version${n}Detail`)}))}/>
    </SheetFrame></Page>;
}
/** File info for a film with three versions (desktop-detail f10, detail-phone 5 frame 11, detail-tv-5d): the screen's File info content on the R9 `details` form. */
function FileInfo() {
  const [tab, setTab] = useState('more'), [open, setOpen] = useState(true), {isTV} = useTheme();
  const model = fileInfoModel(dune2Files, {audience: 'viewer'}, {t: defaultI18n.t});
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID="frames-title-file-info" {...filmFrame(tab, setTab, isTV)}><Posters title={f('moreLikeDune')} items={moreLikeDune}/></TitleFrame>
    <SheetFrame kind="task" task="details" open={open} onOpenChange={setOpen} title={f('fileInfo')} subtitle={f('dune2')}>
      <NativeFileInfoContent model={model} versionCount={dune2Files.length} onManage={none}/>
    </SheetFrame></Page>;
}
/** The owner's Episode display (desktop-detail f4): a show's Details with the Show settings card; Change opens the choices beside it (R9 `choice`). */
function EpisodeDisplayCase() {
  const [tab, setTab] = useState('details'), [open, setOpen] = useState(true), [group, setGroup] = useState('season'), [number, setNumber] = useState('season'), [order, setOrder] = useState('oldest');
  const change = useRef<View>(null), theme = useTheme(), frame = useFrameMetrics();
  return <Page destination={{kind: 'library', libraryId: 'show'}}><TitleFrame testID="frames-title-episode-display" back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={asset('pool/tv/the-last-of-us--bd.jpg')}/>} title={f('tlou')}
    metadata={<Meta text={f('tlouFacts')} tags={[f('tlouSeasons')]}/>} primary={{icon: 'i-play', label: f('resumeEpisode'), progress: .67, remaining: f('ep1Left'), onPress: none}}
    actions={[{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    synopsis={f('tlouSynopsis')} tabs={tabs(tab, setTab, true)} collapsed>
    <PanelBlock><View style={{gap: theme.space.sm, padding: frame.cardPadding, borderRadius: frame.cardRadius, backgroundColor: theme.colors.raised, borderWidth: 1, borderColor: theme.colors.line}}>
      <PanelHeading title={f('showSettings')} note={f('ownerOnly')}/>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f('showSettingsBody')}</Text>
      <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
        <View style={{flex: 1}}><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{f('episodeDisplay')}</Text><Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{`${f('bySeason')}  ${f('oldestFirst')}`}</Text></View>
        <View ref={change} collapsable={false}><Button variant="glass" size="small" trailingIcon="i-chdown" onPress={() => setOpen(true)}>{f('change')}</Button></View>
      </View>
    </View></PanelBlock>
    <SheetFrame kind="task" task="choice" open={open} onOpenChange={setOpen} title={f('episodeDisplay')} anchorRef={change}>
      <EpisodeDisplay title={f('episodeDisplay')} mark={f('forEveryone')} intro={f('episodeDisplayIntro')} preview={{label: f('readsAs'), code: f('ep3Code'), title: f('ep3')}} rows={[
        {label: f('groupEpisodes'), items: [{value: 'season', label: f('bySeason')}, {value: 'one', label: f('inOneList')}, {value: 'ranges', label: f('ranges')}], value: group, onValueChange: setGroup},
        {label: f('numberThem'), items: [{value: 'season', label: f('bySeason')}, {value: 'across', label: f('acrossSeasons')}], value: number, onValueChange: setNumber},
        {label: f('order'), items: [{value: 'oldest', label: f('oldestFirst')}, {value: 'newest', label: f('newestFirst')}], value: order, onValueChange: setOrder}]}/>
    </SheetFrame>
  </TitleFrame></Page>;
}

const whole = {screen: true} as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-title-film', title: 'next.platform.frame.titleFilm', render: () => <Film/>, ...whole},
  {id: 'frames-title-film-resume', title: 'next.platform.frame.titleFilmResume', render: FilmResume, ...whole},
  {id: 'frames-title-film-more', title: 'next.platform.frame.titleFilmMore', render: () => <Film more/>, ...whole},
  {id: 'frames-title-film-selecting', title: 'next.platform.frame.titleFilm', render: () => <Film selecting/>, ...whole},
  {id: 'frames-title-film-details', title: 'next.platform.frame.titleFilmDetails', render: FilmDetails, ...whole},
  {id: 'frames-title-film-people', title: 'next.platform.frame.titleFilmDetails', render: FilmPeople, ...whole},
  {id: 'frames-title-show', title: 'next.platform.frame.titleShow', render: () => <Show/>, ...whole},
  {id: 'frames-title-show-seasons', title: 'next.platform.frame.titleShowSeasons', render: () => <Show seasons/>, ...whole},
  {id: 'frames-title-show-episode', title: 'next.platform.frame.titleShowEpisode', render: () => <Show opened="e3"/>, ...whole},
  {id: 'frames-title-season', title: 'next.platform.frame.titleSeason', render: Season, ...whole},
  {id: 'frames-title-episode', title: 'next.platform.frame.titleEpisode', render: EpisodePage, ...whole},
  {id: 'frames-title-person', title: 'next.platform.frame.titlePerson', render: Person, ...whole},
  {id: 'frames-title-collection', title: 'next.platform.frame.titleCollection', render: Collection, ...whole},
  {id: 'frames-title-album', title: 'next.platform.frame.titleAlbum', render: Album, ...whole},
  {id: 'frames-title-artist', title: 'next.platform.frame.titleArtist', render: Artist, ...whole},
  {id: 'frames-title-book', title: 'next.platform.frame.titleBook', render: Book, ...whole},
  {id: 'frames-title-missing', title: 'next.platform.frame.titleMissing', render: Missing, ...whole},
  {id: 'frames-title-loading', title: 'next.platform.frame.titleLoading', render: Loading, ...whole},
  {id: 'frames-title-versions', title: 'next.platform.frame.titleVersions', render: Versions, ...whole},
  {id: 'frames-title-file-info', title: 'next.platform.frame.titleFileInfo', render: FileInfo, ...whole},
  {id: 'frames-title-episode-display', title: 'next.platform.frame.titleEpisodeDisplay', render: EpisodeDisplayCase, ...whole},
];
export default galleries;
