import React, {useState} from 'react';
import {Text, View} from 'react-native';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {newFramesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/newframes-fixtures';
import {Artwork, ChannelMark, type ArtworkSource} from '../../../kit/artwork/index';
import {CircleCard, GenreTile, LiveCard, PosterCard, QuickTile, SquareCard, WideCard} from '../../../kit/cards';
import {Button, Chip, Segmented, Switch, Tabs, TextField} from '../../../kit/controls';
import {SongRow} from '../../../kit/browse/Tracks';
import {BrowseFrame, LiveFrame, SearchFrame, Section, SectionCard, SplitFrame, type FramePane} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {AiringRow, ChannelRow, ConflictCard, GuideGrid, RecordingRow, RuleCard, type GuideChannel} from '../../../kit/live';
import {Row, type LayoutItemContext, type LayoutSelection} from '../../../kit/layout';
import {DownloadRow, StorageMeter} from '../../../kit/saved';
import {SearchHistory, TopResult} from '../../../kit/search';
import {SettingsRow} from '../../../kit/shell';
import {FocusProvider} from '../../../kit/focus';
import {useTheme} from '../../../kit/theme';
import {NativeTVKeyboard} from '../../../screens/search/TVKeyboard';
import type {Gallery} from '../../gallery-registry';
import {Page, asset} from './frames.gallery';

/** Round 8: the kinds of screen that had no frame (Search, Live TV, Saved, Downloads, music, audiobooks), each drawn
 * whole on its frame from the app's own parts with the mock-ups' words and the reference artwork. Every case adapts
 * to the window, so the same id is shot on a phone, a tablet both ways and a TV. The reader is the web's (FRAMES.md). */
type FixtureName = keyof typeof newFramesFixtureMessages extends `next.newframes.fixture.${infer Name}` ? Name : never;
const f = (name: FixtureName) => formatMessage(newFramesFixtureMessages[`next.newframes.fixture.${name}`]);
const none = () => {};
const selection: LayoutSelection = {active: false, isSelected: () => false, onToggle: none, onEnter: none};
const card = ({onFocus, onBlur, focusId, testID}: LayoutItemContext) => ({onFocus, onBlur, focusId, testID});
const fact = (id: string, text: string, role: 'identity' | 'context' | 'supplement' = 'context') => ({id, text, role});
/** The reference pool keeps shows apart from films. */
const shows = new Set(['severance', 'the-last-of-us', 'silo', 'slow-horses']);
const poster = (id: string) => asset(`pool/${shows.has(id) ? 'tv' : 'movies'}/${id}.jpg`);
const album = (id: string) => asset(`pool/albums/${id}.jpg`);
const book = (id: string) => asset(`pool/books/${id}.jpg`);

// ---- Search ----
function useScale() { const theme = useTheme(), frame = useFrameMetrics(); return {theme, frame, tv: frame.form === 'tv', phone: frame.form === 'phone'}; }
function SearchField({value}: {value?: string}) {
  const {theme, tv} = useScale();
  const field = <TextField variant="search" value={value ?? ''} placeholder={f('searchPlaceholder')} accessibilityLabel={f('search')} onChangeText={none}
    trailing={value ? <Button variant="icon" size="small" icon="i-x" iconOnly accessibilityLabel={f('clearText')} onPress={none}/> : undefined}/>;
  return tv ? <View style={{gap: theme.space.sm}}>{field}</View> : field;
}
function Keyboard({value}: {value: string}) {
  const {tv, theme} = useScale();
  if (!tv) return null;
  return <View style={{gap: theme.space.md}}>
    <NativeTVKeyboard id="frames-search-keyboard" value={value} onChangeText={none} activation={{visible: true, intent: 'enter', revision: 1}} leftRegionId="frames-search-rail"/>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f('voiceHint')}</Text>
  </View>;
}
const genres = [['genreAnimation', 'titles14', 'p-spirited'], ['genreSciFi', 'titles16', 'pool/movies/interstellar--bd'], ['genreDrama', 'titles31', 'p-shawshank'], ['genreCrime', 'titles9', 'p-godfather'],
  ['genreComedy', 'titles12', 'bd-rick'], ['genreAdventure', 'titles18', 'bd-lotr']] as const;
