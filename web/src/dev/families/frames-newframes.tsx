import {useEffect, useMemo, useRef, useState, type CSSProperties, type ComponentType, type ReactNode} from 'react';
import {defaultI18n, formatMessage, type MessageId} from '@i18n';
import {frameForm, frameSpec} from '@design';
import {libraryTabs} from '@core/presentation/library-tabs';
import {browseToolbarModel} from '@portico/client-core/kit-model';
import type {FactPart} from '@portico/client-core/kit-model';
import {newFramesFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/newframes-fixtures.ts';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {AmbientArt, ChannelMark} from '../../kit/artwork';
import {CircleCard, GenreTile, LiveCard, PosterCard, QuickTile, ResumeCard, SquareCard, WideCard} from '../../kit/cards';
import {BrowseToolbar, LibraryMenu, LibrarySwitcher, SongRow} from '../../kit/browse';
import {Button, Chip, Segmented, Switch, Tabs, TextField} from '../../kit/controls';
import {RecordingStorage} from '../../kit/dvr/RecordingsLayout';
import {RuleCard} from '../../kit/dvr/RuleCard';
import {ArtBox, BrowseFrame, LiveFrame, ReaderFrame, SearchFrame, Section, SectionCard, type FramePane} from '../../kit/frames';
import {Shelf} from '../../kit/layout';
import {AiringRow, ChannelRow, ConflictCard, GuideGrid, RecordingRow, type GuideChannel, type GuideCell} from '../../kit/live';
import {usePhone} from '../../kit/overlays/shared';
import {ReaderAnnotations, ReaderPanel} from '../../kit/reader/ReaderPanel';
import {ReaderDisplayPopover, ReaderDisplaySheet} from '../../kit/reader/ReaderDisplay';
import {DirectoryRows, DownloadRow, StorageMeter} from '../../kit/saved';
import {SearchHistory, TopResultCard} from '../../kit/search';
import {SettingsRow, WaitingNotice} from '../../kit/shell';
import {SearchResultHero, TitleFactsLine} from '../../kit/title/TitleParts';
import {Page, f as framesText, useReferenceArtwork} from './frames';
import {capabilities as libraryCapabilities, env as libraryEnv, labels as libraryLabels} from './frames-library';
import page from './frames-newframes.module.css';

/** Round 8: the kinds of screen that had no frame (Search, Live TV, Saved, Downloads, music, audiobooks, the reader), each
 * drawn whole on its frame from the app's own parts with the mock-ups' words and the reference artwork. The native gallery
 * has the same ids. Every case answers to the window's width, so the same case is shot at 390 and at 1440. Serve-only. */
type FixtureName = keyof typeof newFramesFixtureMessages extends `next.newframes.fixture.${infer Name}` ? Name : never;
const f = (name: FixtureName) => formatMessage(newFramesFixtureMessages[`next.newframes.fixture.${name}`]);
const none = () => {};
const fact = (id: string, text: string, role: FactPart['role'] = 'context'): FactPart => ({id, text, role});
/** The reference pool keeps shows apart from films. */
const shows = new Set(['severance', 'the-last-of-us', 'silo', 'slow-horses']);
const posterPath = (id: string) => `pool/${shows.has(id) ? 'tv' : 'movies'}/${id}.jpg`;
const platform = (phone: boolean) => phone ? 'phone-web' as const : 'desktop' as const;

/** A row's value with the chevron that opens its choice. */
const RowValue = ({children}: {children: ReactNode}) => <Button variant="text" size="small" icon="i-right" onClick={none}>{children}</Button>;

// ---- Search ----
function SearchField({value}: {value?: string}) {
  const phone = usePhone();
  return <TextField variant={phone ? 'search' : 'large-search'} type="search" value={value ?? ''} readOnly placeholder={f('searchPlaceholder')} aria-label={f('search')}
    trailing={value ? <Button variant="icon" icon="i-x" iconOnly aria-label={f('clearText')} onClick={none}/> : undefined}/>;
}
const genres = [['genreAnimation', 'titles14', 'bd-spirited'], ['genreSciFi', 'titles16', 'pool/movies/interstellar--bd'], ['genreDrama', 'titles31', 'p-shawshank'], ['genreCrime', 'titles9', 'p-godfather'],
  ['genreComedy', 'titles12', 'bd-rick'], ['genreAdventure', 'titles18', 'bd-lotr'], ['genreAnimation', 'titles14', 'bd-toystory'], ['genreDrama', 'titles31', 'bd-matrix']] as const;
const fans = ['spirited-away', 'inside-out', 'interstellar', 'dune', 'blade-runner', 'alien', 'the-dark-knight', 'barbie', 'gladiator', 'jaws'];
const posterFan = (index: number) => [fans[index % fans.length]!, fans[(index + 3) % fans.length]!];
function GenreTiles({count}: {count: number}) {
  const art = useReferenceArtwork(), phone = usePhone();
  // The live Search page lays its genre tiles out the same way (screens/search/search.module.css .genres): four across, two at phone width.
  return <div style={{display: 'grid', gridTemplateColumns: `repeat(${phone ? 2 : 4}, minmax(0, 1fr))`, gap: 'var(--space-sm)', padding: '0 var(--g)'}}>
    {/* phone-app 7 draws a fan of two posters at phone width; desktop-search s-empty a backdrop. */}
    {genres.slice(0, count).map(([name, titles, path], index) => <GenreTile key={index} visible variant={phone ? 'poster' : 'video'} artwork={art(`${path}.jpg`)} covers={posterFan(index).map(id => art(`pool/movies/${id}.jpg`))}
      model={{title: f(name), caption: [fact('count', f(titles), 'supplement')]}} style={{width: '100%'}} onNavigate={none}/>)}
  </div>;
}
const channels = {
  nn: {id: 'nn', name: f('chNorthwind'), number: '101', favourite: true, mark: {sourceId: 'demo', id: 'nn', text: 'NN', seed: 'demo:nn'}},
  n24: {id: 'n24', name: f('chNorthwind24'), number: '102', mark: {sourceId: 'demo', id: 'n24', text: 'N24', seed: 'demo:n24'}},
  ss: {id: 'ss', name: f('chSummit'), number: '201', mark: {sourceId: 'demo', id: 'ss', text: 'SS', seed: 'demo:ss'}},
  sse: {id: 'sse', name: f('chSummitExtra'), number: '202', mark: {sourceId: 'demo', id: 'sse', text: 'SSE', seed: 'demo:sse'}},
  lm: {id: 'lm', name: f('chLantern'), number: '301', mark: {sourceId: 'demo', id: 'lm', text: 'LM', seed: 'demo:lm'}},
  lc: {id: 'lc', name: f('chClassics'), number: '302', mark: {sourceId: 'demo', id: 'lc', text: 'LC', seed: 'demo:lc'}},
  pk: {id: 'pk', name: f('chPebble'), number: '401', favourite: true, mark: {sourceId: 'demo', id: 'pk', text: 'PK', seed: 'demo:pk'}},
  ad: {id: 'ad', name: f('chAtlas'), number: '501', favourite: true, mark: {sourceId: 'demo', id: 'ad', text: 'AD', seed: 'demo:ad'}},
  qc: {id: 'qc', name: f('chQuiet'), number: '901', mark: {sourceId: 'demo', id: 'qc', text: 'QC', seed: 'demo:qc'}},
} as const;
function OnNowShelf() {
  const phone = usePhone();
  const items = [[channels.nn, 'pWorldTonight', 'range330430', .58], [channels.ss, 'pCycling', 'range330630', .19], [channels.pk, 'pStoryTime', 'r400430', .17], [channels.ad, 'pBridges', 'range330430', .66]] as const;
  return <Shelf heading={f('onNow')} subtitle={f('liveSource')} onSeeAll={none} seeAllLabel={f('guide')} platform={platform(phone)} items={items} getKey={item => item[0].id}
    renderItem={([channel, title, range, progress]) => <LiveCard channel={{sourceId: 'demo', id: channel.id, name: channel.name, number: channel.number, mark: channel.mark}}
      programme={{title: f(title), facts: [fact('range', f(range))], progress, live: channel.id === 'ss'}} style={{width: phone ? 260 : 316}} onNavigate={none}/>}/>;
}
function SearchEmpty() {
  const phone = usePhone();
  return <Page current="search"><SearchFrame title={f('search')} field={<SearchField/>}>
    <SearchHistory compact={phone} title={f('recent')} clear={{label: f('clear'), onPress: none}} items={(['recent1', 'recent2', 'recent3', 'recent4'] as const).slice(0, phone ? 3 : 4).map(name => ({query: f(name), onPress: none, remove: {label: f('remove'), onPress: none}}))}/>
    <Section title={f('browseByGenre')} subtitle={phone ? undefined : f('videoKinds')} trailing={!phone && <Button variant="text" size="small" icon="i-right" onClick={none}>{f('allGenres')}</Button>}><GenreTiles count={phone ? 6 : 8}/></Section>
    <OnNowShelf/>
  </SearchFrame></Page>;
}
function Scopes() {
  const scopes = [['scopeAll', undefined], ['scopeMovies', undefined], ['scopeShows', 1], ['scopeEpisodes', 4], ['scopePeople', undefined], ['scopeArtists', undefined], ['scopeAlbums', undefined], ['scopeSongs', undefined], ['scopeAudiobooks', undefined], ['scopeLive', undefined]] as const;
  return <>{scopes.map(([name, count], index) => <Chip key={name} variant="search" selected={index === 0} count={count} retry={name === 'scopePeople' || name === 'scopeLive'}
    dim={index > 0 && count === undefined && name !== 'scopePeople' && name !== 'scopeLive'} onClick={none}>{f(name)}</Chip>)}</>;
}
function EpisodesShelf() {
  const art = useReferenceArtwork(), phone = usePhone();
  const items = [['episode1', 'episodeCode1', .2], ['episode2', 'episodeCode2', undefined], ['episode3', 'episodeCode3', undefined], ['episode4', 'episodeCode4', undefined]] as const;
  return <Shelf heading={f('episodes')} subtitle="4" platform={platform(phone)} items={items} getKey={item => item[0]}
    renderItem={([title, code, progress]) => <WideCard model={{title: f(title), caption: [fact('show', f('tlou'), 'identity'), fact('code', f(code))], detail: [], progress}} artwork={art('bd-tlou.jpg')} style={{width: 'var(--card-wide-width)'}} onNavigate={none}/>}/>;
}
function SearchResults() {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Page current="search"><SearchFrame field={<SearchField value={f('queryTlou')}/>} cancel={phone && <Button variant="text" size="small" onClick={none}>{f('cancel')}</Button>} scopes={<Scopes/>}
    notice={<WaitingNotice icon="i-clock" action={{id: 'retry', label: f('tryAgain'), onAction: none}}>{f('partial')}</WaitingNotice>}>
    {phone ? <div style={{padding: '0 var(--card)'}}><TopResultCard eyebrow={f('topResult')} title={f('tlou')} facts={<TitleFactsLine parts={f('tlouFacts').split(' · ').map((text, index) => fact(`f${index}`, text, index ? 'context' : 'identity'))}/>}
      background={art('bd-tlou.jpg')} onOpen={none} play={{label: f('play'), onPress: none}}/></div>
    : <div style={{padding: '0 var(--g)'}}><SearchResultHero title={f('tlou')} eyebrow={f('topResult')} facts={<TitleFactsLine parts={f('tlouFacts').split(' · ').map((text, index) => fact(`f${index}`, text, index ? 'context' : 'identity'))}/>}
      synopsis={f('tlouSynopsis')} artwork={<AmbientArt treatment="title" foreground={art('bd-tlou.jpg')} foregroundFrameStyle={{left: '30%', width: '70%', height: '100%'}} position="50% 6%"/>}
      actions={<div style={{display: 'flex', gap: 'var(--space-px-12)', flexWrap: 'wrap'}}><Button variant="primary" icon="i-play" onClick={none}>{f('resumeEpisode')}</Button>
        <Button icon="i-plus" onClick={none}>{f('myList')}</Button><Button icon="i-more" iconOnly aria-label={f('more')} onClick={none}/></div>}/></div>}
    <EpisodesShelf/>
  </SearchFrame></Page>;
}
function SearchNone() {
  return <Page current="search"><SearchFrame field={<SearchField value={f('queryNone')}/>} none={{title: f('noMatches'), body: f('noMatchesBody')}}>
    <Section title={f('browseInstead')}><GenreTiles count={4}/></Section>
  </SearchFrame></Page>;
}

// ---- Live TV ----
const cell = (id: string, title: FixtureName, from: number, to: number, extra: Partial<GuideCell> = {}): GuideCell => ({id, title: f(title), from, to, past: to <= 35, progress: from <= 35 && to > 35 ? (35 - from) / (to - from) : undefined, ...extra});
const live = [{id: 'live' as const, text: f('flagLive')}], fresh = [{id: 'new' as const, text: f('flagNew')}];
const guide: readonly GuideChannel[] = [
  {...channels.nn, cells: [cell('a', 'pWorldTonight', 0, 60, {flags: live, detail: f('range330430')}), cell('b', 'pMorningBriefing', 60, 120, {detail: f('range430530')}), cell('c', 'pMidday', 120, 150)]},
  {...channels.n24, cells: [cell('a', 'pMorningBriefing', 0, 60, {detail: f('range330430')}), cell('b', 'pWorldTonight', 60, 120, {flags: live, detail: f('range430530'), recording: f('recording')}), cell('c', 'pMorningBriefing', 120, 180)]},
  {...channels.ss, cells: [cell('a', 'pCycling', 0, 180, {detail: f('range330630')})]},
  {...channels.sse, cells: [cell('a', 'pCycling', -90, 90, {detail: f('range200500')}), cell('b', 'pCycling', 90, 270, {detail: f('range500800')})]},
  {...channels.lm, cells: [cell('a', 'pGlassSky', -93, 41, {detail: f('range157411')}), cell('b', 'pQuietHarbor', 41, 143, {flags: [{id: 'premiere', text: f('flagPremiere')}], detail: f('range411553')})]},
  {...channels.lc, cells: [cell('a', 'pQuietHarbor', -78, 24), cell('b', 'pLighthouse', 24, 142, {detail: f('range354552')})]},
  {...channels.pk, cells: [cell('a', 'pTinyGardeners', -30, 30), cell('b', 'pStoryTime', 30, 60, {flags: fresh, detail: f('codeS3E1'), recording: f('recording'), selected: true}), cell('c', 'pStoryTime', 60, 90, {detail: f('codeS5E2')}), cell('d', 'pTinyGardeners', 90, 120)]},
  {...channels.ad, cells: [cell('a', 'pWildPlains', -30, 15), cell('b', 'pBridges', 15, 45, {detail: f('codeS4E12')}), cell('c', 'pClockmakers', 45, 105, {detail: f('codeS5E9')}), cell('d', 'pWildPlains', 105, 150)]},
  {...channels.qc, cells: [cell('a', 'noGuide', 0, 240, {empty: true})]},
];
const ruler = (['time330', 'time400', 'time430', 'time500', 'time530', 'time600'] as const).map((label, index) => ({at: index * 30, label: f(label)}));
const Grid = () => <GuideGrid channels={guide} span={210} now={35} ruler={ruler} nowLabel={f('nowTime')} channelLabel={f('channel')} onCell={none}/>;
function Groups() {
  const groups = [['groupAll', 14], ['groupFavorites', 3], ['groupNews', 2], ['groupSports', 2], ['groupMovies', 2], ['groupKids', 1], ['groupDocumentary', 1]] as const;
  return <>{groups.map(([name, count], index) => <Chip key={name} variant="search" selected={index === 0} count={count} icon={name === 'groupFavorites' ? 'i-star' : undefined} onClick={none}>{f(name)}</Chip>)}</>;
}
function Switcher() {
  // phone-guide g1 draws the source picker as a chip at the head of the groups.
  return usePhone() ? <Chip trailingIcon="i-chdown" onClick={none}>{f('allChannels')}</Chip> : <Button size="small" icon="i-chdown" onClick={none}>{f('allChannels')}</Button>;
}
function GuideTools() {
  const phone = usePhone();
  return <>
    <Button size="small" icon="i-cal" onClick={none}>{f(phone ? 'todayShort' : 'today')}</Button>
    {!phone && <><Button size="small" icon="i-left" onClick={none}>{f('back2h')}</Button><Button size="small" onClick={none}>{f('now')}</Button><Button size="small" icon="i-right" onClick={none}>{f('ahead2h')}</Button></>}
    <span style={{flex: 1}}/>
    <Button variant="text" size="small" icon="i-chdown" onClick={none}>{f('sortByNumber')}</Button>
    {!phone && <Segmented value="grid" onValueChange={none} items={[{value: 'grid', label: f('grid'), icon: 'i-grid'}, {value: 'list', label: f('list'), icon: 'i-list'}]}/>}
  </>;
}
function PhoneActions({list = false}: {list?: boolean}) {
  return <><Segmented value={list ? 'list' : 'guide'} onValueChange={none} items={[{value: 'list', label: f('list')}, {value: 'guide', label: f('guide')}]}/><Button variant="icon" icon="i-rec" iconOnly aria-label={f('tabRecordings')} onClick={none}/></>;
}
const LiveTabs = ({value}: {value: 'guide' | 'recordings'}) => <Tabs value={value} onValueChange={none} items={[{value: 'guide', label: f('tabGuide')}, {value: 'recordings', label: <>{f('tabRecordings')} <small style={{color: 'var(--color-record)'}}>{f('conflictTab')}</small></>}]}/>;
function PaneChannel() {
  return <div style={{display: 'flex', alignItems: 'center', gap: 'var(--space-xs)'}}><ChannelMark mark={channels.pk.mark} size={40}/>
    <span style={{flex: 1, display: 'grid'}}><b>{channels.pk.name} ★</b><small style={{color: 'var(--color-text-tertiary)'}}>{f('pebbleLine')}</small></span><small style={{color: 'var(--color-record)'}}>● {f('onNowMark')}</small></div>;
}
function ProgrammeBody() {
  return <>
    <p style={{margin: 0}}>{f('storyFacts')}</p>
    <span style={{display: 'block', height: 3, borderRadius: 2, background: 'var(--color-raised-high)'}}><i style={{display: 'block', width: '17%', height: '100%', background: 'var(--color-accent-strong)'}}/></span>
    <small style={{color: 'var(--color-text-tertiary)'}}>{f('startedAgo')}</small>
    <p style={{margin: 0, color: 'var(--color-text-secondary)'}}>{f('storySynopsis')}</p>
    <div style={{display: 'flex', flexWrap: 'wrap', gap: 'var(--space-xs)'}}><Button variant="primary" icon="i-play" onClick={none}>{f('watchLive')}</Button><Button icon="i-rec" onClick={none}>{f('recording')}</Button><Button icon="i-repeat" onClick={none}>{f('recordSeries')}</Button></div>
    <Section title={f('moreAirings')}>
      {([['t430', 'codeS5E2short', 'airFirstLight', 'r430500'], ['t530', 'codeS2E4', 'airS2E4', 'r530600'], ['t745', 'codeS5E7', 'airS5E7', 'r745815'], ['t1000', 'codeS6E5', 'airS6E5', 'r10001030']] as const).map(([time, code, title, range]) =>
        <AiringRow key={time} time={f(time)} code={f(code)} title={f(title)} detail={f(range)} onPress={none} trailing={<Button variant="icon" icon="i-bell" iconOnly aria-label={f('remind')} onClick={none}/>}/>)}
      <small style={{color: 'var(--color-text-tertiary)'}}>{f('reminderNote')}</small></Section>
  </>;
}
const programmePane = (): FramePane => ({title: f('pStoryTime'), subtitle: f('codeS3E1'), eyebrow: <PaneChannel/>, close: {label: f('close'), onPress: none}, children: <ProgrammeBody/>});
function LiveGuide({details = false}: {details?: boolean}) {
  const phone = usePhone();
  return <Page current="liveTvShort"><LiveFrame title={f('liveTv')} switcher={<Switcher/>} facts={<small style={{color: 'var(--color-text-tertiary)'}}>{f('liveFacts')}</small>} actions={phone && <PhoneActions/>}
    tabs={!phone && <LiveTabs value="guide"/>} chips={<Groups/>} lead={!phone && !details && <OnNowShelf/>} toolbar={<GuideTools/>} details={details ? programmePane() : undefined} hints={!phone && f('keyboardHint')}>
    <Grid/>
  </LiveFrame></Page>;
}
function LiveList() {
  const phone = usePhone();
  const rows = [[channels.nn, 'pWorldTonight', 'left30', .5, 'nextBriefing', false], [channels.n24, 'pMorningBriefing', 'left30', .5, 'nextWorld', false], [channels.ss, 'pCycling', 'left150', .17, 'nextLocker', true], [channels.lm, 'pGlassSky', 'left11', .88, 'nextHarbor', false]] as const;
  return <Page current="liveTvShort"><LiveFrame title={f('liveTv')} switcher={<Switcher/>} actions={phone && <PhoneActions list/>} tabs={!phone && <LiveTabs value="guide"/>} chips={<Groups/>}
    toolbar={<><small style={{color: 'var(--color-text-secondary)'}}>{f('onNowAt')}</small><span style={{flex: 1}}/><Button variant="text" size="small" icon="i-chdown" onClick={none}>{f('sortByNumber')}</Button></>}>
    <div>{rows.map(([channel, title, left, progress, next, isLive]) => <ChannelRow key={channel.id} channel={channel} title={f(title)} left={f(left)} progress={progress} next={f(next)} live={isLive ? f('flagLive') : undefined} onPress={none}/>)}</div>
  </LiveFrame></Page>;
}
/** Recordings: from 600 a tab of Live TV with its views as chips and the storage line at their end; at phone width its own page. */
function recordings(value: 'upcoming' | 'recorded' | 'rules', phone: boolean) {
  const chips = [['upcoming', 'upcoming', 8], ['recorded', 'recorded', 20], ['rules', 'seriesRules', 4], ['history', 'history', undefined], ['storage', 'storage', undefined]] as const;
  if (phone) return {title: f('recordings'), back: {label: f('liveTv'), onPress: none}, tabs: <Tabs value={value} onValueChange={none} items={[{value: 'upcoming', label: f('upcoming')}, {value: 'recorded', label: f('recorded')}, {value: 'rules', label: f('seriesRules')}, {value: 'history', label: f('history')}]}/>};
  return {title: f('liveTv'), switcher: <Switcher/>, facts: <small style={{color: 'var(--color-text-tertiary)'}}>{f('liveFacts')}</small>, tabs: <LiveTabs value="recordings"/>,
    chips: <>{chips.map(([id, name, count]) => <Chip key={id} variant="search" selected={id === value} count={count} onClick={none}>{f(name)}</Chip>)}<span style={{flex: 1}}/>
      <RecordingStorage used={f('storageUsed')} free={f('storageFree')} remaining={f('storageNote')} label={f('storageUsed')} fraction={.14}/></>};
}
function LiveUpcoming() {
  const art = useReferenceArtwork(), phone = usePhone(), more = {label: f('more'), onPress: none};
  return <Page current="liveTvShort"><LiveFrame {...recordings('upcoming', phone)}>
    <Section title={f('conflicts')}><ConflictCard title={f('conflictTitle')} body={f('conflictBody')} action={{label: f('resolve'), onPress: none}}>
      <RecordingRow time={f('t800')} range={f('r800900')} title={f('football')} flags={live} channel={channels.sse} source={f('seriesRule')} artwork={art('bd-solo.jpg')} status={{text: f('tunerConflict'), tone: 'record'}}/>
    </ConflictCard></Section>
    <Section title={f('recordingNow')}><RecordingRow time={f('tNow')} range={f('r400430')} title={f('pStoryTime')} flags={fresh} detail={f('codeS3E1')} channel={channels.pk} source={f('oneTime')} artwork={art('bd-toystory.jpg')} status={{text: f('recording'), tone: 'record'}} more={more} onPress={none}/></Section>
    <Section title={f('laterToday')}>
      <RecordingRow time={f('t415')} range={f('r415500')} title={f('pClockmakers')} detail={f('codeS5E9')} channel={channels.ad} source={f('seriesRule')} artwork={art('bd-matrix.jpg')} status={phone ? {text: f('series'), tone: 'accent'} : undefined} more={more} onPress={none}/>
      <RecordingRow time={f('t430')} range={f('range430530')} title={f('pWorldTonight')} flags={live} channel={channels.n24} source={f('seriesRule')} artwork={art('bd-sev.jpg')} status={phone ? {text: f('series'), tone: 'accent'} : undefined} more={more} onPress={none}/>
      <RecordingRow time={f('t553')} range={f('r553820')} title={f('pLongRoad')} detail={f('year1997')} channel={channels.lm} source={f('oneTime')} artwork={art('bd-lotr.jpg')} more={more} onPress={none}/>
    </Section>
    {phone && <StorageMeter used={f('storageUsed')} free={f('storageFree')} value={.14} note={f('storageNote')}/>}
  </LiveFrame></Page>;
}
/** desktop-live live-recorded: each show a live card with its channel, its corner badge or unwatched count, and its line. */
const recordedShows = [[channels.ad, 'pClockmakers', 'recordings6', undefined, 2], [channels.pk, 'pStoryTime', 'recordings3', undefined, 2], [channels.n24, 'pWorldTonight', 'yesterday430', undefined, undefined],
  [channels.lm, 'pQuietHarbor', 'quietHarborFacts', 'kept', undefined], [channels.ad, 'pWildPlains', 'recordingsThree', 'kept', undefined], [channels.ss, 'pCycling', 'today130', 'incomplete', undefined]] as const;
/** The line under a recorded show: its parts, the unwatched one in accent. */
const recordedLine = (line: string) => line.split(' · ').map((text, index) => fact(`line${index}`, text, / unwatched$/.test(text) ? 'status' : 'context'));
function LiveRecorded() {
  const phone = usePhone();
  return <Page current="liveTvShort"><LiveFrame {...recordings('recorded', phone)} toolbar={<><small style={{color: 'var(--color-text-secondary)'}}>{f('recordedCounts')}</small><span style={{flex: 1}}/><Button variant="text" size="small" icon="i-chdown" onClick={none}>{f('sortRecent')}</Button></>}>
    <div className={page.cardGrid}>
      {recordedShows.map(([channel, title, line, badge, count]) => <LiveCard key={title} channel={{sourceId: 'demo', id: channel.id, name: channel.name, mark: channel.mark}} programme={{title: f(title), facts: []}}
        caption={recordedLine(f(line))} badge={badge && f(badge)} count={count} style={{width: '100%'}} onNavigate={none}/>)}
    </div>
  </LiveFrame></Page>;
}
function RuleBody() {
  const [on, setOn] = useState(true);
  return <>
    <SectionCard><SettingsRow label={f('ruleOn')} help={f('ruleOnHelp')}><Switch checked={on} onCheckedChange={setOn} aria-label={f('ruleOn')}/></SettingsRow></SectionCard>
    <SectionCard>
      <SettingsRow label={f('recordLabel')}><Segmented value="all" onValueChange={none} items={[{value: 'new', label: f('newEpisodes')}, {value: 'all', label: f('allEpisodes')}]}/></SettingsRow>
      <SettingsRow label={f('channelLabel')}><Segmented value="any" onValueChange={none} items={[{value: 'this', label: f('thisChannel')}, {value: 'any', label: f('anyChannel')}]}/></SettingsRow>
      <SettingsRow label={f('keep')}><RowValue>{f('keepEvery')}</RowValue></SettingsRow>
      <SettingsRow label={f('startEarly')}><RowValue>{f('twoMinutes')}</RowValue></SettingsRow>
      <SettingsRow label={f('endLate')}><RowValue>{f('none')}</RowValue></SettingsRow>
    </SectionCard>
    <Section title={f('comingUp')}>
      {([['t415', 'codeS5E9'], ['t645', 'codeS4E12'], ['t800', 'codeS3E1']] as const).map(([time, code]) => <AiringRow key={time} time={f(time)} title={f(code)} detail={`${f('airingsToday')} · ${channels.ad.name}`}/>)}</Section>
  </>;
}
function LiveRules() {
  const phone = usePhone();
  const rules = [['pClockmakers', ['allEpisodes', 'anyChannel', 'keepEvery'], 'ruleNextClock', false, 'upcoming3', 'documentary'], ['pWorldTonight', ['allEpisodes', 'chNorthwind24', 'keepEvery'], 'ruleNextWorld', false, 'upcoming2', 'news'],
    ['summitFootball', ['allEpisodes', 'anyChannel', 'keepEvery'], 'ruleNextFootball', true, 'upcoming1', 'sport'], ['pWildPlains', ['newEpisodes', 'chAtlas', 'keepEvery'], 'ruleNextPlains', false, undefined, 'documentary']] as const;
  return <Page current="liveTvShort"><LiveFrame {...recordings('rules', phone)} details={{title: f('pClockmakers'), subtitle: f('ruleCounts'), eyebrow: <small style={{color: 'var(--color-accent-strong)'}}>{f('seriesRule')}</small>, close: {label: f('close'), onPress: none},
    children: <RuleBody/>, footer: <><Button variant="primary" size="small" onClick={none}>{f('save')}</Button><Button size="small" onClick={none}>{f('cancel')}</Button><span style={{flex: 1}}/><Button variant="text" size="small" onClick={none}>{f('deleteRule')}</Button></>}}>
    <div style={{display: 'grid', gap: 'var(--space-xs)', padding: '0 var(--card)'}}>{rules.map(([title, options, next, conflict, upcoming, tone], index) =>
      <RuleCard key={title} title={f(title)} glyph={f(title).replace(/^The /, '')[0] ?? ''} tone={tone} selected={index === 0} paused={index === 3} options={options.map(option => f(option))} next={f(next)} conflict={conflict}
        state={f(index === 3 ? 'paused' : 'on')} upcoming={upcoming && f(upcoming)} actionsLabel={f('more')} onOpen={none}/>)}</div>
  </LiveFrame></Page>;
}

// ---- Saved, Downloads, Music and Audiobooks: named uses of BrowseFrame (its header, tabs, toolbar, grid and rows regions) ----
const listed = [['dune-part-two', 'Dune: Part Two', '2024 · Movie'], ['severance', 'Severance', 'Show · 1 season'], ['the-last-of-us', 'The Last of Us', 'S1 E1 · 1 h 4 min left'], ['spirited-away', 'Spirited Away', '2001 · Movie'],
  ['interstellar', 'Interstellar', '2014 · Movie'], ['silo', 'Silo', 'Show · 1 season'], ['blade-runner', 'Blade Runner', '1982 · Movie'], ['slow-horses', 'Slow Horses', 'Show · 1 season'], ['oppenheimer', 'Oppenheimer', '2023 · Movie'],
  ['alien', 'Alien', '1979 · Movie'], ['inside-out', 'Inside Out', '2015 · Movie'], ['the-dark-knight', 'The Dark Knight', '2008 · Movie'], ['barbie', 'Barbie', '2023 · Movie'], ['gladiator', 'Gladiator', '2000 · Movie']] as const;
const caption = (text: string) => text.split(' · ').map((part, index) => fact(`c${index}`, part, index ? 'context' : 'identity'));
function ListedPoster({item: [id, name, line]}: {item: typeof listed[number]}) {
  const art = useReferenceArtwork();
  return <PosterCard visible model={{title: name, caption: caption(line), progress: id === 'the-last-of-us' ? .18 : undefined}} artwork={art(posterPath(id))} style={{width: '100%'}} context="grid" density="grid" onNavigate={none}/>;
}
const savedTabs = (value: string) => ({value, onValueChange: none, items: (['myList', 'favorites', 'playlists', 'collections', 'savedViews', 'history', 'downloads'] as const).map(name => ({value: name, label: f(name)}))});
/** My List's Filters bar: the library's own toolbar (Filters, quick chips, the count, sort, grid or list). */
function SavedToolbar() {
  const phone = usePhone(), model = useMemo(() => browseToolbarModel(libraryCapabilities, 'movie', [], {field: 'added', direction: 'desc'}, libraryEnv), []);
  return <BrowseToolbar model={model} labels={libraryLabels} platform={phone ? 'phone' : 'desktop'} resultCount={f('titles18Count')} topInset={0} onFilters={none} onQuickChange={none} onSortChange={none} view={{value: 'grid', onValueChange: none}}/>;
}
/** A bounded grid of cards in the page's flow, at the library grid's own column width and gaps (frameSpec.browse.grid). My List's
 * grid is followed by its Music and audiobooks row, so it is not the self-scrolling `grid` region. */
function CardGrid({children}: {children: ReactNode}) {
  const form = frameForm(typeof window === 'undefined' ? 1440 : window.innerWidth), grid = frameSpec.browse.grid;
  return <div className={page.cards} style={{'--grid-card': `${grid.card[form]}px`, columnGap: grid.columnGap[form], rowGap: grid.rowGap[form]} as CSSProperties}>{children}</div>;
}
function Saved() {
  const art = useReferenceArtwork(), phone = usePhone();
  const listening = [['book', 'project-hail-mary-unabridged', 'bHailMary', 'wWeir'], ['book', 'the-hobbit', 'bHobbit', 'wTolkien'], ['album', 'rumours', 'aRumours', 'rFleetwood'], ['album', 'random-access-memories', 'aRam', 'rDaftPunk']] as const;
  // At phone width Saved is a page of entries (phone-web 6, phone-app 9): My List's row, then the other lists, each opening its own page.
  if (phone) return <Page current="saved"><BrowseFrame header={{title: f('saved')}}>
    <Shelf heading={f('myList')} subtitle={f('titles12')} onSeeAll={none} platform="phone-web" items={listed.slice(0, 6)} getKey={item => item[0]}
      renderItem={([id, name, line]) => <PosterCard model={{title: name, caption: caption(line)}} artwork={art(posterPath(id))} style={{width: 'var(--card-poster-width)'}} onNavigate={none}/>}/>
    <div>
      <DirectoryRows rows={[{id: 'downloads', label: f('downloads'), icon: 'i-dl', caption: f('webDownloadsLine'), onPress: none}]}/>
      <DirectoryRows rows={([['favorites', 'i-heart', 'count24'], ['playlists', 'i-queue', 'count3'], ['collections', 'i-stack', 'count1'], ['savedViews', 'i-filter', 'count2'], ['history', 'i-clock', undefined]] as const)
        .map(([id, icon, count]) => ({id, label: f(id), icon, count: count && f(count), onPress: none}))}/>
    </div>
  </BrowseFrame></Page>;
  return <Page current="saved"><BrowseFrame header={{title: f('saved'), tabs: savedTabs('myList')}} toolbar={<SavedToolbar/>}>
    <CardGrid>{listed.map(item => <ListedPoster key={item[0]} item={item}/>)}</CardGrid>
    <Shelf heading={f('musicAndBooks')} subtitle="4" platform="desktop" items={listening} getKey={item => item[1]}
      renderItem={([kind, id, name, by]) => <SquareCard variant={kind === 'book' ? 'audiobook' : 'music'} model={{title: f(name), caption: [fact('by', f(by), 'identity')]}} artwork={art(`pool/${kind === 'book' ? 'books' : 'albums'}/${id}.jpg`)} style={{width: 'var(--card-square-width)'}} onNavigate={none}/>}/>
  </BrowseFrame></Page>;
}
/** The web's Downloads tab: what the server has prepared for this browser to save (collections-34; collections-36 at phone
 * width). A device's own downloads, with storage and Rules, are the apps' (tablet 11b, the native case). */
function Downloads() {
  const art = useReferenceArtwork(), phone = usePhone(), more = {label: f('more'), onPress: none};
  const save = <Button variant="primary" size="small" icon="i-dl" onClick={none}>{f('saveFile')}</Button>;
  const stop = <Button size="small" onClick={none}>{f('stopPreparing')}</Button>;
  return <Page current="saved"><BrowseFrame header={phone ? {title: f('downloads'), back: {label: f('saved'), onPress: none}} : {title: f('saved'), tabs: savedTabs('downloads')}}>
    <Section title={f('prepared')} subtitle={f('preparedUsage')}>
      {!phone && <div className={page.inset}><WaitingNotice icon="i-info">{f('browserNote')}</WaitingNotice></div>}
      <div className={page.list}>
        <DownloadRow title={f('duneTwo')} detail={f('duneTwoFacts')} artwork={art(posterPath('dune-part-two'))} status={{text: f('readyToSave'), tone: 'ready', note: f('keptUntil')}} action={save} more={more} onPress={none}/>
        <DownloadRow title={f('spirited')} detail={f('spiritedFacts')} artwork={art(posterPath('spirited-away'))} status={{text: f('readyToSave'), tone: 'ready', note: f('keptUntil')}} action={save} more={more} onPress={none}/>
        <DownloadRow title={f('failuresContagious')} detail={f('failuresWebFacts')} artwork={art(posterPath('slow-horses'))} status={{text: f('preparing64'), note: f('minutesLeft4')}} progress={.64}
          action={<><Button size="small" icon="i-pause" onClick={none}>{f('pause')}</Button>{stop}</>} onPress={none}/>
        <DownloadRow title={f('interstellar')} detail={f('interstellarFacts')} artwork={art(posterPath('interstellar'))} status={{text: f('waiting'), note: f('waitingFirst')}} action={stop} onPress={none}/>
        <DownloadRow title={f('titanic')} detail={f('titanicFacts')} artwork={art(posterPath('blade-runner'))} status={{text: f('expired'), tone: 'warning', note: f('expiredHelp')}} retry={{label: f('tryAgain'), onPress: none}} more={more} onPress={none}/>
      </div>
    </Section>
    <Section title={f('wholeShows')}><div className={page.list}>
      <DownloadRow title={f('severanceSeason')} detail={f('severanceFacts')} artwork={art(posterPath('severance'))} status={{text: f('ready3of9')}} progress={.33} action={<><Button size="small" icon="i-pause" onClick={none}>{f('pause')}</Button><Button size="small" onClick={none}>{f('cancel')}</Button></>} onPress={none}/>
      <DownloadRow title={f('silo')} detail={f('siloFacts')} artwork={art(posterPath('silo'))} status={{text: f('ready9of10'), note: f('couldNotPrepare')}} action={<Button size="small" onClick={none}>{f('cancel')}</Button>} onPress={none}/>
    </div></Section>
    <div className={page.inset}><Button size="small" onClick={none}>{f('showMore')}</Button></div>
  </BrowseFrame></Page>;
}

/** A library's header (frameRegions.browse `header.*`): the switcher in the title (centred at phone width), ⋯, the tabs the
 * library's kind has (client-core `libraryTabs`), and Shuffle all for Music. */
function useListenHeader(kind: 'music' | 'audiobook', tab: string) {
  const phone = usePhone(), [library, setLibrary] = useState(kind === 'music' ? 'music' : 'audiobooks'), [current, setCurrent] = useState(tab);
  const libraries = (['movies', 'tvShows', 'anime', 'music', 'audiobooks'] as const).map(id => ({id, name: id === 'music' ? f('music') : id === 'audiobooks' ? f('audiobooks') : framesText(id)}));
  return {title: libraries.find(entry => entry.id === library)!.name, back: {label: f('libraries'), onPress: none},
    switcher: <LibrarySwitcher libraries={libraries} value={library} onValueChange={setLibrary} compact={phone}/>,
    menu: <LibraryMenu owner onSaveView={none} onMarkAll={none} onEditPins={none} onLibrarySettings={none} onScan={none}/>,
    trailing: kind === 'music' && !phone ? <Button icon="i-shuffle" onClick={none}>{f('shuffleAll')}</Button> : undefined,
    counts: f(kind === 'music' ? 'musicCounts' : 'bookCounts').split(' · ').map((text, index) => fact(`count${index}`, text)),
    tabs: {value: current, onValueChange: setCurrent, items: libraryTabs(kind).map(item => ({value: item.id, label: defaultI18n.t(item.label)}))}};
}
const albums = [['after-hours', 'aAfterHours', 'rWeeknd'], ['damn', 'aDamn', 'rKendrick'], ['rumours', 'aRumours', 'rFleetwood'], ['abbey-road-2019-mix', 'aAbbey', 'rBeatles'],
  ['hit-me-hard-and-soft', 'aHitMe', 'rBillie'], ['random-access-memories', 'aRam', 'rDaftPunk'], ['1989-taylor-s-version-deluxe', 'a1989', 'rTaylor'], ['thriller', 'aThriller', 'rJackson'], ['the-wall', 'aWall', 'rFloyd']] as const;
const songs = [['sHardest', 'd331', 'after-hours', 'rWeeknd'], ['sAlone', 'd410', 'after-hours', 'rWeeknd'], ['sTooLate', 'd359', 'after-hours', 'rWeeknd'], ['sDreams', 'd414', 'rumours', 'rFleetwood']] as const;
function Songs({count = 4}: {count?: number}) {
  const art = useReferenceArtwork();
  return <div className={page.inset}>{songs.slice(0, count).map(([title, duration, path, by], index) => <SongRow key={title} context="search" onPlay={none}
    row={{id: title, playing: index === 0, artwork: <ArtBox kind="square" icon="i-music" source={art(`pool/albums/${path}.jpg`)}/>, duration: fact('duration', f(duration)), text: {id: title, title: f(title), caption: [fact('artist', f(by))], detail: []}}}/>)}</div>;
}
/** Recently played: quick tiles two rows deep, as many across as tiles of about their own width fit, two to four
 * (final3-desktop-12, phone-app 15a; the native gallery's rule). */
function RecentlyPlayed() {
  const art = useReferenceArtwork(), width = typeof window === 'undefined' ? 1440 : window.innerWidth;
  const columns = Math.min(4, Math.max(2, Math.round(width / frameSpec.quickTile.width[frameForm(width)])));
  return <Section title={f('recentlyPlayed')}><div className={page.tiles} style={{'--columns': columns} as CSSProperties}>{albums.slice(0, columns * 2).map(([id, name, by]) =>
    <QuickTile key={id} visible model={{title: f(name), caption: [fact('by', f(by), 'identity')]}} artwork={art(`pool/albums/${id}.jpg`)} playing={id === 'after-hours'} onPause={none} onNavigate={none}/>)}</div></Section>;
}
function Music({tab = 'discover'}: {tab?: 'discover' | 'songs'}) {
  const art = useReferenceArtwork(), phone = usePhone(), header = useListenHeader('music', tab), shelf = platform(phone);
  const artists = albums.slice(0, 8).map(([id, , by]) => ({id, by}));
  const newAlbums = <Shelf key="albums" heading={f('newAlbums')} subtitle={f('lastSevenDays')} onSeeAll={none} platform={shelf} items={albums} getKey={item => item[0]}
    renderItem={([id, name, by]) => <SquareCard variant="music" model={{title: f(name), caption: [fact('by', f(by), 'identity')]}} artwork={art(`pool/albums/${id}.jpg`)} style={{width: 'var(--card-square-width)'}} onNavigate={none}/>}/>;
  const artistRow = <Shelf key="artists" heading={f('artists')} onSeeAll={none} platform={shelf} items={artists} getKey={item => item.by}
    renderItem={({id, by}) => <CircleCard model={{title: f(by), caption: [fact('albums', f('oneAlbum'))]}} covers={[art(`pool/albums/${id}.jpg`)]} style={{width: 'var(--card-square-width)'}} onNavigate={none}/>}/>;
  return <Page current="music"><BrowseFrame header={header}>
    {tab === 'songs' ? <Songs/> : <>
      <RecentlyPlayed/>
      {phone ? [artistRow, newAlbums] : [newAlbums, artistRow]}
      {!phone && <Section title={f('playMost')} subtitle={f('last30')}><Songs count={3}/></Section>}
    </>}
  </BrowseFrame></Page>;
}
function Audiobooks() {
  const art = useReferenceArtwork(), phone = usePhone(), shelf = platform(phone), header = useListenHeader('audiobook', 'discover');
  const going = [['the-martian-unabridged', 'bMartian', 'wWeir', 'chapter15', 'left434', .58], ['harry-potter-and-the-philosopher-s-stone', 'bPotter', 'wRowling', 'chapter6', 'left533', .32], ['the-hobbit', 'bHobbit', 'wTolkien', 'chapter6', 'left432', .62]] as const;
  const added = [['the-martian-unabridged', 'bMartian', 'wWeir'], ['the-hunger-games-special-edition', 'bHunger', 'wCollins'], ['the-hobbit', 'bHobbit', 'wTolkien'], ['project-hail-mary-unabridged', 'bHailMary', 'wWeir'],
    ['pride-and-prejudice', 'bPride', 'wAusten'], ['harry-potter-and-the-philosopher-s-stone', 'bPotter', 'wRowling'], ['dune', 'bDune', 'wHerbert']] as const;
  const authors = [['iWeir', 'wWeir', 'twoBooks'], ['iHerbert', 'wHerbert', 'oneBook'], ['iRowling', 'wRowling', 'oneBook'], ['iTolkien', 'wTolkien', 'oneBook']] as const;
  const square = {width: 'var(--card-square-width)'};
  return <Page current="audiobooks"><BrowseFrame header={header}>
    {/* Continue listening: the wide resume card from tablet width (desktop-listen books), square covers with time left on a phone (phone-app 15c). */}
    <Shelf heading={f('continueListening')} platform={shelf} items={phone ? going : going.slice(0, 2)} getKey={item => item[0]}
      renderItem={([id, name, by, chapter, left, progress]) => phone
        ? <SquareCard variant="audiobook" model={{title: f(name), caption: [fact('left', f(left))], progress}} artwork={art(`pool/books/${id}.jpg`)} style={square} onNavigate={none}/>
        : <ResumeCard model={{title: f(name), caption: [fact('by', f(by), 'identity')], progress}} location={fact('chapter', f(chapter))} remaining={fact('left', f(left))} artwork={art(`pool/books/${id}.jpg`)} onNavigate={none}/>}/>
    <Shelf heading={f('recentlyAdded')} subtitle={f('books8')} onSeeAll={none} platform={shelf} items={added} getKey={item => item[0]}
      renderItem={([id, name, by]) => <SquareCard variant="audiobook" model={{title: f(name), caption: [fact('by', f(by), 'identity')]}} artwork={art(`pool/books/${id}.jpg`)} style={square} onNavigate={none}/>}/>
    <Shelf heading={f('authors')} onSeeAll={none} platform={shelf} items={authors} getKey={item => item[0]}
      renderItem={([initials, name, books]) => <CircleCard variant="person" initials={f(initials)} covers={[]} model={{title: f(name), caption: [fact('books', f(books))]}} style={square} onNavigate={none}/>}/>
  </BrowseFrame></Page>;
}

// ---- The reader: the kit's ReaderChrome under its frame name ----
/** A stand-in for the reading engine's page (its own type, set by the book's display settings): two columns from 1024, one below. */
function ReaderPage() {
  return <article className={page.page}>
    <p className={page.chapter}><small>{f('chapterWord')}</small><span>{f('chapterNumeral')}</span></p>
    <p>{f('prose1')}</p><p className={page.indent}>{f('prose2')}</p>
  </article>;
}
function Reader({panel}: {panel?: 'contents' | 'display'}) {
  const phone = usePhone(), anchor = useRef<HTMLSpanElement>(null), [tab, setTab] = useState('highlights'), [anchored, setAnchored] = useState(false);
  // The display popover is anchored to its tool; it opens once the anchor is on the page.
  useEffect(() => setAnchored(true), []);
  const actions = ([['contents', 'contents', 'i-list'], ['search', 'searchBook', 'i-search'], ['bookmark', 'bookmark', 'i-bookmark'], ['display', 'display', 'i-size'], ['full', 'fullScreen', 'i-fs'], ['more', 'more', 'i-more']] as const)
    .filter(([id]) => !phone || id !== 'full').map(([id, label, icon]) => ({id, label: f(label), icon, expanded: panel === id, separatorBefore: id === 'full', onPress: none}));
  const highlights = <ReaderPanel label={f('contents')} tabs={[{id: 'contents', label: f('contents')}, {id: 'bookmarks', label: f('bookmarks'), count: '3'}, {id: 'highlights', label: f('highlights'), count: '3'}]} selected={tab} onSelect={setTab} onClose={none}
    footer={<><small>{f('synced')}</small><Button variant="text" size="small" onClick={none}>{f('exportHighlights')}</Button></>}>
    <ReaderAnnotations entries={[{id: 'h1', quote: f('highlight1'), note: f('note1'), chapter: f('chapter1'), page: f('page1'), date: f('oct18'), colour: 'yellow'}, {id: 'h2', quote: f('highlight2'), chapter: f('chapter1'), page: f('page22'), date: f('oct18'), colour: 'blue'},
      {id: 'h3', quote: f('highlight3'), note: f('note3'), chapter: f('chapter3'), page: f('page39'), date: f('todayDate'), colour: 'yellow'}] as never}/>
  </ReaderPanel>;
  const choice = (value: string, label: FixtureName) => ({value, label: f(label)});
  const controls = {themes: {label: f('page'), value: 'dark', onChange: none, options: [{...choice('dark', 'themeDark'), tone: 'dark' as const}, {...choice('black', 'themeBlack'), tone: 'black' as const}, {...choice('sepia', 'themeSepia'), tone: 'sepia' as const}, {...choice('light', 'themeLight'), tone: 'light' as const}]},
    fonts: {label: f('font'), value: 'literata', onChange: none, options: [{...choice('publisher', 'publisherFont'), face: 'publisher' as const, compactLabel: f('publisherFont'), description: f('fromTheBook')}, {...choice('literata', 'literata'), face: 'literata' as const, compactLabel: f('literata')}, {...choice('manrope', 'manrope'), face: 'manrope' as const, compactLabel: f('manrope')}]},
    size: {label: f('size'), sliderLabel: f('textSize'), smallerLabel: f('smaller'), largerLabel: f('larger'), value: 5, min: 1, max: 7, step: 1, valueText: '5', onChange: none},
    rows: [{id: 'spacing', label: f('lineSpacing'), value: 'normal', onChange: none, options: [choice('tight', 'tight'), choice('normal', 'normal'), choice('loose', 'loose')]},
      {id: 'margins', label: f('margins'), value: 'normal', onChange: none, options: [choice('narrow', 'narrow'), choice('normal', 'normal'), choice('wide', 'wide')]},
      {id: 'align', label: f('alignment'), value: 'justified', onChange: none, options: [choice('left', 'left'), choice('justified', 'justified')]}],
    copy: {sample: f('sample'), sizeSample: f('sample'), sessionNote: f('sessionNote'), defaultsNote: f('defaultsNote'), openSettings: f('openSettings')}, onOpenSettings: none};
  const Display = phone ? ReaderDisplaySheet : ReaderDisplayPopover;
  return <div style={{position: 'relative', height: '100%'}}>
    <span ref={anchor} style={{position: 'absolute', top: 16, right: 140, width: 40, height: 40}}/>
    <ReaderFrame title={f('gatsby')} subtitle={f('fitzgerald')} copy={{back: f('readerBack'), previous: f('previousPage'), next: f('nextPage'), progress: f('position'), showControls: f('showControls')}}
      chapter={f('chapter3')} pages={f('pages3940')} remainingInSection={f('pagesLeft')} percent={f('percent22')} remainingInBook={f('leftInBook')} progression={.22}
      chapters={[{id: 'c1', start: 0, end: .09}, {id: 'c2', start: .09, end: .17}, {id: 'c3', start: .17, end: .3, current: true}, {id: 'c4', start: .3, end: .4}, {id: 'c5', start: .4, end: .5}, {id: 'c6', start: .5, end: .62}, {id: 'c7', start: .62, end: .78}, {id: 'c8', start: .78, end: .88}, {id: 'c9', start: .88, end: 1}]}
      direction="ltr" tapTurnEnabled pageTurnsEnabled showPageArrows={!phone} presentation={phone ? 'phone' : 'desktop'} holdOpen onBack={none} onTurn={none} actions={actions}
      panel={panel === 'contents' ? highlights : undefined} panelReflows={panel === 'contents' && !phone}>
      <ReaderPage/>
    </ReaderFrame>
    {panel === 'display' && anchored && <Display label={f('display')} open onOpenChange={none} anchorRef={anchor} {...controls}/>}
  </div>;
}

const section = (id: string, label: string, render: ComponentType, height = 900): GalleryCase => ({id, labelKey: `next.platform.frame.${label}` as MessageId, width: 1440, height, render});
export default defineGalleryFamily({id: 'frames-newframes', labelKey: 'next.platform.frame.family', cases: [
  section('search-empty', 'searchEmpty', SearchEmpty), section('search-results', 'searchResults', SearchResults), section('search-none', 'searchNone', SearchNone),
  section('live-guide', 'liveGuide', () => <LiveGuide/>, 1400), section('live-details', 'liveDetails', () => <LiveGuide details/>), section('live-list', 'liveList', LiveList),
  section('live-upcoming', 'liveUpcoming', LiveUpcoming, 1300), section('live-recorded', 'liveRecorded', LiveRecorded), section('live-rules', 'liveRules', LiveRules),
  section('saved', 'saved', Saved), section('downloads', 'downloads', Downloads),
  section('music', 'music', () => <Music/>, 1300), section('music-songs', 'musicSongs', () => <Music tab="songs"/>), section('audiobooks', 'audiobooks', Audiobooks, 1200),
  section('reader', 'reader', () => <Reader/>), section('reader-contents', 'readerContents', () => <Reader panel="contents"/>), section('reader-display', 'readerDisplay', () => <Reader panel="display"/>),
]});
