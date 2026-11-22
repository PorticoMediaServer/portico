import React, {useMemo, useState} from 'react';
import {Text, View, type LayoutChangeEvent} from 'react-native';
import {defaultI18n, formatMessage} from '../../../../../../packages/i18n/src';
import {browseToolbarModel} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {libraryTabs} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import {newFramesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/newframes-fixtures';
import {Artwork, ChannelMark} from '../../../kit/artwork/index';
import {CircleCard, GenreTile, LiveCard, PosterCard, QuickTile, ResumeCard, SquareCard, WideCard} from '../../../kit/cards';
import {cardCaptionHeight} from '../../../kit/cards/caption-metrics';
import {Button, Chip, Segmented, Switch, Tabs, TextField} from '../../../kit/controls';
import {BrowseToolbar, LibraryMenu, LibrarySwitcher} from '../../../kit/browse';
import {SongRow} from '../../../kit/browse/Tracks';
import {BrowseFrame, LiveFrame, SearchFrame, Section, SectionCard, SplitFrame, type FramePane} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {AiringRow, ChannelRow, ConflictCard, GuideGrid, GuideGroups, GuidePicker, RecordingRow, RuleCard, type GuideChannel} from '../../../kit/live';
import {Row, type LayoutItemContext, type LayoutSelection} from '../../../kit/layout';
import {DownloadRow, StorageMeter} from '../../../kit/saved';
import {SearchHistory, TopResult} from '../../../kit/search';
import {OwnerTag, SettingsRow} from '../../../kit/shell';
import {FocusProvider} from '../../../kit/focus';
import {useTheme} from '../../../kit/theme';
import {NativeTVKeyboard} from '../../../kit/search/TVKeyboard';
import type {Gallery} from '../../gallery-registry';
import {capability, labels} from '../browse/browse.gallery';
import {Page, asset, libraries} from './frames.gallery';
import {frameSpec} from '@portico/design';
import {posters as libraryPosters, useGrid} from './library.gallery';

/** Round 8: the kinds of screen that had no frame (Search, Live TV, Saved, Downloads, music, audiobooks), each drawn
 * whole on its frame from the app's own parts with the mock-ups' words and the reference artwork. Every case adapts
 * to the window, so the same id is shot on a phone, a tablet both ways and a TV. The reader is the web's (FRAMES.md). */
type FixtureName = keyof typeof newFramesFixtureMessages extends `next.newframes.fixture.${infer Name}` ? Name : never;
const f = (name: FixtureName) => formatMessage(newFramesFixtureMessages[`next.newframes.fixture.${name}`]);
const none = () => {};
const selection: LayoutSelection = {active: false, isSelected: () => false, onToggle: none, onEnter: none};
const card = ({onFocus, onBlur, focusId, testID}: LayoutItemContext) => ({onFocus, onBlur, focusId, testID});
const fact = (id: string, text: string, role: 'identity' | 'context' | 'supplement' | 'status' = 'context') => ({id, text, role});
/** The reference pool keeps shows apart from films. */
const shows = new Set(['severance', 'the-last-of-us', 'silo', 'slow-horses']);
const poster = (id: string) => asset(`pool/${shows.has(id) ? 'tv' : 'movies'}/${id}.jpg`);
const album = (id: string) => asset(`pool/albums/${id}.jpg`);
const book = (id: string) => asset(`pool/books/${id}.jpg`);

// ---- Search ----
function useScale() { const theme = useTheme(), frame = useFrameMetrics(); return {theme, frame, tv: frame.form === 'tv', phone: frame.form === 'phone'}; }
function SearchField({value}: {value?: string}) {
  const {theme, tv} = useScale();
  const field = <TextField variant="search" leadingIcon="i-search" value={value ?? ''} placeholder={f('searchPlaceholder')} accessibilityLabel={f('search')} onChangeText={none}
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
/** phone-app 7: a touch tile fans two posters at its right; TV draws the backdrop. */
const fans = ['spirited-away', 'inside-out', 'interstellar', 'dune', 'blade-runner', 'alien', 'the-dark-knight', 'barbie'];
const fan = (index: number) => [poster(fans[index % fans.length]!), poster(fans[(index + 3) % fans.length]!)];
/** A grid's room: the window's estimate first, then what the row really gets (a tablet's page sits beside its rail). */
function useRoom() {
  const {frame} = useScale(), [room, setRoom] = useState(frame.width - frame.gutter - frame.rightGutter);
  return [room, (event: LayoutChangeEvent) => setRoom(event.nativeEvent.layout.width)] as const;
}
function GenreTiles({count = 6}: {count?: number}) {
  const {theme, frame, tv, phone} = useScale(), [room, measure] = useRoom(), columns = phone ? 2 : tv ? 3 : 4, gap = theme.space.sm;
  const width = (room - gap * (columns - 1)) / columns;
  return <View onLayout={measure} style={{flexDirection: 'row', flexWrap: 'wrap', gap, marginLeft: frame.gutter, marginRight: frame.rightGutter}}>
    {genres.slice(0, count).map(([name, titles, art], index) => <GenreTile key={name} title={f(name)} caption={[fact('count', f(titles), 'supplement')]} variant="video" width={width} height={phone ? 84 : width * .42}
      backdrop={asset(`${art}.jpg`)} covers={fan(index)} onNavigate={none}/>)}
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
  const {theme} = useScale(), width = theme.spec.card.width.wide[theme.column];
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
const groups = ([['groupAll', 14], ['groupFavorites', 3], ['groupNews', 2], ['groupSports', 2], ['groupMovies', 2], ['groupKids', 1], ['groupDocumentary', 1]] as const)
  .map(([name, count]) => ({id: name, label: f(name), count, icon: name === 'groupFavorites' ? 'i-star' as const : undefined}));
function Groups() { return <GuideGroups groups={groups} value="groupAll" onValueChange={none}/>; }
// On a phone the source picker is an outlined chip at the head of the groups, their height (phone-guide g1); a button elsewhere.
function Switcher() { const {phone} = useScale(); return phone ? <Chip variant="pill" trailingIcon="i-chdown" onPress={none}>{f('allChannels')}</Chip> : <Button variant="glass" size="small" trailingIcon="i-chdown" onPress={none}>{f('allChannels')}</Button>; }
function GuideTools({list = false}: {list?: boolean}) {
  const {phone} = useScale();
  // A phone's day and sort are the guide's quiet pickers, text on one line (phone-guide g1).
  if (phone) return <><GuidePicker icon="i-cal" label={f('todayShort')} value={f('guideDate')} onPress={none}/><View style={{flex: 1}}/><GuidePicker label={f('sortByNumber')} onPress={none}/></>;
  return <>
    <Button variant="glass" size="small" icon="i-cal" trailingIcon="i-chdown" onPress={none}>{f('today')}</Button>
    {!phone && !list && <><Button variant="glass" size="small" icon="i-left" onPress={none}>{f('back2h')}</Button><Button variant="glass" size="small" onPress={none}>{f('now')}</Button><Button variant="glass" size="small" trailingIcon="i-right" onPress={none}>{f('ahead2h')}</Button></>}
    <View style={{flex: 1}}/>
    <Button variant="text" size="small" trailingIcon="i-chdown" onPress={none}>{f('sortByNumber')}</Button>
  </>;
}
/** The title row's buttons: a phone's List | Guide and Recordings; a tablet's Grid | List; TV's Today, Now and Recordings. */
function LiveActions({list = false}: {list?: boolean}) {
  const {phone, tv, theme} = useScale();
  if (tv) return <><Button variant="glass" size="small" trailingIcon="i-chdown" onPress={none}>{f('todayShort')}</Button><Button variant="glass" size="small" onPress={none}>{f('now')}</Button><Button variant="glass" size="small" icon="i-rec" onPress={none}>{f('tabRecordings')}</Button></>;
  const view = <Segmented variant="glass" value={list ? 'list' : 'grid'} onValueChange={none} items={[{value: 'list', label: f('list'), icon: 'i-list'}, {value: 'grid', label: f(phone ? 'guide' : 'grid'), icon: 'i-grid'}]}/>;
  // A phone's Recordings is a bare glyph in the title row, as a bar's actions are.
  return phone ? <>{view}<Button variant="text" icon="i-rec-ring" iconColor={theme.colors.text} iconOnly accessibilityLabel={f('tabRecordings')} onPress={none}/></> : view;
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
      <View style={{flexDirection: tv ? 'column' : 'row', flexWrap: tv ? undefined : 'wrap', gap: theme.space.xs}}>
        <Button variant="glass" icon="i-rec" iconColor={theme.colors.record} align={tv ? 'stretch' : undefined} onPress={none}>{f('recording')}</Button>
        <Button variant="glass" icon="i-repeat" align={tv ? 'stretch' : undefined} onPress={none}>{f('recordSeries')}</Button>
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
/** The source's facts beside the title (desktop-live guide): its name in the text colour, then its count and reach in the
 * secondary grey, at a row's help size. */
function LiveFacts() {
  const {theme} = useScale(), [source, ...rest] = f('liveFacts').split(' · ');
  return <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}><Text style={{color: theme.colors.text, fontFamily: theme.type.listLabel.fontFamily}}>{source}</Text>{rest.map(part => `   ${part}`).join('')}</Text>;
}
function LiveGuide({details = false}: {details?: boolean}) {
  const {theme} = useScale(), web = theme.platform === 'web';
  return <Page destination={{kind: 'live'}}><FocusProvider><LiveFrame testID={details ? 'frames-live-details' : 'frames-live-guide'} title={f('liveTv')} switcher={<Switcher/>}
    facts={<LiveFacts/>} actions={<LiveActions/>} tabs={<LiveTabs value="guide"/>} chips={<Groups/>}
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
/** desktop-live live-recorded: each show a live card with its channel, its corner badge or unwatched count, and its line. */
const recordedShows = [[channels.ad, 'pClockmakers', 'recordings6', undefined, 2], [channels.pk, 'pStoryTime', 'recordings3', undefined, 2], [channels.n24, 'pWorldTonight', 'yesterday430', undefined, undefined],
  [channels.lm, 'pQuietHarbor', 'quietHarborFacts', 'kept', undefined], [channels.ad, 'pWildPlains', 'recordingsThree', 'kept', undefined], [channels.ss, 'pCycling', 'today130', 'incomplete', undefined]] as const;
/** The line under a recorded show: its parts, the unwatched one in accent. */
const recordedLine = (line: string) => line.split(' · ').map((text, index) => fact(`line${index}`, text, / unwatched$/.test(text) ? 'status' : 'context'));
function LiveRecorded() {
  const {theme, phone} = useScale();
  return <Page destination={{kind: 'live'}}><FocusProvider><LiveFrame testID="frames-live-recorded" {...recordingsHeader('recorded', phone)} chips={<RecordingsChips value="recorded"/>}
    toolbar={<><Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f('recordedCounts')}</Text></>}><RecordedTiles/></LiveFrame></FocusProvider></Page>;
}
/** Measured inside the page (beside the rail), so the tiles fill the page's width, not the window's. */
function RecordedTiles() {
  const {theme, frame, phone} = useScale(), columns = phone ? 1 : frame.form === 'tv' ? 3 : 2, gap = theme.space.sm;
  const width = (frame.width - frame.gutter - frame.rightGutter - gap * (columns - 1)) / columns;
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', gap, marginLeft: frame.gutter, marginRight: frame.rightGutter}}>
    {recordedShows.map(([channel, title, line, badge, count]) => <LiveCard key={title} title={f(title)} channel={channel.name} mark={channel.mark} under={recordedLine(f(line))} badge={badge && f(badge)} count={count} width={width} onNavigate={none}/>)}
  </View>;
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
  const {theme, tv} = useScale(), [on, setOn] = useState(true);
  // TV, as its Settings: the switch is the whole row and a choice is a value row that opens its side sheet.
  return <>
    <SectionCard>{tv ? <SettingsRow first label={f('ruleOn')} description={f('ruleOnHelp')} toggle={{checked: on, visual: <Switch checked={on} focusable={false} accessibilityLabel={f('ruleOn')}/>}} onPress={() => setOn(!on)}/>
      : <SettingsRow first label={f('ruleOn')} description={f('ruleOnHelp')} control={<Switch checked={on} onCheckedChange={setOn} accessibilityLabel={f('ruleOn')}/>}/>}</SectionCard>
    <SectionCard>
      {tv ? <><SettingsRow first label={f('recordLabel')} value={f('allEpisodes')} onPress={none}/><SettingsRow label={f('channelLabel')} value={f('anyChannel')} onPress={none}/></> : <>
        <SettingsRow first label={f('recordLabel')} control={<Segmented value="all" onValueChange={none} items={[{value: 'new', label: f('newEpisodes')}, {value: 'all', label: f('allEpisodes')}]}/>}/>
        <SettingsRow label={f('channelLabel')} control={<Segmented value="any" onValueChange={none} items={[{value: 'this', label: f('thisChannel')}, {value: 'any', label: f('anyChannel')}]}/>}/></>}
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

// ---- Saved, Downloads, Music and Audiobooks: named uses of BrowseFrame (its header, tabs, toolbar, grid and rows regions) ----
const listed = [['dune-part-two', 'Dune: Part Two', '2024'], ['severance', 'Severance', 'Season 1'], ['the-last-of-us', 'The Last of Us', '1 h 4 min left'], ['spirited-away', 'Spirited Away', '2001'], ['interstellar', 'Interstellar', '2014'], ['silo', 'Silo', '1 season'], ['blade-runner', 'Blade Runner', '1982'], ['alien', 'Alien', '1979']] as const;
function PosterRow({title, subtitle}: {title: string; subtitle?: string}) {
  const {theme} = useScale(), width = theme.spec.card.width.poster[theme.column], caption = cardCaptionHeight(theme, 2);
  return <Row title={title} subtitle={subtitle} items={listed} getKey={item => item[0]} itemWidth={width} itemHeight={width * 1.5 + caption} captionHeight={caption} selection={selection} onSeeAll={none}
    renderItem={([id, name, line], context) => <PosterCard {...card(context)} title={name} artwork={poster(id)} progress={id === 'the-last-of-us' ? .18 : undefined} caption={[fact('caption', line, 'identity')]} width={width} onNavigate={none}/>}/>;
}
const savedTabs = (value: string) => ({value, onValueChange: none, items: (['myList', 'favorites', 'playlists', 'collections', 'savedViews', 'history', 'downloads'] as const).map(name => ({value: name, label: f(name)}))});
/** My List's Filters bar: the library's own toolbar (Filters, quick chips, the count, sort, grid or list) with Select, and
 * the chips tablet 11 draws for a list of what you saved: Unplayed, In progress, Played, Favorites (a My List chip would
 * filter the page to itself). */
function SavedToolbar() {
  const model = useMemo(() => {
    const library = browseToolbarModel(capability, 'movie', [], {field: 'added', direction: 'desc'}, {t: defaultI18n.t, owner: true}), offered = [...library.quick, ...library.filterQuick];
    return {...library, quick: (['unwatched', 'in-progress', 'watched', 'favorites'] as const).flatMap(id => offered.filter(quick => quick.id === id))};
  }, []);
  return <BrowseToolbar model={model} labels={labels} resultCount={f('titles18Count')} onFilters={none} onQuickChange={none} onSortChange={none} onSelect={none} view={{value: 'grid', onValueChange: none}}/>;
}
/** My List's own posters in the library frame's grid. A constant, so the grid keeps one collection. */
const myList = libraryPosters.slice(0, 18);
// The grid measures the page it is drawn in, so its hook runs inside the gallery `Page` (beside the rail), not around it.
const SavedList = () => <Page destination={{kind: 'saved'}}><SavedGrid/></Page>;
function SavedGrid() {
  const {grid} = useGrid(myList);
  return <BrowseFrame testID="frames-saved" header={{title: f('saved'), tabs: savedTabs('myList')}} toolbar={<SavedToolbar/>} grid={grid}/>;
}
/** A phone's Saved is a page of entries (phone-app 9): My List's row, the downloads on this phone, then the other lists. */
function Saved() {
  const {phone, theme} = useScale();
  if (!phone) return <SavedList/>;
  const width = theme.spec.card.width.wide[theme.column];
  const downloads = [['slow-horses', 'slowHorses', 'twoEpisodes14', undefined], ['dune-part-two', 'duneTwo', 'downloading64', .64]] as const;
  return <Page destination={{kind: 'saved'}}><BrowseFrame testID="frames-saved" header={{title: f('saved')}}>
    <PosterRow title={f('myList')} subtitle={f('titles12')}/>
    <Row title={f('downloads')} subtitle={f('onThisPhone')} items={downloads} getKey={item => item[0]} itemWidth={width} itemHeight={width * 9 / 16} selection={selection} onSeeAll={none}
      renderItem={([id, title, line, progress], context) => <WideCard {...card(context)} title={f(title)} artwork={asset(`pool/${id === 'slow-horses' ? 'tv' : 'movies'}/${id}--bd.jpg`)} progress={progress} caption={[fact('line', f(line), 'identity')]} width={width} onNavigate={none}/>}/>
    <SectionCard>
      <SettingsRow first icon="i-heart" label={f('favorites')} value={f('count24')} navigates onPress={none}/>
      <SettingsRow icon="i-queue" label={f('playlists')} value={f('count3')} navigates onPress={none}/>
      <SettingsRow icon="i-stack" label={f('collections')} value={f('count1')} navigates onPress={none}/>
      <SettingsRow icon="i-clock" label={f('history')} navigates onPress={none}/>
    </SectionCard>
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
  const page = <BrowseFrame testID="frames-downloads" header={phone ? {title: f('downloads'), back: {label: f('saved'), onPress: none}} : {title: f('saved'), tabs: savedTabs('downloads')}}>
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

/** A library's header (frameRegions.browse `header.*`): the switcher in the title (the centred name on a phone), ⋯, the tabs the
 * library's kind has (client-core `libraryTabs`), and Shuffle all for Music from tablet width. */
function useListenHeader(kind: 'music' | 'audiobook', tab: string) {
  const {phone} = useScale(), [library, setLibrary] = useState<string>(kind), [current, setCurrent] = useState(tab);
  return {title: libraries.find(entry => entry.id === library)?.name ?? f(kind === 'music' ? 'music' : 'audiobooks'), back: {label: f('libraries'), onPress: none},
    switcher: <LibrarySwitcher libraries={libraries} value={library} onValueChange={setLibrary} trigger={phone ? 'name' : 'chevron'}/>,
    menu: <LibraryMenu owner ownerTag={<OwnerTag compact/>} onSaveView={none} onMarkAll={none} onEditPins={none} onLibrarySettings={none} onScan={none}/>,
    trailing: kind === 'music' && !phone ? <Button variant="glass" size="small" icon="i-shuffle" onPress={none}>{f('shuffleAll')}</Button> : undefined,
    counts: f(kind === 'music' ? 'musicCounts' : 'bookCounts').split(' · ').map((text, index) => fact(`count${index}`, text)),
    tabs: {value: current, onValueChange: setCurrent, items: libraryTabs(kind).map(item => ({value: item.id, label: defaultI18n.t(item.label)}))}};
}
const albums = [['after-hours', 'aAfterHours', 'rWeeknd'], ['damn', 'aDamn', 'rKendrick'], ['rumours', 'aRumours', 'rFleetwood'], ['abbey-road-2019-mix', 'aAbbey', 'rBeatles'],
  ['hit-me-hard-and-soft', 'aHitMe', 'rBillie'], ['random-access-memories', 'aRam', 'rDaftPunk'], ['1989-taylor-s-version-deluxe', 'a1989', 'rTaylor'], ['thriller', 'aThriller', 'rJackson'], ['the-wall', 'aWall', 'rFloyd']] as const;
/** Recently played: quick tiles two lines deep, as many across as tiles of about their own width fit, two to four (final3-desktop-12, phone-app 15a). */
function RecentlyPlayed() {
  const {frame} = useScale(), gap = frameSpec.quickTile.gap[frame.form], [room, measure] = useRoom();
  const columns = Math.min(4, Math.max(2, Math.round((room + gap) / (frameSpec.quickTile.width[frame.form] + gap)))), width = (room - gap * (columns - 1)) / columns;
  return <Section title={f('recentlyPlayed')}><View onLayout={measure} style={{flexDirection: 'row', flexWrap: 'wrap', gap, marginLeft: frame.gutter, marginRight: frame.rightGutter}}>
    {albums.slice(0, columns * 2).map(([id, name, by], index) => index === 0
      ? <QuickTile key={id} focusId={`frames-music-tile-${id}`} title={f(name)} artwork={album(id)} caption={[fact('by', f(by), 'identity')]} width={width} playing onPause={none} onNavigate={none}/>
      : <QuickTile key={id} focusId={`frames-music-tile-${id}`} title={f(name)} artwork={album(id)} caption={[fact('by', f(by), 'identity')]} width={width} onNavigate={none}/>)}
  </View></Section>;
}
function AlbumRow() {
  const {theme} = useScale(), width = theme.spec.card.width.square[theme.column], caption = cardCaptionHeight(theme, 2);
  return <Row title={f('newAlbums')} subtitle={f('lastSevenDays')} items={albums} getKey={item => item[0]} itemWidth={width} itemHeight={width + caption} captionHeight={caption} selection={selection} onSeeAll={none}
    renderItem={([id, name, by], context) => <SquareCard {...card(context)} title={f(name)} artwork={album(id)} caption={[fact('by', f(by), 'identity')]} width={width} onNavigate={none}/>}/>;
}
function ArtistRow() {
  const {theme} = useScale(), width = theme.isTV ? theme.spec.card.width.artist.tv : 104, caption = cardCaptionHeight(theme, 2);
  return <Row title={f('artists')} items={albums.slice(0, 8)} getKey={item => item[2]} itemWidth={width} itemHeight={width + caption} captionHeight={caption} selection={selection} onSeeAll={none}
    renderItem={([id, , by], context) => <CircleCard {...card(context)} title={f(by)} art={{covers: [album(id)]}} caption={[fact('albums', f('oneAlbum'))]} width={width} onNavigate={none}/>}/>;
}
const songs = [['sHardest', 'd331', 'after-hours', 'rWeeknd'], ['sAlone', 'd410', 'after-hours', 'rWeeknd'], ['sTooLate', 'd359', 'after-hours', 'rWeeknd'], ['sDreams', 'd414', 'rumours', 'rFleetwood']] as const;
function Songs({count = 4}: {count?: number}) {
  const {frame} = useScale();
  return <View style={{marginHorizontal: frame.gutter}}>{songs.slice(0, count).map(([title, duration, art, by], index) => <SongRow key={title} focusId={`frames-music-${title}`} onPlay={none} onEnter={none}
    row={{id: title, playing: index === 0, artwork: <Artwork source={album(art)}/>, duration: fact('duration', f(duration)), text: {id: title, title: f(title), caption: [fact('artist', f(by))], detail: []}}}/>)}</View>;
}
function Music({tab = 'discover'}: {tab?: 'discover' | 'songs'}) {
  const {phone} = useScale(), header = useListenHeader('music', tab);
  return <Page destination={{kind: 'libraries'}}><BrowseFrame testID={tab === 'songs' ? 'frames-music-songs' : 'frames-music'} header={header}>
    {tab === 'songs' ? <Songs/> : <>
      <RecentlyPlayed/>
      {phone ? <><ArtistRow/><AlbumRow/></> : <><AlbumRow/><ArtistRow/><Section title={f('playMost')} subtitle={f('last30')}><Songs count={3}/></Section></>}
    </>}
  </BrowseFrame></Page>;
}
function Audiobooks() {
  const {theme, phone} = useScale(), header = useListenHeader('audiobook', 'discover');
  const width = theme.spec.card.width.square[theme.column], caption = cardCaptionHeight(theme, 2), person = theme.isTV ? theme.spec.card.width.artist.tv : 104;
  const going = [['the-martian-unabridged', 'bMartian', 'wWeir', 'chapter15', 'left434', .58], ['harry-potter-and-the-philosopher-s-stone', 'bPotter', 'wRowling', 'chapter6', 'left533', .32], ['the-hobbit', 'bHobbit', 'wTolkien', 'chapter6', 'left432', .62]] as const;
  const added = [['the-martian-unabridged', 'bMartian', 'wWeir'], ['the-hunger-games-special-edition', 'bHunger', 'wCollins'], ['the-hobbit', 'bHobbit', 'wTolkien'], ['project-hail-mary-unabridged', 'bHailMary', 'wWeir'],
    ['pride-and-prejudice', 'bPride', 'wAusten'], ['harry-potter-and-the-philosopher-s-stone', 'bPotter', 'wRowling'], ['dune', 'bDune', 'wHerbert']] as const;
  const authors = [['iWeir', 'wWeir', 'twoBooks'], ['iHerbert', 'wHerbert', 'oneBook'], ['iRowling', 'wRowling', 'oneBook'], ['iTolkien', 'wTolkien', 'oneBook']] as const;
  // Continue listening: the wide resume card from tablet width (desktop-listen books), square covers with time left on a phone (phone-app 15c).
  // desktop-listen books draws the resume card 456 wide; at the kit's narrower default its chapter line is cut.
  const resume = theme.isTV ? 560 : 456;
  return <Page destination={{kind: 'libraries'}}><BrowseFrame testID="frames-audiobooks" header={header}>
    {phone ? <Row title={f('continueListening')} items={going} getKey={item => item[0]} itemWidth={width} itemHeight={width + caption} captionHeight={caption} selection={selection}
      renderItem={([id, name, , , left, progress], context) => <SquareCard {...card(context)} title={f(name)} artwork={book(id)} progress={progress} caption={[fact('left', f(left))]} width={width} onNavigate={none}/>}/>
    : <Row title={f('continueListening')} items={going.slice(0, 2)} getKey={item => item[0]} itemWidth={resume} itemHeight={theme.isTV ? 216 : 150} selection={selection}
      renderItem={([id, name, by, chapter, left, progress], context) => <ResumeCard {...card(context)} title={f(name)} artwork={book(id)} progress={progress} caption={[fact('by', f(by), 'identity')]}
        detail={[fact('chapter', f(chapter)), fact('remaining', f(left))]} width={resume} onNavigate={none}/>}/>}
    <Row title={f('recentlyAdded')} subtitle={f('books8')} items={added} getKey={item => item[0]} itemWidth={width} itemHeight={width + caption} captionHeight={caption} selection={selection} onSeeAll={none}
      renderItem={([id, name, by], context) => <SquareCard {...card(context)} title={f(name)} artwork={book(id)} caption={[fact('by', f(by), 'identity')]} width={width} onNavigate={none}/>}/>
    <Row title={f('authors')} items={authors} getKey={item => item[0]} itemWidth={person} itemHeight={person + caption} captionHeight={caption} selection={selection} onSeeAll={none}
      renderItem={([initials, name, books], context) => <CircleCard {...card(context)} person title={f(name)} art={{initials: f(initials), covers: []}} caption={[fact('books', f(books))]} width={person} onNavigate={none}/>}/>
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
  {id: 'frames-music-songs', title: 'next.platform.frame.musicSongs', render: () => <Music tab="songs"/>, ...whole},
  {id: 'frames-audiobooks', title: 'next.platform.frame.audiobooks', render: Audiobooks, ...whole},
];
export default galleries;