function GenreTiles({count = 6}: {count?: number}) {
  const {theme, frame, tv, phone} = useScale(), columns = phone ? 2 : tv ? 3 : 4, gap = theme.space.sm;
  const width = (frame.width - frame.gutter - frame.rightGutter - gap * (columns - 1)) / columns;
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, marginLeft: frame.gutter, marginRight: frame.rightGutter}}>
    {genres.slice(0, count).map(([name, titles, art]) => <GenreTile key={name} title={f(name)} caption={[fact('count', f(titles), 'supplement')]} variant="video" width={width} height={phone ? 84 : width * .42}
      backdrop={asset(`${art}.jpg`)} covers={[]} onNavigate={none}/>)}
  </View>;
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
function OnNowRow() {
  const {theme} = useScale(), width = theme.isTV ? 400 : theme.platform === 'phone' ? 260 : 300;
  const items = [[channels.nn, 'pWorldTonight', 'range330430', .58], [channels.ss, 'pCycling', 'range330630', .19], [channels.pk, 'pStoryTime', 'r400430', .17], [channels.ad, 'pBridges', 'range330430', .66]] as const;
  return <Row title={f('onNow')} subtitle={f('liveSource')} items={items} getKey={item => item[0].id} itemWidth={width} itemHeight={theme.isTV ? 225 : 140} selection={selection} onSeeAll={none}
    renderItem={([channel, title, range, progress], context) => <LiveCard {...card(context)} title={f(title)} channel={channel.name} number={channel.number} mark={channel.mark} range={f(range)} progress={progress} width={width} onNavigate={none}/>}/>;
}
function SearchEmpty() {
  const {phone, tv} = useScale();
  return <Page destination={{kind: 'search'}}><FocusProvider><SearchFrame testID="frames-search-empty" title={f('search')} field={<SearchField/>} keyboard={<Keyboard value=""/>}>
    <SearchHistory title={f('recent')} clear={{label: f('clear'), onPress: none}} items={(['recent1', 'recent2', 'recent3', 'recent4'] as const).slice(0, phone ? 3 : 4).map(name => ({query: f(name), onPress: none, remove: {label: f('remove'), onPress: none}}))}/>
    <Section title={f('browseByGenre')} subtitle={phone ? undefined : f('videoKinds')} trailing={!phone && <Button variant="text" size="small" trailingIcon="i-right" onPress={none}>{f('allGenres')}</Button>}><GenreTiles count={tv ? 3 : 6}/></Section>
    <OnNowRow/>
  </SearchFrame></FocusProvider></Page>;
}
function Scopes({empty = false}: {empty?: boolean}) {
  const scopes = [['scopeAll', undefined], ['scopeMovies', empty ? undefined : 7], ['scopeShows', 1], ['scopeEpisodes', 4], ['scopePeople', undefined], ['scopeArtists', undefined], ['scopeAlbums', undefined], ['scopeSongs', undefined], ['scopeAudiobooks', undefined], ['scopeLive', undefined]] as const;
  return <>{scopes.map(([name, count], index) => <Chip key={name} selected={index === 0} count={count} dim={index > 0 && count === undefined && name !== 'scopePeople' && name !== 'scopeLive'} retry={name === 'scopePeople' || name === 'scopeLive'} onPress={none}>{f(name)}</Chip>)}</>;
}
function EpisodesRow() {
  const {theme} = useScale(), width = theme.spec.card.width.wide[theme.platform];
  const items = [['episode1', 'episodeCode1', .2], ['episode2', 'episodeCode2', undefined], ['episode3', 'episodeCode3', undefined], ['episode4', 'episodeCode4', undefined]] as const;
  return <Row title={f('episodes')} subtitle="4" items={items} getKey={item => item[0]} itemWidth={width} itemHeight={width * 9 / 16} selection={selection}
    renderItem={([title, code, progress], context) => <WideCard {...card(context)} title={f(title)} artwork={asset('bd-tlou.jpg')} progress={progress} width={width} caption={[fact('show', f('tlou'), 'identity'), fact('code', f(code))]} onNavigate={none}/>}/>;
}
function SearchResults() {
  const {phone, theme} = useScale(), query = f('queryTlou');
  const facts = <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f('tlouFacts')}</Text>;
  return <Page destination={{kind: 'search'}}><FocusProvider><SearchFrame testID="frames-search-results" field={<SearchField value={query}/>} keyboard={<Keyboard value={query}/>}
    cancel={phone && <Button variant="text" size="small" onPress={none}>{f('cancel')}</Button>} scopes={<Scopes/>}
    notice={!theme.isTV && <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, padding: theme.space.sm, borderRadius: theme.radii.artwork, backgroundColor: theme.colors.raised}}>
      <Text style={[theme.type.rowLabel, {flex: 1, color: theme.colors.textSecondary}]}>{f('partial')}</Text><Button variant="glass" size="small" icon="i-retry" onPress={none}>{f('tryAgain')}</Button></View>}>
    <TopResult eyebrow={f('topResult')} title={f('tlou')} facts={facts} synopsis={f('tlouSynopsis')} artwork={asset('bd-tlou.jpg')} onPress={none} play={{label: f('play'), onPress: none}}
      actions={<><Button icon="i-play" size="small" onPress={none}>{f('resumeEpisode')}</Button><Button variant="glass" size="small" icon="i-plus" onPress={none}>{f('myList')}</Button><Button variant="glass" size="small" icon="i-more" iconOnly accessibilityLabel={f('more')} onPress={none}/></>}/>
    <EpisodesRow/>
  </SearchFrame></FocusProvider></Page>;
}
function SearchNone() {
  const query = f('queryNone');
  return <Page destination={{kind: 'search'}}><FocusProvider><SearchFrame testID="frames-search-none" field={<SearchField value={query}/>} keyboard={<Keyboard value={query}/>} none={{title: f('noMatches'), body: f('noMatchesBody')}}>
    <Section title={f('browseInstead')}><GenreTiles count={4}/></Section>
  </SearchFrame></FocusProvider></Page>;
}

