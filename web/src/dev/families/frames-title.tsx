import {Fragment, useEffect, useRef, useState, type ComponentType, type ReactNode} from 'react';
import {formatMessage, type MessageId} from '@i18n';
import type {FactPart} from '@portico/client-core/kit-model';
import {titleFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/title-fixtures.ts';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {AmbientArt, Artwork, PersonCircle, nameInitials} from '../../kit/artwork';
import {CollectionBanner, EpisodeTile, PosterCard, SquareCard} from '../../kit/cards';
import {TrackTable, type TrackRowData} from '../../kit/browse';
import {Button, Chip} from '../../kit/controls';
import {AboutTable, CastRow, EpisodeDisplay, EpisodeList, EpisodeListOpened, EpisodeNeighbour, NumberedRow, OwnerCard, PanelBlock, PanelHeading, PanelText, SeasonBar, SeasonMenu, SheetFrame, TitleFrame, TrackPicker, VersionRows, episodeRowClass,
  type CastPerson, type TitleFrameProps} from '../../kit/frames';
import {HeroFacts, Shelf} from '../../kit/layout';
import {Menu, MenuGroup, MenuItem, MenuSeparator} from '../../kit/overlays';
import {usePhone} from '../../kit/overlays/shared';
import {FileInfoBody} from '../../kit/overlays/FileInfo';
import {fileInfoModel, type FileFacts} from '@core/presentation/kit-model';
import {defaultI18n} from '@i18n';
import {EpisodeDetailsLayout} from '../../kit/title/TitleParts';
import {Page, useReferenceArtwork, useWide} from './frames';

/** Family C, the title page's forms: every kind of title page filled in full on the one `TitleFrame`, from the parts
 * its tab panel and side are built from. The same ids as the native gallery (`dev/families/frames/title.gallery.tsx`).
 * Each case answers to the window's width, so the same case is captured at 390 and at 1440. Serve-only. */
type Name = keyof typeof titleFixtureMessages extends `next.title.fixture.${infer N}` ? N : never;
const f = (name: Name) => formatMessage(titleFixtureMessages[`next.title.fixture.${name}`]);
const none = () => {};
/** Facts as the server sends them: the first is the identity; a rating and a season count are outlined, a runtime is not (phone-detail d1, desktop-detail f1). */
const facts = (text: string, ...tags: string[]): FactPart[] => [...text.split(' · ').map((part, index) => ({id: index === 0 ? 'year' : `fact-${index}`, text: part, role: index === 0 ? 'identity' as const : 'context' as const})),
  ...tags.map((tag, index) => ({id: /season/.test(tag) ? 'season-count' : index === 0 ? 'content-rating' : `tag-${index}`, text: tag, role: 'context' as const}))];
const year = (text: string): FactPart[] => [{id: 'year', text, role: 'context'}];
function Meta({text, tags = []}: {text: string; tags?: string[]}) {
  return <HeroFacts parts={facts(text, ...tags)} platform={usePhone() ? 'phone-web' : 'desktop'} />;
}
const back = {label: f('back'), onPress: none}, playOn = {icon: 'i-cast' as const, label: f('playOn'), onPress: none};

function Posters({title, note, items}: {title: string; note?: string; items: readonly (readonly [string, string, string])[]}) {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Shelf heading={title} subtitle={note} platform={phone ? 'phone-web' : 'desktop'} items={items} getKey={item => item[0]}
    renderItem={([id, name, made]) => <PosterCard model={{title: name, caption: year(made)}} artwork={art(id)} style={{width: 'var(--card-poster-width)'}} onNavigate={none} />} />;
}
const moreLikeDune = [['pool/movies/dune.jpg', 'Dune', '2021'], ['pool/movies/blade-runner.jpg', 'Blade Runner', '1982'], ['pool/movies/interstellar.jpg', 'Interstellar', '2014'], ['pool/movies/star-wars.jpg', 'Star Wars', '1977'],
  ['pool/movies/avatar.jpg', 'Avatar', '2009'], ['pool/movies/the-empire-strikes-back.jpg', 'The Empire Strikes Back', '1980'], ['pool/movies/the-matrix.jpg', 'The Matrix', '1999']] as const;
const viewersAlso = [['pool/movies/oppenheimer.jpg', 'Oppenheimer', '2023'], ['pool/movies/inception.jpg', 'Inception', '2010'], ['pool/movies/the-dark-knight.jpg', 'The Dark Knight', '2008'], ['pool/movies/barbie.jpg', 'Barbie', '2023'],
  ['pool/movies/spider-man-across-the-spider-verse.jpg', 'Spider-Man: Across the Spider-Verse', '2023'], ['pool/movies/fight-club.jpg', 'Fight Club', '1999'], ['pool/movies/alien.jpg', 'Alien', '1979']] as const;
const moreLikeInsideOut = [['pool/movies/toy-story.jpg', 'Toy Story', '1995'], ['pool/movies/monsters-inc.jpg', 'Monsters, Inc.', '2001'], ['pool/movies/wall-e.jpg', 'WALL·E', '2008'], ['pool/movies/incredibles-2.jpg', 'Incredibles 2', '2018'],
  ['pool/movies/spirited-away.jpg', 'Spirited Away', '2001'], ['pool/movies/how-to-train-your-dragon.jpg', 'How to Train Your Dragon', '2010'], ['pool/movies/moana.jpg', 'Moana', '2016']] as const;
const moreLikeShow = [['pool/tv/severance.jpg', 'Severance', '2022'], ['pool/tv/silo.jpg', 'Silo', '2023'], ['pool/tv/stranger-things.jpg', 'Stranger Things', '2016'], ['pool/tv/the-walking-dead.jpg', 'The Walking Dead', '2010'],
  ['pool/tv/slow-horses.jpg', 'Slow Horses', '2022'], ['pool/tv/the-mandalorian.jpg', 'The Mandalorian', '2019'], ['pool/tv/breaking-bad.jpg', 'Breaking Bad', '2008']] as const;

const tabs = (value: string, onValueChange: (value: string) => void, show = false) => ({value, onValueChange, items: [
  ...(show ? [{value: 'episodes', label: f('episodes')}] : []), {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]});

/** A film: the collection kicker, the facts, Play with My List, Watched, Download and More, the picker row, and the tabs. */
function filmFrame(art: (key: string) => ReturnType<ReturnType<typeof useReferenceArtwork>>, tab: string, setTab: (value: string) => void): Omit<TitleFrameProps, 'children'> {
  return {back, topAction: playOn, art: <AmbientArt treatment="title" foreground={art('pool/movies/dune-part-two--bd.jpg')} />,
    kicker: {icon: 'i-layers', label: f('duneCollection'), detail: f('duneCollectionCount'), onPress: none}, title: f('dune2'), metadata: <Meta text={f('dune2Facts')} tags={[f('dune2Rating')]} />,
    primary: {icon: 'i-play', label: f('play'), onPress: none},
    actions: [{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}],
    tagline: f('dune2Tagline'), synopsis: f('dune2Synopsis'),
    credits: [{label: f('directedBy'), names: [{text: f('dune2Director'), onPress: none}]}, {label: f('starring'), names: (['dune2Cast1', 'dune2Cast2', 'dune2Cast3', 'dune2Cast4'] as const).map(name => ({text: f(name), onPress: none}))}],
    picker: <TrackPicker audio={f('audioChoice')} subtitles={f('subtitlesChoice')} label={f('picker')} onPress={none} />,
    tabs: tabs(tab, setTab)};
}
function Film({more = false}: {more?: boolean}) {
  const art = useReferenceArtwork(), [tab, setTab] = useState('more'), [open, setOpen] = useState(more), anchor = useRef<HTMLElement | null>(null);
  // The menu hangs from the frame's own More button (desktop-detail f9).
  useEffect(() => { if (more) anchor.current = document.querySelector<HTMLElement>(`[aria-label="${f('more')}"]`); }, [more]);
  return <Page current="movies"><TitleFrame {...filmFrame(art, tab, setTab)}>
    <Posters title={f('moreLikeDune')} items={moreLikeDune} />
    <Posters title={f('viewersAlso')} note={f('viewersAlsoNote')} items={viewersAlso} />
  </TitleFrame>
  {more && <Menu open={open} onOpenChange={setOpen} title={f('dune2')} anchorRef={anchor} placement="bottom-start">
    <MenuItem icon="i-heart" onSelect={none}>{f('menuFavourite')}</MenuItem><MenuItem icon="i-star" onSelect={none}>{f('menuRate')}</MenuItem><MenuItem icon="i-listadd" onSelect={none}>{f('menuPlaylist')}</MenuItem>
    <MenuItem icon="i-layers" onSelect={none}>{f('menuCollection')}</MenuItem><MenuItem icon="i-dl" onSelect={none}>{f('menuDownload')}</MenuItem>
    <MenuSeparator /><MenuItem icon="i-film" onSelect={none}>{f('menuVersion')}</MenuItem><MenuItem icon="i-info" onSelect={none}>{f('menuFileInfo')}</MenuItem>
    <MenuGroup label={f('ownerOnly')} ownerOnly><MenuItem icon="i-edit" onSelect={none}>{f('menuEdit')}</MenuItem><MenuItem icon="i-refresh" onSelect={none}>{f('menuRefresh')}</MenuItem></MenuGroup>
  </Menu>}</Page>;
}
function FilmResume() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('more');
  return <Page current="movies"><TitleFrame back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={art('bd-insideout.jpg')} />} title={f('insideOut')} metadata={<Meta text={f('insideOutFacts')} tags={[f('insideOutRating')]} />}
    primary={{icon: 'i-play', label: f('resume'), progress: .09, remaining: f('insideOutLeft'), startOver: {label: f('startOver'), onPress: none}, onPress: none}}
    actions={[{icon: 'i-check', label: f('myList'), labelled: true, active: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    tagline={f('insideOutTagline')} synopsis={f('insideOutSynopsis')} credits={[{label: f('directedBy'), names: 'Pete Docter'}, {label: f('starring'), names: 'Amy Poehler, Phyllis Smith, Richard Kind, Bill Hader'}]}
    picker={<TrackPicker audio={f('audioChoice')} subtitles={f('subtitlesChoice')} label={f('picker')} onPress={none} />} tabs={tabs(tab, setTab)}>
    <Posters title={f('moreLikeInsideOut')} items={moreLikeInsideOut} />
  </TitleFrame></Page>;
}

const duneCast: readonly CastPerson[] = (['1', '2', '3', '4', '5', '6'] as const).map(n => ({id: n, name: f(`dune2Cast${n}`), role: f(`dune2Role${n}`), onPress: none}));
const duneCrew: readonly CastPerson[] = (['1', '2', '3', '4'] as const).map(n => ({id: `crew-${n}`, name: f(`dune2Crew${n}`), role: f(`dune2CrewRole${n}`), onPress: none}));
const link = (text: string) => ({text, onPress: none});
function DuneDetails() {
  const art = useReferenceArtwork();
  return <>
    <PanelBlock label={f('about')}><div style={{display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 340px), 1fr))', gap: 'var(--frame-section-gap)'}}>
      <div><PanelHeading title={f('about')} /><AboutTable rows={[{label: f('released'), values: [{text: f('releasedValue')}]}, {label: f('runtime'), values: [{text: f('runtimeValue')}]},
        {label: f('genres'), values: [link(f('genre1')), link(f('genre2'))]}, {label: f('rated'), values: [{text: f('dune2Rating')}]}, {label: f('directedBy'), values: [link(f('dune2Director'))]},
        {label: f('studio'), values: [link(f('studioValue'))]}, {label: f('language'), values: [{text: f('languageValue')}]}, {label: f('added'), values: [{text: f('addedValue')}]}]} /></div>
      <div><PanelHeading title={f('castAndCrew')} trailing={<Button variant="text" size="small" icon="i-right" onClick={none}>{f('seeAllCount')}</Button>} /><CastRow groups={[duneCast, duneCrew]} /></div>
    </div></PanelBlock>
    <PanelBlock label={f('duneCollectionName')}><CollectionBanner model={{title: f('duneCollectionName'), caption: []}} kindLabel={f('duneCollectionKind')} description={f('duneCollectionBody')} actionLabel={f('openCollection')} actionVariant="glass" onAction={none}
      covers={[art('pool/movies/dune.jpg'), art('pool/movies/dune-part-two.jpg')]} artwork={art('pool/movies/dune-part-two--bd.jpg')} onNavigate={none} /></PanelBlock>
  </>;
}
/** Details, held collapsed: the hero gone, the collapsed bar with the title, the tabs, Play and + (desktop-detail f3; phone-detail d1 frame 2). */
function FilmDetails() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('details');
  return <Page current="movies"><TitleFrame {...filmFrame(art, tab, setTab)} collapsed><DuneDetails /></TitleFrame></Page>;
}

/** A show's episodes (desktop-detail f5, tablet f8, phone-detail d2): the season bar and the episode rows. */
type Episode = Readonly<{id: string; title: string; label: string; length: string; date?: string; synopsis: string; progress?: number; watched?: boolean; status?: string; left?: string; art: string}>;
const tlouEpisodes: readonly Episode[] = [
  {id: 'e1', title: f('ep1'), label: f('ep1Label'), length: f('ep1Length'), synopsis: f('ep1Synopsis'), progress: .67, status: f('ep1Status'), left: f('ep1Left'), art: 'pool/tv/the-last-of-us--bd.jpg'},
  {id: 'e2', title: f('ep2'), label: f('ep2Label'), length: f('ep2Length'), date: f('ep2Date'), synopsis: f('ep2Synopsis'), watched: true, art: 'bd-tlou.jpg'},
  {id: 'e3', title: f('ep3'), label: f('ep3Label'), length: f('ep3Length'), date: f('ep3Date'), synopsis: f('ep3Synopsis'), art: 'pool/tv/the-last-of-us--bd.jpg'},
  {id: 'e4', title: f('ep4'), label: f('ep4Label'), length: f('ep4Length'), date: f('ep4Date'), synopsis: f('ep4Synopsis'), art: 'bd-tlou.jpg'},
];
function EpisodeTiles({episodes, layout, opened}: {episodes: readonly Episode[]; layout: 'list' | 'grid'; opened?: string}) {
  const art = useReferenceArtwork();
  // An episode opens under its row: after the last tile of the row it is in (desktop-detail f5), as the show page does.
  const columns = 4, at = opened ? episodes.findIndex(episode => episode.id === opened) : -1;
  const rowEnd = at < 0 ? -1 : layout === 'list' ? at : Math.min(episodes.length - 1, Math.floor(at / columns) * columns + columns - 1);
  return <EpisodeList layout={layout} columns={columns}>{episodes.map((episode, index) => <Fragment key={episode.id}>
    <EpisodeTile className={episodeRowClass} context="detail" artworkKind="still" artwork={art(episode.art)} watched={episode.watched} expanded={episode.id === opened} length={episode.length}
      model={{title: episode.title, caption: [{id: 'episode', text: episode.label, role: 'context'}, ...(episode.status ? [{id: 'continue', text: episode.status, role: 'status' as const}, {id: 'left', text: episode.left!, role: 'context' as const}] : [])], detail: [], progress: episode.progress}}
      airDate={episode.date ? {id: 'air-date', text: episode.date, role: 'context'} : undefined} description={episode.synopsis} onNavigate={none} />
    {index === rowEnd && <EpisodeListOpened><OpenedEpisode /></EpisodeListOpened>}
  </Fragment>)}</EpisodeList>;
}
/** The episode opened in place under its row (desktop-detail f5; phone-detail d2 frame 4): the kit's episode layout. */
function OpenedEpisode() {
  const art = useReferenceArtwork();
  return <EpisodeDetailsLayout title={f('ep3')} artwork={art('pool/tv/the-last-of-us--bd.jpg')} backdrop context={<><b>{f('tlou')}</b> <span>{f('season1')}</span></>}
    facts={<Meta text={`${f('ep3Code')} · ${f('ep3Aired')} · ${f('ep3Length')}`} tags={[f('ep3Rating')]} />} synopsis={f('ep3Synopsis')}
    actions={<div style={{display: 'flex', gap: 'var(--spec-hero-actions-gap-web)'}}><Button variant="primary" icon="i-play">{f('play')}</Button><Button icon="i-plus">{f('myList')}</Button><Button iconOnly icon="i-check" aria-label={f('watched')} /><Button iconOnly icon="i-more" aria-label={f('more')} /></div>}
    adjacent={<><EpisodeNeighbour direction="previous" label={f('previousEpisode')} title={f('ep2')} detail={f('ep2Label')} artwork={art('bd-tlou.jpg')} onPress={none} />
      <EpisodeNeighbour direction="next" label={f('nextEpisode')} title={f('ep4')} detail={f('ep4Label')} artwork={art('bd-tlou.jpg')} onPress={none} /></>}
    credits={<AboutTable rows={[{label: f('guestStars'), values: [link(`${f('guest1')}`), link(f('guest2'))]}, {label: f('directedBy'), values: [link(f('ep3Director'))]}, {label: f('writtenBy'), values: [link(f('ep3Writer'))]}]} />}
    onClose={none} />;
}
function ShowBar({season, count, progress, menu}: {season: string; count: string; progress?: Readonly<{watched: number; total: number; label: string}>; menu?: ReactNode}) {
  const [hide, setHide] = useState(false), [layout, setLayout] = useState<'list' | 'grid'>('list');
  return <SeasonBar season={menu ?? <Chip variant="toolbar" trailingIcon="i-down">{season}</Chip>} count={count} progress={progress}
    actions={<><Chip variant="toolbar" icon="i-sort" iconSize={18} trailingIcon="i-down" trailingSize={16}>{f('episodeOrder')}</Chip><Chip variant="toolbar">{f('filters')}</Chip></>}
    hideWatched={{label: f('hideWatched'), checked: hide, onCheckedChange: setHide}} layout={{value: layout, onValueChange: setLayout, label: f('layout'), listLabel: f('list'), gridLabel: f('tiles')}}
    more={<Button size="small" iconOnly icon="i-more" aria-label={f('seasonMore')} />} />;
}
const showCast: readonly CastPerson[] = [['Pedro Pascal', 'Joel Miller'], ['Bella Ramsey', 'Ellie Williams'], ['Gabriel Luna', 'Tommy Miller'], ['Nick Offerman', 'Bill']].map(([name, role]) => ({id: name!, name: name!, role, onPress: none}));
function showFrame(art: ReturnType<typeof useReferenceArtwork>, tab: string, setTab: (value: string) => void): Omit<TitleFrameProps, 'children'> {
  return {back, topAction: playOn, art: <AmbientArt treatment="title" foreground={art('pool/tv/the-last-of-us--bd.jpg')} />, title: f('tlou'), metadata: <Meta text={f('tlouFacts')} tags={[f('tlouSeasons')]} />,
    primary: {icon: 'i-play', label: f('resumeEpisode'), progress: .67, remaining: f('ep1Left'), onPress: none},
    actions: [{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}],
    synopsis: f('tlouSynopsis'), credits: [{label: f('createdBy'), names: [link('Craig Mazin'), link('Neil Druckmann')]}, {label: f('starring'), names: [link('Pedro Pascal'), link('Bella Ramsey')]}],
    cast: <CastRow groups={[showCast]} />, tabs: tabs(tab, setTab, true)};
}
function Show({opened}: {opened?: string}) {
  const art = useReferenceArtwork(), [tab, setTab] = useState('episodes'), wide = useWide();
  return <Page current="tvShows"><TitleFrame {...showFrame(art, tab, setTab)}>
    <PanelBlock label={f('episodes')}><ShowBar season={f('season1')} count={f('episodeCount')} progress={{watched: 1, total: 4, label: f('seasonProgress')}} />
      <EpisodeTiles episodes={tlouEpisodes} layout={wide ? 'grid' : 'list'} opened={opened} /></PanelBlock>
  </TitleFrame></Page>;
}
/** A long-running show with the season menu open (desktop-detail f6; phone-detail d2 frame 5): tiles, four to a row. */
function ShowSeasons() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('episodes'), [open, setOpen] = useState(true), [selected, setSelected] = useState('s3'), wide = useWide();
  const rows = [{id: 's1', label: f('season1'), years: f('seasonYears1'), count: f('count13'), watched: true}, {id: 's2', label: f('season2'), years: f('seasonYears2'), count: f('count22'), watched: true},
    {id: 's3', label: f('season3'), years: f('seasonYears3'), count: f('season3Count'), progress: 4 / 24}, {id: 's4', label: f('season4'), years: f('seasonYears4'), count: f('count22')},
    {id: 's5', label: f('season5'), years: f('seasonYears5'), count: f('count22')}, {id: 's0', label: f('specials'), count: f('count12'), special: true}];
  const episodes = (['simpsonsEp1', 'simpsonsEp2', 'simpsonsEp3', 'simpsonsEp4'] as const).map((name, index): Episode => ({id: name, title: f(name), label: f((['episodeN1', 'episodeN2', 'episodeN3', 'episodeN4'] as const)[index]!), length: f('simpsonsEpLength'), date: `Sep ${19 + 7 * index}, 1991`, synopsis: f('simpsonsEpSynopsis'), watched: index > 1, art: 'pool/tv/the-simpsons--bd.jpg'}));
  return <Page current="tvShows"><TitleFrame back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={art('pool/tv/the-simpsons--bd.jpg')} />} title={f('simpsons')} metadata={<Meta text={f('simpsonsFacts')} tags={[f('simpsonsSeasons')]} />}
    primary={{icon: 'i-play', label: f('playS3E5'), onPress: none}} actions={[{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    synopsis={f('simpsonsSynopsis')} tabs={tabs(tab, setTab, true)}>
    <PanelBlock label={f('episodes')}><ShowBar season={f('season3')} count={f('season3Count')} progress={{watched: 4, total: 24, label: f('season3Progress')}}
      menu={<SeasonMenu open={open} onOpenChange={setOpen} label={f('season3')} title={f('seasonMenu')} heading={f('seasonsHeading')} rows={rows} selectedId={selected} watchedLabel={f('watched')} onSelect={setSelected}
        jump={{placeholder: f('jumpSeason'), range: f('seasonRange'), maximum: 36, onJump: async () => true}} />} />
      <EpisodeTiles episodes={[...episodes, ...episodes.map(episode => ({...episode, id: `${episode.id}-b`, watched: false}))]} layout={wide ? 'grid' : 'list'} /></PanelBlock>
  </TitleFrame></Page>;
}
/** A season's own page: the show as its kicker, its episodes. */
function Season() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('episodes');
  return <Page current="tvShows"><TitleFrame back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={art('bd-tlou.jpg')} />} kicker={{icon: 'i-tv', label: f('seasonKicker'), onPress: none}} title={f('season1')}
    metadata={<Meta text={f('seasonFacts')} />} primary={{icon: 'i-play', label: f('resumeEpisode'), progress: .67, remaining: f('ep1Left'), onPress: none}}
    actions={[{icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]} synopsis={f('seasonSynopsis')} tabs={tabs(tab, setTab, true)}>
    <PanelBlock label={f('episodes')}><ShowBar season={f('season1')} count={f('episodeCount')} progress={{watched: 1, total: 4, label: f('seasonProgress')}} /><EpisodeTiles episodes={tlouEpisodes} layout="list" /></PanelBlock>
  </TitleFrame></Page>;
}
/** An episode on a page of its own (from Search or a link): the show and season as its kicker, the picker, its guests and neighbours in the panel. */
function EpisodePage() {
  const art = useReferenceArtwork();
  return <Page current="tvShows"><TitleFrame back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={art('pool/tv/the-last-of-us--bd.jpg')} />} kicker={{icon: 'i-tv', label: f('tlou'), detail: f('season1'), onPress: none}} title={f('ep3')}
    metadata={<Meta text={f('episodeFacts')} tags={[f('ep3Rating')]} />} primary={{icon: 'i-play', label: f('play'), onPress: none}}
    actions={[{icon: 'i-plus', label: f('myList'), labelled: true, pinned: true, onPress: none}, {icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    synopsis={f('ep3Synopsis')} credits={[{label: f('directedBy'), names: [link(f('ep3Director'))]}, {label: f('writtenBy'), names: [link(f('ep3Writer'))]}]}
    picker={<TrackPicker audio={f('audioChoice')} subtitles={f('subtitlesChoice')} label={f('picker')} onPress={none} />}>
    <PanelBlock label={f('guestStars')}><PanelHeading title={f('guestStars')} /><CastRow groups={[[{id: 'g1', name: f('guest1'), role: f('guestRole1'), onPress: none}, {id: 'g2', name: f('guest2'), role: f('guestRole2'), onPress: none}]]} /></PanelBlock>
    <PanelBlock label={f('episodes')}><div style={{display: 'flex', flexWrap: 'wrap', gap: 'var(--space-md)'}}>
      <EpisodeNeighbour direction="previous" label={f('previousEpisode')} title={f('ep2')} detail={f('ep2Label')} artwork={art('bd-tlou.jpg')} onPress={none} />
      <EpisodeNeighbour direction="next" label={f('nextEpisode')} title={f('ep4')} detail={f('ep4Label')} artwork={art('bd-tlou.jpg')} onPress={none} /></div></PanelBlock>
  </TitleFrame></Page>;
}

/** A person (desktop-detail f7; phone-detail d3 frame 7): the portrait disc, Acting, born, the biography with Read more, On this server. */
function Person() {
  const art = useReferenceArtwork(), [filter, setFilter] = useState('all');
  const poster = (key: string, title: string, role: string, extra?: string) => <PosterCard key={key} model={{title, caption: [{id: 'role', text: role, role: 'context'}, ...(extra ? [{id: 'extra', text: extra, role: 'context' as const}] : [])]}} artwork={art(key)} style={{width: 'var(--card-poster-width)'}} onNavigate={none} />;
  return <Page current="movies"><TitleFrame back={{label: f('dune2'), onPress: none}} cover={{shape: 'circle', node: <PersonCircle covers={[art('pool/movies/dune-part-two.jpg'), art('pool/movies/dune.jpg'), art('pool/tv/silo.jpg')]} initials={nameInitials(f('person'))} alt="" />}}
    kicker={{label: f('personKicker')}} title={f('person')} metadata={<Meta text={f('personFacts')} />} synopsis={f('personBio')} readMore={{label: f('readMore'), note: f('bioSource'), onPress: none}}>
    <PanelBlock label={f('onThisServer')}>
      <PanelHeading title={f('onThisServer')} count={f('onThisServerCount')} trailing={<>{(['all', 'cast', 'crew'] as const).map(id => <Chip key={id} variant="facet" selected={filter === id} onClick={() => setFilter(id)}>{f(id)}</Chip>)}
        <Chip variant="toolbar" icon="i-sort" trailingIcon="i-down">{f('yearSort')} {f('newestFirst')}</Chip><Chip variant="toolbar">{f('filters')}</Chip></>} />
      <div style={{display: 'flex', flexWrap: 'wrap', gap: 'var(--frame-section-gap)'}}>
        <div><PanelHeading title={f('moviesGroup')} count="2" /><div style={{display: 'flex', gap: 'var(--space-sm)'}}>{poster('pool/movies/dune-part-two.jpg', f('dune2'), f('asJessica'))}{poster('pool/movies/dune.jpg', f('dune'), f('asJessica'))}</div></div>
        <div><PanelHeading title={f('showsGroup')} count="1" /><div style={{display: 'flex', gap: 'var(--space-sm)'}}>{poster('pool/tv/silo.jpg', f('silo'), f('asJuliette'), f('executiveProducer'))}</div></div>
      </div>
      <PanelText tone="note">{f('onlyOnServer')}</PanelText>
    </PanelBlock>
  </TitleFrame></Page>;
}

/** A collection (desktop-detail f8; phone-detail d3 frame 8): Play all, Shuffle, the numbered films in order, the About side. */
function Collection() {
  const art = useReferenceArtwork();
  const film = (number: number, key: string, title: string, made: string, synopsis: string, status: Parameters<typeof NumberedRow>[0]['status'], action: ReactNode, progress?: number) =>
    <NumberedRow key={key} number={number} artwork={art(key)} title={title} facts={<Meta text={made} tags={['PG-13']} />} synopsis={synopsis} status={status} action={action} progress={progress} />;
  return <Page current="movies"><TitleFrame back={back} topAction={playOn} art={<AmbientArt treatment="title" foreground={art('bd-lotr.jpg')} />}
    kicker={{icon: 'i-layers', label: f('collectionKicker'), detail: f('collectionMade')}} title={f('lotr')} metadata={<Meta text={f('lotrFacts')} tags={['PG-13']} />}
    primary={{icon: 'i-play', label: f('playAll'), onPress: none}} actions={[{icon: 'i-shuffle', label: f('shuffle'), labelled: true, onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    synopsis={f('lotrSynopsis')}
    aside={<><div><PanelHeading title={f('about')} /><AboutTable rows={[{label: f('films'), values: [{text: f('filmsValue')}]}, {label: f('years'), values: [{text: f('yearsValue')}]},
      {label: f('directedBy'), values: [link(f('lotrDirector'))]}, {label: f('genres'), values: [link(f('lotrGenre1')), link(f('lotrGenre2'))]}]} /></div>
      <OwnerCard title={f('thisCollection')} mark={f('ownerOnly')} body={f('thisCollectionBody')} actions={[{icon: 'i-edit', label: f('rename'), onPress: none}, {icon: 'i-plus', label: f('addFilm'), onPress: none}, {icon: 'i-sort', label: f('changeOrder'), onPress: none}]} /></>}>
    <PanelBlock label={f('filmsInOrder')}><PanelHeading title={f('filmsInOrder')} note={f('byRelease')} trailing={<Chip variant="toolbar" icon="i-sort" trailingIcon="i-down">{f('collectionOrder')}</Chip>} />
      {film(1, 'pool/movies/the-lord-of-the-rings-the-fellowship-of-the-ring.jpg', f('fellowship'), f('fellowshipFacts'), f('fellowshipSynopsis'), {label: f('watched'), icon: 'i-check'}, <Button size="compact" icon="i-play">{f('playAgain')}</Button>)}
      {film(2, 'pool/movies/the-lord-of-the-rings-the-two-towers.jpg', f('towers'), f('towersFacts'), f('towersSynopsis'), {label: f('upNext'), accent: true}, <Button size="compact" icon="i-play">{`${f('resume')} · ${f('towersLeft')}`}</Button>, .5)}
      {film(3, 'pool/movies/the-lord-of-the-rings-the-return-of-the-king.jpg', f('king'), f('kingFacts'), f('kingSynopsis'), {label: f('notStarted')}, <Button size="compact" icon="i-play">{f('play')}</Button>)}
    </PanelBlock>
  </TitleFrame></Page>;
}

/** An album (desktop-listen album; phone-app 13): the cover, Album, the artist and facts, Play, Shuffle, the songs, the Details side. */
const songs = (names: readonly Name[], album?: string): TrackRowData[] => names.map((name, index) => ({id: name, number: index + 1, text: {id: name, title: f(name), caption: album ? [{id: 'album', text: album, role: 'context'}] : [], detail: []},
  plays: [18, 9, 31, 12][index], duration: {id: 'duration', text: ['4:10', '3:59', '3:31', '3:11'][index]!, role: 'context'}, playing: !album && index === 2, favorite: !album && index === 2}));
function Album() {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Page current="movies"><TitleFrame back={back} cover={{shape: 'square', node: <Artwork source={art('pool/albums/after-hours.jpg')} alt="" />}} kicker={{label: f('albumKicker')}} title={f('album')}
    metadata={<Meta text={`${f('albumArtist')} · ${f('albumFacts')}`} />} primary={{icon: 'i-play', label: f('play'), onPress: none}}
    actions={[{icon: 'i-shuffle', label: f('shuffle'), labelled: true, onPress: none}, {icon: 'i-heart', label: f('favourite'), onPress: none}, {icon: 'i-listadd', label: f('addToPlaylist'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    aside={<><div><PanelHeading title={f('details')} /><AboutTable rows={[{label: f('year'), values: [{text: '2020'}]}, {label: f('released'), values: [{text: 'March 20, 2020'}]}, {label: f('label'), values: [{text: f('labelValue')}]},
      {label: f('type'), values: [{text: f('albumKicker')}]}, {label: f('genre'), values: [link('R&B/Soul')]}, {label: f('added'), values: [{text: 'Oct 17, 2026'}]}]} /></div>
      <OwnerCard title={f('files')} mark={f('ownerOnly')} body={f('filesBody')} actions={[{icon: 'i-folder', label: f('manage'), onPress: none}]} /></>}>
    <PanelBlock label={f('songsLabel')}><TrackTable variant="album" label={f('songsLabel')} platform={phone ? 'phone' : 'desktop'} rows={songs(['song1', 'song2', 'song3', 'song4'])} onPlay={none} onMore={none} /></PanelBlock>
    <Posters title={f('moreByWeeknd')} items={[['pool/albums/after-hours.jpg', f('album'), '2020']]} />
  </TitleFrame></Page>;
}
/** An artist (desktop-listen artist): the round photo at the right, Artist, Play, Shuffle, Top songs, Albums and Details at the side. */
function Artist() {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Page current="movies"><TitleFrame back={back} cover={{shape: 'circle', node: <Artwork source={art('pool/albums/abbey-road-2019-mix.jpg')} alt="" />}} kicker={{label: f('artistKicker')}} title={f('artist')}
    metadata={<Meta text={f('artistFacts')} />} primary={{icon: 'i-play', label: f('play'), onPress: none}}
    actions={[{icon: 'i-shuffle', label: f('shuffle'), labelled: true, onPress: none}, {icon: 'i-heart', label: f('favourite'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    aside={<><div><PanelHeading title={f('albums')} /><SquareCard model={{title: f('abbeyRoad'), caption: year('1969')}} artwork={art('pool/albums/abbey-road-2019-mix.jpg')} style={{width: 200}} onNavigate={none} /></div>
      <div><PanelHeading title={f('details')} /><AboutTable rows={[{label: f('country'), values: [{text: f('countryValue')}]}, {label: f('active'), values: [{text: f('activeValue')}]}, {label: f('genre'), values: [link('Rock')]}, {label: f('type'), values: [{text: f('group')}]}]} /></div></>}>
    <PanelBlock label={f('topSongs')}><PanelHeading title={f('topSongs')} /><TrackTable variant="top-songs" label={f('topSongs')} platform={phone ? 'phone' : 'desktop'} rows={songs(['artistSong1', 'artistSong2', 'artistSong3', 'artistSong4'], f('abbeyRoad'))
      .map(row => ({...row, artwork: <Artwork source={art('pool/albums/abbey-road-2019-mix.jpg')} alt="" />}))} onPlay={none} onMore={none} /></PanelBlock>
  </TitleFrame></Page>;
}
/** An audiobook (desktop-listen book): the cover, the series as its kicker, Resume chapter 6 with the played line, Start over, the chapters, About this book. */
function Book() {
  const art = useReferenceArtwork(), phone = usePhone();
  const chapters: TrackRowData[] = (['chapter1', 'chapter2', 'chapter3', 'chapter4', 'chapter5', 'chapter6', 'chapter7'] as const).map((name, index) => ({id: name, number: index + 1, text: {id: name, title: f(name), caption: [], detail: []},
    duration: {id: 'duration', text: ['27:43', '22:15', '25:10', '26:38', '39:47', '35:19', '27:29'][index]!, role: 'context'}, done: index < 5, current: index === 5, progress: index === 5 ? .52 : undefined,
    remaining: index === 5 ? {id: 'left', text: '17 min left', role: 'context'} : undefined}));
  return <Page current="movies"><TitleFrame back={back} cover={{shape: 'square', node: <Artwork source={art('pool/books/harry-potter-and-the-philosopher-s-stone.jpg')} alt="" />}}
    kicker={{label: f('bookSeries'), detail: f('bookSeriesDetail'), onPress: none}} title={f('book')} metadata={<Meta text={f('bookFacts')} />}
    primary={{icon: 'i-play', label: f('resumeChapter'), progress: .32, remaining: f('bookLeft'), startOver: {label: f('startOver'), onPress: none}, onPress: none}}
    actions={[{icon: 'i-check', label: f('watched'), wide: true, onPress: none}, {icon: 'i-plus', label: f('myList'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]}
    aside={<><div><PanelHeading title={f('aboutBook')} /><PanelText>{f('bookAbout')}</PanelText>
      <AboutTable rows={[{label: f('author'), values: [link(f('authorValue'))]}, {label: f('narrator'), values: [link(f('narratorValue'))]}, {label: f('published'), values: [{text: f('publishedValue')}]}]} /></div>
      <OwnerCard title={f('files')} mark={f('ownerOnly')} body={f('filesBody')} actions={[{icon: 'i-folder', label: f('manage'), onPress: none}]} /></>}>
    <PanelBlock label={f('chapters')}><PanelHeading title={f('chapters')} /><TrackTable variant="chapters" label={f('chapters')} platform={phone ? 'phone' : 'desktop'} rows={chapters} onPlay={none} onMore={none} /></PanelBlock>
  </TitleFrame></Page>;
}

/** No artwork, a long title, Play only: the boxes are the loaded page's (rule 12). */
function Missing() {
  const [tab, setTab] = useState('more');
  return <Page current="movies"><TitleFrame back={back} art={<AmbientArt treatment="title" />} title={f('longTitle')} metadata={<Meta text={f('longFacts')} />} primary={{icon: 'i-play', label: f('play'), onPress: none}} tabs={tabs(tab, setTab)}>
    <Posters title={f('moreLikeThis')} items={moreLikeDune} />
  </TitleFrame></Page>;
}
/** Loading: the card's title and art are known; the facts and words are skeletons, the buttons are in place but say nothing they do not know, the tab panel keeps skeleton cards. */
function Loading() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('more');
  return <Page current="movies"><TitleFrame {...filmFrame(art, tab, setTab)} kicker={undefined} state={{kind: 'loading'}} /></Page>;
}

/** The overlays a title page opens, each on the R9 table's form for its task (SheetFrame), over the page it opens from. */
const dune2Files: readonly FileFacts[] = [
  {id: 'v1', edition: "Director's Cut", available: true, width: 3840, height: 1608, videoCodec: 'hevc', dynamicRange: 'dolby_vision', bitDepth: 10, bitRate: 44_000_000, container: 'mkv', sizeBytes: 61.4 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'truehd', channels: 8, objectAudio: 'atmos'}, {id: 'a2', language: 'en', codec: 'ac3', channels: 6}, {id: 'a3', language: 'fr', codec: 'eac3', channels: 8}, {id: 'a4', language: 'es', codec: 'eac3', channels: 8}],
    subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'en', hearingImpaired: true}, {id: 's3', language: 'fr'}, {id: 's4', language: 'es'}]},
  {id: 'v2', available: true, width: 3840, height: 1608, videoCodec: 'hevc', dynamicRange: 'dolby_vision', bitDepth: 10, bitRate: 41_000_000, container: 'mkv', sizeBytes: 58.2 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'truehd', channels: 8, objectAudio: 'atmos'}, {id: 'a2', language: 'en', codec: 'ac3', channels: 6}], subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'en', hearingImpaired: true}, {id: 's3', language: 'fr'}]},
  {id: 'v3', available: true, width: 1920, height: 804, videoCodec: 'h264', bitRate: 7_000_000, container: 'mkv', sizeBytes: 9.8 * 1024 ** 3, durationSeconds: 9960,
    audio: [{id: 'a1', language: 'en', codec: 'ac3', channels: 6}], subtitles: [{id: 's1', language: 'en'}, {id: 's2', language: 'es'}]},
];
/** Play a different version (desktop-detail 9b, detail-phone 9b). */
function Versions() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('more'), [open, setOpen] = useState(true);
  return <Page current="movies"><TitleFrame {...filmFrame(art, tab, setTab)}><Posters title={f('moreLikeDune')} items={moreLikeDune} /></TitleFrame>
    <SheetFrame task="versions" open={open} onOpenChange={setOpen} title={f('versionTitle')} subtitle={f('dune2')}>
      <VersionRows onChoose={() => setOpen(false)} rows={(['1', '2', '3'] as const).map(n => ({id: `v${n}`, heading: f(`version${n}`), detail: f(`version${n}Detail`)}))} />
    </SheetFrame></Page>;
}
/** File info for a film with three versions (desktop-detail f10, detail-phone 5 frame 11): the kit's File info content on the R9 `details` form. */
function FileInfoCase() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('more'), [open, setOpen] = useState(true), phone = usePhone();
  const model = fileInfoModel(dune2Files, {audience: 'viewer'}, {t: defaultI18n.t}, phone ? 'phone' : undefined);
  return <Page current="movies"><TitleFrame {...filmFrame(art, tab, setTab)}><Posters title={f('moreLikeDune')} items={moreLikeDune} /></TitleFrame>
    <SheetFrame task="details" open={open} onOpenChange={setOpen} title={f('fileInfo')} subtitle={f('dune2')}>
      <FileInfoBody model={model} layout={phone ? 'phone' : 'desktop'} onManage={none} />
    </SheetFrame></Page>;
}
/** The owner's Episode display (desktop-detail f4): a show's Details with the Show settings card; Change opens the choices beside it (R9 `choice`). */
function EpisodeDisplayCase() {
  const art = useReferenceArtwork(), [tab, setTab] = useState('details'), [open, setOpen] = useState(true), change = useRef<HTMLSpanElement>(null);
  const [group, setGroup] = useState('season'), [number, setNumber] = useState('season'), [order, setOrder] = useState('oldest');
  return <Page current="movies"><TitleFrame {...showFrame(art, tab, setTab)} collapsed>
    <PanelBlock label={f('showSettings')}><OwnerCard title={f('showSettings')} mark={f('ownerOnly')} body={f('showSettingsBody')} />
      <div style={{display: 'flex', justifyContent: 'flex-end', marginTop: 'var(--space-sm)'}}><span ref={change}><Button variant="glass" size="small" onClick={() => setOpen(true)}>{f('change')}</Button></span></div></PanelBlock>
    <SheetFrame task="choice" open={open} onOpenChange={setOpen} title={f('episodeDisplay')} anchorRef={change}>
      <EpisodeDisplay title={f('episodeDisplay')} mark={f('forEveryone')} intro={f('episodeDisplayIntro')} preview={{label: f('readsAs'), code: f('ep3Code'), title: f('ep3')}} rows={[
        {label: f('groupEpisodes'), items: [{value: 'season', label: f('bySeason')}, {value: 'one', label: f('inOneList')}, {value: 'ranges', label: f('ranges')}], value: group, onValueChange: setGroup},
        {label: f('numberThem'), items: [{value: 'season', label: f('bySeason')}, {value: 'across', label: f('acrossSeasons')}], value: number, onValueChange: setNumber},
        {label: f('order'), items: [{value: 'oldest', label: f('oldestFirst')}, {value: 'newest', label: f('newestFirst')}], value: order, onValueChange: setOrder}]} />
    </SheetFrame>
  </TitleFrame></Page>;
}

const section = (id: string, label: string, render: ComponentType, height = 900): GalleryCase => ({id, labelKey: `next.platform.frame.${label}` as MessageId, width: 1440, height, render});
export default defineGalleryFamily({id: 'frames-title', labelKey: 'next.platform.frame.titleFamily', cases: [
  section('title-film', 'titleFilm', () => <Film />, 1300),
  section('title-film-resume', 'titleFilmResume', FilmResume),
  section('title-film-more', 'titleFilmMore', () => <Film more />),
  section('title-film-details', 'titleFilmDetails', FilmDetails, 900),
  section('title-show', 'titleShow', () => <Show />, 1300),
  section('title-show-seasons', 'titleShowSeasons', ShowSeasons, 1300),
  section('title-show-episode', 'titleShowEpisode', () => <Show opened="e3" />, 1500),
  section('title-season', 'titleSeason', Season, 1300),
  section('title-episode', 'titleEpisode', EpisodePage),
  section('title-person', 'titlePerson', Person, 1300),
  section('title-collection', 'titleCollection', Collection, 1500),
  section('title-album', 'titleAlbum', Album, 1300),
  section('title-artist', 'titleArtist', Artist, 1300),
  section('title-book', 'titleBook', Book, 1400),
  section('title-missing', 'titleMissing', Missing),
  section('title-loading', 'titleLoading', Loading),
  section('title-versions', 'titleVersions', Versions),
  section('title-file-info', 'titleFileInfo', FileInfoCase),
  section('title-episode-display', 'titleEpisodeDisplay', EpisodeDisplayCase, 1100),
]});