// ---- Live TV ----
type Cell = GuideChannel['cells'][number];
const cell = (id: string, title: FixtureName, from: number, to: number, extra: Partial<Cell> = {}): Cell => ({id, title: f(title), from, to, past: to <= 35, progress: from <= 35 && to > 35 ? (35 - from) / (to - from) : undefined, ...extra});
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
function Grid() { return <GuideGrid channels={guide} span={210} now={35} ruler={ruler} nowLabel={f('nowTime')} channelLabel={f('channel')} onCell={none}/>; }
function Groups() {
  const groups = [['groupAll', 14], ['groupFavorites', 3], ['groupNews', 2], ['groupSports', 2], ['groupMovies', 2], ['groupKids', 1], ['groupDocumentary', 1]] as const;
  return <>{groups.map(([name, count], index) => <Chip key={name} selected={index === 0} count={count} icon={name === 'groupFavorites' ? 'i-star' : undefined} onPress={none}>{f(name)}</Chip>)}</>;
}
function Switcher() { return <Button variant="glass" size="small" trailingIcon="i-chdown" onPress={none}>{f('allChannels')}</Button>; }
function GuideTools({list = false}: {list?: boolean}) {
  const {phone} = useScale();
  return <>
    <Button variant="glass" size="small" icon="i-cal" trailingIcon="i-chdown" onPress={none}>{f(phone ? 'todayShort' : 'today')}</Button>
    {!phone && !list && <><Button variant="glass" size="small" icon="i-left" onPress={none}>{f('back2h')}</Button><Button variant="glass" size="small" onPress={none}>{f('now')}</Button><Button variant="glass" size="small" trailingIcon="i-right" onPress={none}>{f('ahead2h')}</Button></>}
    <View style={{flex: 1}}/>
    <Button variant="text" size="small" trailingIcon="i-chdown" onPress={none}>{f('sortByNumber')}</Button>
  </>;
}
/** The title row's buttons: a phone's List | Guide and Recordings; a tablet's Grid | List; TV's Today, Now and Recordings. */
function LiveActions({list = false}: {list?: boolean}) {
  const {phone, tv} = useScale();
  if (tv) return <><Button variant="glass" size="small" trailingIcon="i-chdown" onPress={none}>{f('todayShort')}</Button><Button variant="glass" size="small" onPress={none}>{f('now')}</Button><Button variant="glass" size="small" icon="i-rec" onPress={none}>{f('tabRecordings')}</Button></>;
  const view = <Segmented variant="glass" value={list ? 'list' : 'grid'} onValueChange={none} items={[{value: 'list', label: f('list')}, {value: 'grid', label: f(phone ? 'guide' : 'grid')}]}/>;
  return phone ? <>{view}<Button variant="icon" size="small" icon="i-rec" iconOnly accessibilityLabel={f('tabRecordings')} onPress={none}/></> : view;
}
function LiveTabs({value}: {value: 'guide' | 'recordings'}) {
  const {phone, tv} = useScale();
  if (phone || tv) return null;
  return <Tabs value={value} onValueChange={none} items={[{value: 'guide', label: f('tabGuide')}, {value: 'recordings', label: f('tabRecordings'), count: f('conflictTab')}]}/>;
}
function programmePane(): FramePane {
  return {title: f('pStoryTime'), subtitle: f('codeS3E1'), close: {label: f('close'), onPress: none}, eyebrow: <PaneChannel/>, children: <ProgrammeBody/>};
}
function PaneChannel() {
  const {theme} = useScale();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
    <ChannelMark mark={channels.pk.mark} size={theme.isTV ? 48 : 36}/>
    <View style={{flex: 1}}><Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{channels.pk.name} ★</Text><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{f('pebbleLine')}</Text></View>
    <Text style={[theme.type.rowHelp, {color: theme.colors.record}]}>● {f('onNowMark')}</Text>
  </View>;
}
function ProgrammeBody() {
  const {theme, tv} = useScale();
  return <>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{f('storyFacts')}</Text>
    <View style={{height: 3, borderRadius: 2, backgroundColor: theme.colors.raisedHigh}}><View style={{width: '17%', height: 3, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/></View>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{f('startedAgo')}</Text>
    <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{f('storySynopsis')}</Text>
    <View style={{gap: theme.space.xs}}>
      <Button icon="i-play" align="stretch" preferredFocus={tv} onPress={none}>{f('watchLive')}</Button>
      <View style={{flexDirection: tv ? 'column' : 'row', gap: theme.space.xs}}>
        <Button variant="glass" icon="i-rec" iconColor={theme.colors.record} align="stretch" style={{flex: tv ? undefined : 1}} onPress={none}>{f('recording')}</Button>
        <Button variant="glass" icon="i-repeat" align="stretch" style={{flex: tv ? undefined : 1}} onPress={none}>{f('recordSeries')}</Button>
      </View>
      {tv && <Button variant="glass" icon="i-heart" align="stretch" onPress={none}>{f('favourite')}</Button>}
    </View>
    <View>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text, marginBottom: theme.space.xs}]}>{f('moreAirings')}</Text>
      {([['t430', 'codeS5E2short', 'airFirstLight', 'r430500'], ['t530', 'codeS2E4', 'airS2E4', 'r530600'], ['t745', 'codeS5E7', 'airS5E7', 'r745815'], ['t1000', 'codeS6E5', 'airS6E5', 'r10001030']] as const).map(([time, code, title, range], index) =>
        <AiringRow key={time} time={f(time)} code={f(code)} title={f(title)} detail={f(range)} onPress={none}
          trailing={!tv && <Button variant={index === 2 ? 'glass' : 'icon'} size="small" icon="i-bell" iconOnly accessibilityLabel={f('remind')} onPress={none}/>}/>)}
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: theme.space.xs}]}>{f('reminderNote')}</Text>
    </View>
  </>;
}
function LiveGuide({details = false}: {details?: boolean}) {
  const {theme} = useScale(), web = theme.platform === 'web';
  return <Page destination={{kind: 'live'}}><FocusProvider><LiveFrame testID={details ? 'frames-live-details' : 'frames-live-guide'} title={f('liveTv')} switcher={<Switcher/>}
    facts={<Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f('liveFacts')}</Text>} actions={<LiveActions/>} tabs={<LiveTabs value="guide"/>} chips={<Groups/>}
    lead={web && <OnNowRow/>} toolbar={<GuideTools/>} details={details ? programmePane() : undefined} hints={web && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{f('keyboardHint')}</Text>}>
    <Grid/>
  </LiveFrame></FocusProvider></Page>;
}
function LiveList() {
  const rows = [[channels.nn, 'pWorldTonight', 'left30', .5, 'nextBriefing', false], [channels.n24, 'pMorningBriefing', 'left30', .5, 'nextWorld', false], [channels.ss, 'pCycling', 'left150', .17, 'nextLocker', true],
    [channels.lm, 'pGlassSky', 'left11', .88, 'nextHarbor', false]] as const;
  const {theme} = useScale();
  return <Page destination={{kind: 'live'}}><FocusProvider><LiveFrame testID="frames-live-list" title={f('liveTv')} switcher={<Switcher/>} actions={<LiveActions list/>} tabs={<LiveTabs value="guide"/>} chips={<Groups/>}
    toolbar={<><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f('onNowAt')}</Text><View style={{flex: 1}}/><Button variant="text" size="small" trailingIcon="i-chdown" onPress={none}>{f('sortByNumber')}</Button></>}>
    <View>{rows.map(([channel, title, left, progress, next, isLive]) => <ChannelRow key={channel.id} channel={channel} title={f(title)} left={f(left)} progress={progress} next={f(next)} live={isLive ? f('flagLive') : undefined} onPress={none}/>)}</View>
  </LiveFrame></FocusProvider></Page>;
}
function RecordingsChips({value}: {value: 'upcoming' | 'recorded' | 'rules'}) {
  const {phone} = useScale(), chips = [['upcoming', 'upcoming', 8], ['recorded', 'recorded', 20], ['rules', 'seriesRules', 4], ['history', 'history', undefined], ['storage', 'storage', undefined]] as const;
  if (phone) return null;
  return <>{chips.map(([id, name, count]) => <Chip key={id} selected={id === value} count={count} onPress={none}>{f(name)}</Chip>)}</>;
}
/** A phone's Recordings is its own page: Back to Live TV, the title in the bar, and the views as tabs (phone-app 12). */
function recordingsHeader(value: 'upcoming' | 'recorded' | 'rules', phone: boolean) {
  return phone ? {title: f('recordings'), back: {label: f('liveTv'), onPress: none},
    tabs: <Tabs value={value} onValueChange={none} items={[{value: 'upcoming', label: f('upcoming')}, {value: 'recorded', label: f('recorded')}, {value: 'rules', label: f('seriesRules')}, {value: 'history', label: f('history')}]}/>}
    : {title: f('liveTv'), switcher: <Switcher/>, tabs: <LiveTabs value="recordings"/>};
}
function Storage() { return <StorageMeter used={f('storageUsed')} free={f('storageFree')} value={.14} note={f('storageNote')} onPress={none}/>; }
function LiveUpcoming() {
  const {phone, theme} = useScale(), more = {label: f('more'), onPress: none};
  return <Page destination={{kind: 'live'}}><FocusProvider><LiveFrame testID="frames-live-upcoming" {...recordingsHeader('upcoming', phone)} chips={<RecordingsChips value="upcoming"/>}>
    {!phone && <Storage/>}
    <Section title={f('conflicts')}><ConflictCard title={f('conflictTitle')} body={f('conflictBody')} action={{label: f('resolve'), onPress: none}}>
      <View style={{marginHorizontal: -theme.gutter}}><RecordingRow time={f('t800')} range={f('r800900')} title={f('football')} flags={live} channel={channels.sse} source={f('seriesRule')} artwork={asset('bd-solo.jpg')} status={{text: f('tunerConflict'), tone: 'record'}}/></View>
    </ConflictCard></Section>
    <Section title={f('recordingNow')}><RecordingRow time={f('tNow')} range={f('r400430')} title={f('pStoryTime')} flags={fresh} detail={f('codeS3E1')} channel={channels.pk} source={f('oneTime')} artwork={asset('bd-toystory.jpg')} status={{text: f('recording'), tone: 'record'}} more={more} onPress={none}/></Section>
    <Section title={f('laterToday')}>
      <RecordingRow time={f('t415')} range={f('r415500')} title={f('pClockmakers')} detail={f('codeS5E9')} channel={channels.ad} source={f('seriesRule')} artwork={asset('bd-matrix.jpg')} status={phone ? {text: f('series'), tone: 'accent'} : undefined} more={more} onPress={none}/>
      <RecordingRow time={f('t430')} range={f('range430530')} title={f('pWorldTonight')} flags={live} channel={channels.n24} source={f('seriesRule')} artwork={asset('bd-sev.jpg')} status={phone ? {text: f('series'), tone: 'accent'} : undefined} more={more} onPress={none}/>
      <RecordingRow time={f('t553')} range={f('r553820')} title={f('pLongRoad')} detail={f('year1997')} channel={channels.lm} source={f('oneTime')} artwork={asset('bd-lotr.jpg')} more={more} onPress={none}/>
    </Section>
    {phone && <Storage/>}
  </LiveFrame></FocusProvider></Page>;
}
function LiveRecorded() {
  const {theme, frame, phone} = useScale(), columns = phone ? 1 : frame.form === 'tv' ? 3 : 2, gap = theme.space.sm;
  const width = (frame.width - frame.gutter - frame.rightGutter - gap * (columns - 1)) / columns;
  const items = [['pClockmakers', 'recordings6', 'bd-matrix'], ['pStoryTime', 'recordings3', 'bd-toystory'], ['pWorldTonight', 'yesterday430', 'bd-sev'], ['pQuietHarbor', 'quietHarborFacts', 'bd-spirited']] as const;
  return <Page destination={{kind: 'live'}}><FocusProvider><LiveFrame testID="frames-live-recorded" {...recordingsHeader('recorded', phone)} chips={<RecordingsChips value="recorded"/>}
    toolbar={<><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f('recordedCounts')}</Text></>}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, marginLeft: frame.gutter, marginRight: frame.rightGutter}}>
      {items.map(([title, caption, art]) => <WideCard key={title} title={f(title)} artwork={asset(`${art}.jpg`)} width={width} caption={[fact('caption', f(caption))]} onNavigate={none}/>)}
    </View>
  </LiveFrame></FocusProvider></Page>;
}
function rulePane(): FramePane {
  return {title: f('pClockmakers'), subtitle: f('ruleCounts'), close: {label: f('close'), onPress: none}, children: <RuleBody/>,
    footer: <><Button size="small" onPress={none}>{f('save')}</Button><Button variant="glass" size="small" onPress={none}>{f('cancel')}</Button><View style={{flex: 1}}/><DeleteRule/></>};
}
function DeleteRule() {
  const {theme} = useScale();
  return <Button variant="text" size="small" textStyle={{color: theme.colors.record}} onPress={none}>{f('deleteRule')}</Button>;
}
function RuleBody() {
  const {theme} = useScale(), [on, setOn] = useState(true);
  return <>
    <SectionCard><SettingsRow first label={f('ruleOn')} description={f('ruleOnHelp')} control={<Switch checked={on} onCheckedChange={setOn} accessibilityLabel={f('ruleOn')}/>}/></SectionCard>
    <SectionCard>
      <SettingsRow first label={f('recordLabel')} control={<Segmented value="all" onValueChange={none} items={[{value: 'new', label: f('newEpisodes')}, {value: 'all', label: f('allEpisodes')}]}/>}/>
      <SettingsRow label={f('channelLabel')} control={<Segmented value="any" onValueChange={none} items={[{value: 'this', label: f('thisChannel')}, {value: 'any', label: f('anyChannel')}]}/>}/>
      <SettingsRow label={f('keep')} value={f('keepEvery')} onPress={none}/>
      <SettingsRow label={f('startEarly')} value={f('twoMinutes')} onPress={none}/>
      <SettingsRow label={f('endLate')} value={f('none')} onPress={none}/>
    </SectionCard>
    <View>
      <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text, marginBottom: theme.space.xs}]}>{f('comingUp')}</Text>
      {([['t415', 'codeS5E9'], ['t645', 'codeS4E12'], ['t800', 'codeS3E1']] as const).map(([time, code]) => <AiringRow key={time} time={f(time)} title={f(code)} detail={`${f('airingsToday')} · ${channels.ad.name}`}
        trailing={<View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.record}}/>}/>)}
    </View>
  </>;
}
function LiveRules() {
  const {phone, theme} = useScale(), more = {label: f('more'), onPress: none};
  return <Page destination={{kind: 'live'}}><FocusProvider><LiveFrame testID="frames-live-rules" {...recordingsHeader('rules', phone)} chips={<RecordingsChips value="rules"/>} details={rulePane()}>
    <View style={{gap: theme.space.xs}}>
      <RuleCard selected title={f('pClockmakers')} summary={f('ruleAll')} next={f('ruleNextClock')} state={{label: f('on'), on: true}} upcoming={f('upcoming3')} artwork={asset('bd-matrix.jpg')} more={more} onPress={none}/>
      <RuleCard title={f('pWorldTonight')} summary={f('ruleWorld')} next={f('ruleNextWorld')} state={{label: f('on'), on: true}} upcoming={f('upcoming2')} artwork={asset('bd-sev.jpg')} more={more} onPress={none}/>
      <RuleCard title={f('summitFootball')} summary={f('ruleFootball')} next={f('ruleNextFootball')} warning state={{label: f('on'), on: true}} upcoming={f('upcoming1')} artwork={asset('bd-solo.jpg')} more={more} onPress={none}/>
      <RuleCard title={f('pWildPlains')} summary={f('rulePlains')} next={f('ruleNextPlains')} state={{label: f('paused'), on: false}} artwork={asset('bd-lotr.jpg')} more={more} onPress={none}/>
    </View>
  </LiveFrame></FocusProvider></Page>;
}

// ---- Saved and Downloads ----
const listed = [['dune-part-two', 'Dune: Part Two', '2024'], ['severance', 'Severance', '1 season'], ['the-last-of-us', 'The Last of Us', '1 h 4 min left'], ['spirited-away', 'Spirited Away', '2001'], ['interstellar', 'Interstellar', '2014'], ['silo', 'Silo', '1 season'], ['blade-runner', 'Blade Runner', '1982'], ['alien', 'Alien', '1979']] as const;
function PosterRow({title, subtitle}: {title: string; subtitle?: string}) {
  const {theme} = useScale(), width = theme.spec.card.width.poster[theme.platform];
  return <Row title={title} subtitle={subtitle} items={listed} getKey={item => item[0]} itemWidth={width} itemHeight={width * 1.5} selection={selection} onSeeAll={none}
    renderItem={([id, name, caption], context) => <PosterCard {...card(context)} title={name} artwork={poster(id)} progress={id === 'the-last-of-us' ? .18 : undefined} caption={[fact('caption', caption, 'identity')]} width={width} onNavigate={none}/>}/>;
}
const listening = [['book', 'project-hail-mary-unabridged', 'bHailMary', 'wWeir'], ['book', 'the-hobbit', 'bHobbit', 'wTolkien'], ['album', 'rumours', 'aRumours', 'rFleetwood'], ['album', 'random-access-memories', 'aRam', 'rDaftPunk']] as const;
function SquareRow({title}: {title: string}) {
  const {theme} = useScale(), width = theme.spec.card.width.square[theme.platform];
  return <Row title={title} subtitle="4" items={listening} getKey={item => item[1]} itemWidth={width} itemHeight={width} selection={selection}
    renderItem={([kind, id, name, by], context) => <SquareCard {...card(context)} title={f(name)} artwork={kind === 'book' ? book(id) : album(id)} caption={[fact('by', f(by), 'identity')]} width={width} onNavigate={none}/>}/>;
}
function SavedTabs({value}: {value: string}) {
  // The library frame's tabs region (stage1/r8-library) takes these; until it lands they lead the page's rows.
  return <Tabs value={value} onValueChange={none} items={(['myList', 'favorites', 'playlists', 'collections', 'savedViews', 'history', 'downloads'] as const).map(name => ({value: name, label: f(name)}))}/>;
}
function Saved() {
  const {phone} = useScale();
  return <Page destination={{kind: 'saved'}}><BrowseFrame testID="frames-saved" header={{title: f('saved')}}>
    {!phone && <SavedTabs value="myList"/>}
    <PosterRow title={f('myList')} subtitle={f('titles18Count')}/>
    {phone ? <SectionCard>
      <SettingsRow first icon="i-heart" label={f('favorites')} value={f('count24')} navigates onPress={none}/>
      <SettingsRow icon="i-queue" label={f('playlists')} value={f('count3')} navigates onPress={none}/>
      <SettingsRow icon="i-stack" label={f('collections')} value={f('count1')} navigates onPress={none}/>
      <SettingsRow icon="i-download" label={f('downloads')} value={f('onThisPhone')} navigates onPress={none}/>
    </SectionCard> : <SquareRow title={f('musicAndBooks')}/>}
  </BrowseFrame></Page>;
}
function Rules() {
  const [wifi, setWifi] = useState(true), [remove, setRemove] = useState(true), [next, setNext] = useState(true);
  return <>
    <SectionCard>
      <SettingsRow first label={f('wifiOnly')} description={f('wifiOnlyHelp')} control={<Switch checked={wifi} onCheckedChange={setWifi} accessibilityLabel={f('wifiOnly')}/>}/>
      <SettingsRow label={f('removeWatched')} description={f('removeWatchedHelp')} control={<Switch checked={remove} onCheckedChange={setRemove} accessibilityLabel={f('removeWatched')}/>}/>
      <SettingsRow label={f('nextEpisode')} description={f('nextEpisodeHelp')} control={<Switch checked={next} onCheckedChange={setNext} accessibilityLabel={f('nextEpisode')}/>}/>
      <SettingsRow label={f('storageLimit')} description={f('storageLimitHelp')} value={f('limit25')} navigates onPress={none}/>
    </SectionCard>
    <SectionCard><SettingsRow first icon="i-info" label={f('otherProfiles')} control={<Button variant="glass" size="small" onPress={none}>{f('remove')}</Button>}/></SectionCard>
  </>;
}
function Downloads() {
  const {phone, theme} = useScale(), more = {label: f('more'), onPress: none};
  const group = (children: React.ReactNode) => <SectionCard>{children}</SectionCard>;
  const page = <BrowseFrame testID="frames-downloads" header={{title: f(phone ? 'downloads' : 'saved'), back: phone ? {label: f('saved'), onPress: none} : undefined}}>
    {!phone && <SavedTabs value="downloads"/>}
    <StorageMeter used={f('storageDevice')} free={f('storageDeviceFree')} value={.08} other={.03}/>
    <Section title={`${f('slowHorses')}  ·  ${f('twoEpisodes')}`}>{group(<>
      <DownloadRow title={f('failuresContagious')} detail={f('failuresFacts')} artwork={poster('slow-horses')} action={<Button size="small" icon="i-play" iconOnly accessibilityLabel={f('play')} onPress={none}/>} onPress={none}/>
      <DownloadRow separator title={f('workDrinks')} detail={f('workDrinksFacts')} artwork={poster('slow-horses')} status={{text: f('downloading')}} progress={.64} more={more} onPress={none}/>
    </>)}</Section>
    <Section title={f('movies')}>{group(<>
      <DownloadRow title={f('spirited')} artwork={poster('spirited-away')} status={{text: f('waitingWifi'), tone: 'warning'}} more={more} onPress={none}/>
      <DownloadRow separator title={f('insideOut')} artwork={poster('inside-out')} status={{text: f('stopped'), tone: 'error'}} retry={{label: f('tryAgain'), onPress: none}} more={more} onPress={none}/>
    </>)}</Section>
    {phone && <Section title={f('rules')}><View style={{gap: theme.space.sm}}><Rules/></View></Section>}
  </BrowseFrame>;
  // tablet 11b: Rules is a pane beside the list from tablet width.
  return <Page destination={{kind: 'saved'}}><SplitFrame pane={phone ? undefined : {title: f('rules'), close: {label: f('close'), onPress: none}, children: <Rules/>}}>{page}</SplitFrame></Page>;
}

// ---- Music and audiobooks (named uses of BrowseFrame; the switcher, tabs and toolbar are the library frame's) ----
const albums = [['after-hours', 'aAfterHours', 'rWeeknd'], ['abbey-road-2019-mix', 'aAbbey', 'rBeatles'], ['1989-taylor-s-version-deluxe', 'a1989', 'rTaylor'], ['the-wall', 'aWall', 'rFloyd'], ['thriller', 'aThriller', 'rJackson'], ['damn', 'aDamn', 'rKendrick'], ['rumours', 'aRumours', 'rFleetwood']] as const;
function MusicHeaderActions() {
  return <><Button variant="glass" size="small" icon="i-shuffle" onPress={none}>{f('shuffleAll')}</Button><Button variant="glass" size="small" icon="i-more" iconOnly accessibilityLabel={f('more')} onPress={none}/></>;
}
function MusicTabs({value}: {value: string}) {
  return <Tabs value={value} onValueChange={none} items={[{value: 'discover', label: f('discover')}, {value: 'artists', label: f('artists')}, {value: 'albums', label: f('scopeAlbums')}, {value: 'songs', label: f('songs')}]}/>;
}
function RecentlyPlayed() {
  const {theme, phone} = useScale(), width = phone ? 172 : theme.isTV ? 420 : 300;
  return <Row title={f('recentlyPlayed')} items={albums.slice(0, 6)} getKey={item => item[0]} itemWidth={width} itemHeight={phone ? 52 : 76} selection={selection}
    renderItem={([id, name, by], context) => id === 'after-hours'
      ? <QuickTile {...card(context)} title={f(name)} artwork={album(id)} caption={[fact('by', f(by), 'identity')]} width={width} height={phone ? 52 : undefined} playing onPause={none} onNavigate={none}/>
      : <QuickTile {...card(context)} title={f(name)} artwork={album(id)} caption={[fact('by', f(by), 'identity')]} width={width} height={phone ? 52 : undefined} onNavigate={none}/>}/>;
}
function AlbumRow() {
  const {theme} = useScale(), width = theme.spec.card.width.square[theme.platform];
  return <Row title={f('newAlbums')} subtitle={f('lastSevenDays')} items={albums} getKey={item => item[0]} itemWidth={width} itemHeight={width} selection={selection} onSeeAll={none}
    renderItem={([id, name, by], context) => <SquareCard {...card(context)} title={f(name)} artwork={album(id)} caption={[fact('by', f(by), 'identity')]} width={width} onNavigate={none}/>}/>;
}
function ArtistRow() {
  const {theme} = useScale(), width = theme.isTV ? theme.spec.card.width.artist.tv : 104;
  const artists = [['21', 'rAdele'], ['abbey-road-2019-mix', 'rBeatles'], ['hit-me-hard-and-soft', 'rBillie'], ['random-access-memories', 'rDaftPunk'], ['rumours', 'rFleetwood'], ['damn', 'rKendrick']] as const;
  return <Row title={f('artists')} items={artists} getKey={item => item[1]} itemWidth={width} itemHeight={width} selection={selection} onSeeAll={none}
    renderItem={([id, name], context) => <CircleCard {...card(context)} title={f(name)} art={{covers: [album(id)]}} caption={[fact('albums', f('oneAlbum'))]} width={width} onNavigate={none}/>}/>;
}
function Songs({count = 4}: {count?: number}) {
  const songs = [['sHardest', 'd331', 'after-hours', 'rWeeknd'], ['sAlone', 'd410', 'after-hours', 'rWeeknd'], ['sTooLate', 'd359', 'after-hours', 'rWeeknd'], ['sDreams', 'd414', 'rumours', 'rFleetwood']] as const;
  return <View>{songs.slice(0, count).map(([title, duration, art, by], index) => <SongRow key={title} focusId={`frames-music-${title}`} onPlay={none} onEnter={none}
    row={{id: title, playing: index === 0, artwork: <Artwork source={album(art)}/>, duration: fact('duration', f(duration)), text: {id: title, title: f(title), caption: [fact('artist', f(by))], detail: []}}}/>)}</View>;
}
function Music({songs = false}: {songs?: boolean}) {
  const {frame} = useScale();
  return <Page destination={{kind: 'libraries'}}><BrowseFrame testID={songs ? 'frames-music-songs' : 'frames-music'} header={{title: f('music'), back: {label: f('scopeAll'), onPress: none}, trailing: frame.form !== 'phone' && <MusicHeaderActions/>}}>
    <MusicTabs value={songs ? 'songs' : 'discover'}/>
    {songs ? <View style={{marginHorizontal: frame.gutter}}><Songs/></View> : <>
      <RecentlyPlayed/><AlbumRow/><ArtistRow/>
      <Section title={f('playMost')} trailing={undefined}><View style={{marginHorizontal: frame.gutter}}><Songs count={3}/></View></Section>
    </>}
  </BrowseFrame></Page>;
}
function Audiobooks() {
  const {theme} = useScale(), width = theme.spec.card.width.square[theme.platform], person = theme.isTV ? theme.spec.card.width.artist.tv : 104;
  const going = [['the-hobbit', 'bHobbit', 'left432', .62], ['the-martian-unabridged', 'bMartian', 'left434', .58], ['project-hail-mary-unabridged', 'bHailMary', 'left142', .12]] as const;
  const added = [['the-hunger-games-special-edition', 'bHunger', 'wCollins'], ['pride-and-prejudice', 'bPride', 'wAusten'], ['dune', 'bDune', 'wHerbert'], ['the-hobbit', 'bHobbit', 'wTolkien']] as const;
  const authors = [['iWeir', 'wWeir'], ['iHerbert', 'wHerbert'], ['iTolkien', 'wTolkien']] as const;
  return <Page destination={{kind: 'libraries'}}><BrowseFrame testID="frames-audiobooks" header={{title: f('audiobooks'), back: {label: f('scopeAll'), onPress: none}}}>
    <Tabs value="discover" onValueChange={none} items={[{value: 'discover', label: f('discover')}, {value: 'books', label: f('books')}, {value: 'authors', label: f('authors')}]}/>
    <Row title={f('continueListening')} items={going} getKey={item => item[0]} itemWidth={width} itemHeight={width} selection={selection}
      renderItem={([id, name, left, progress], context) => <SquareCard {...card(context)} title={f(name)} artwork={book(id)} progress={progress} caption={[fact('left', f(left))]} width={width} onNavigate={none}/>}/>
    <Row title={f('recentlyAdded')} subtitle={f('books8')} items={added} getKey={item => item[0]} itemWidth={width} itemHeight={width} selection={selection} onSeeAll={none}
      renderItem={([id, name, by], context) => <SquareCard {...card(context)} title={f(name)} artwork={book(id)} caption={[fact('by', f(by), 'identity')]} width={width} onNavigate={none}/>}/>
    <Row title={f('authors')} items={authors} getKey={item => item[0]} itemWidth={person} itemHeight={person} selection={selection} onSeeAll={none}
      renderItem={([initials, name], context) => <CircleCard {...card(context)} person title={f(name)} art={{initials: f(initials), covers: [book('the-martian-unabridged')]}} width={person} onNavigate={none}/>}/>
  </BrowseFrame></Page>;
}

const whole = {screen: true} as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-search-empty', title: 'next.platform.frame.searchEmpty', render: SearchEmpty, ...whole},
  {id: 'frames-search-results', title: 'next.platform.frame.searchResults', render: SearchResults, ...whole},
  {id: 'frames-search-none', title: 'next.platform.frame.searchNone', render: SearchNone, ...whole},
  {id: 'frames-live-guide', title: 'next.platform.frame.liveGuide', render: () => <LiveGuide/>, ...whole},
  {id: 'frames-live-details', title: 'next.platform.frame.liveDetails', render: () => <LiveGuide details/>, ...whole},
  {id: 'frames-live-list', title: 'next.platform.frame.liveList', render: LiveList, ...whole},
  {id: 'frames-live-upcoming', title: 'next.platform.frame.liveUpcoming', render: LiveUpcoming, ...whole},
  {id: 'frames-live-recorded', title: 'next.platform.frame.liveRecorded', render: LiveRecorded, ...whole},
  {id: 'frames-live-rules', title: 'next.platform.frame.liveRules', render: LiveRules, ...whole},
  {id: 'frames-saved', title: 'next.platform.frame.saved', render: Saved, ...whole},
  {id: 'frames-downloads', title: 'next.platform.frame.downloads', render: Downloads, platforms: ['phone', 'tablet'], ...whole},
  {id: 'frames-music', title: 'next.platform.frame.music', render: () => <Music/>, ...whole},
  {id: 'frames-music-songs', title: 'next.platform.frame.musicSongs', render: () => <Music songs/>, ...whole},
  {id: 'frames-audiobooks', title: 'next.platform.frame.audiobooks', render: Audiobooks, ...whole},
];
export default galleries;
