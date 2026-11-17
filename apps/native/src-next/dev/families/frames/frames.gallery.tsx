import React, {useState, type ReactNode} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {framesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures';
import {AmbientArt, type ArtworkSource} from '../../../kit/artwork/index';
import {PosterCard, SquareCard, WideCard} from '../../../kit/cards';
import {Button, Switch, TextField} from '../../../kit/controls';
import {ArtBox, AudioPlayerFrame, BrowseFrame, HomeHero, PageStateFrame, PlayerChromeFrame, Section, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, SignInFrame, StateRow, TitleFrame,
  type AudioControl, type AudioOption} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {HeroMetadata, Row, type LayoutItemContext, type LayoutSelection} from '../../../kit/layout';
import {SettingsRow} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import type {Gallery} from '../../gallery-registry';

/** The screen frames, each drawn whole from the app's own components with reference text and artwork. One section
 * per frame and state; README.md beside this file lists them and how to open each. Development builds only. */
type FixtureName = keyof typeof framesFixtureMessages extends `next.frames.fixture.${infer Name}` ? Name : never;
const f = (name: FixtureName) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
/** The reference artwork the capture run serves (the same files the mock-ups use). */
const asset = (path: string): ArtworkSource => ({uri: `http://100.120.50.24:8787/assets/${path}`, cacheKey: `frames-reference:${path}`});
const none = () => {};
const selection: LayoutSelection = {active: false, isSelected: () => false, onToggle: none, onEnter: none};
const card = ({onFocus, onBlur, focusId, testID}: LayoutItemContext) => ({onFocus, onBlur, focusId, testID});
const fact = (text: string) => text.split(' · ').map((part, index) => ({id: index === 0 ? 'year' : `fact-${index}`, text: part, role: index === 0 ? 'identity' as const : 'context' as const}));

const posters = ['spirited-away', 'inside-out', 'alien', 'blade-runner', 'dune', 'barbie', 'back-to-the-future', 'the-dark-knight', 'avatar', 'aliens'].map(id => ({id, title: id.split('-').map(word => word[0].toUpperCase() + word.slice(1)).join(' '), art: asset(`pool/movies/${id}.jpg`)}));
const wides = [{id: 'tlou', title: f('tlou'), art: asset('bd-tlou.jpg')}, {id: 'inside-out', title: f('insideOut'), art: asset('bd-insideout.jpg')}, {id: 'spirited', title: f('spirited'), art: asset('bd-spirited.jpg')}];

/** One whole window for a frame: frames keep their own safe areas and gutters, so the Kit page's gutter is undone. */
function Window({children}: {children: ReactNode}) {
  const theme = useTheme(), window = useWindowDimensions();
  return <View style={{height: window.height, marginHorizontal: -theme.gutter}}>{children}</View>;
}

function PosterRow({title}: {title: string}) {
  const theme = useTheme(), width = theme.spec.card.width.poster[theme.platform];
  return <Row title={title} items={posters} getKey={item => item.id} itemWidth={width} itemHeight={width * 1.5} selection={selection}
    renderItem={(item, context) => <PosterCard {...card(context)} title={item.title} artwork={item.art} width={width} onNavigate={none}/>}/>;
}
function WideRow() {
  const theme = useTheme(), width = theme.spec.card.width.wide[theme.platform];
  return <Row title={f('continueWatching')} items={wides} getKey={item => item.id} itemWidth={width} itemHeight={width * 9 / 16} selection={selection}
    renderItem={(item, context) => <WideCard {...card(context)} title={item.title} artwork={item.art} width={width} progress={.4} primaryAction="play" onPlay={none} onNavigate={none}/>}/>;
}

const heroes = [
  {id: 'spirited', title: f('spirited'), reason: f('spiritedReason'), overview: f('tlouSynopsis'), facts: fact(f('spiritedFacts')), art: asset('bd-spirited.jpg')},
  {id: 'tlou', title: f('tlou'), overview: f('tlouSynopsis'), facts: fact(f('tlouFacts')), art: asset('bd-tlou.jpg')},
];
function BrowseHome() {
  const [index, setIndex] = useState(0), art = (id: string) => heroes.find(entry => entry.id === id)?.art;
  return <Window><BrowseFrame testID="frames-browse-home" hero={<HomeHero candidates={heroes} activeIndex={index} onIndexChange={setIndex}
    renderArtwork={(entry, context) => <AmbientArt foreground={art(entry.id)} visible={context.visible} foregroundStyle={context.foregroundStyle} onReady={context.onReady} onError={context.onError}/>}
    renderActions={() => <><Button icon="i-play" onPress={none}>{f('play')}</Button><Button variant="glass" icon="i-plus" onPress={none}>{f('myList')}</Button></>}/>}>
    <WideRow/><PosterRow title={f('newOnServer')}/>
  </BrowseFrame></Window>;
}
const BrowseList = () => <Window><BrowseFrame testID="frames-browse-list" header={{title: f('movies'), back: {label: f('libraries'), onPress: none}, trailing: <Button variant="glass" size="small" icon="i-filter" onPress={none}>{f('filters')}</Button>}}>
  <PosterRow title={f('newOnServer')}/><PosterRow title={f('moreLikeThis')}/>
</BrowseFrame></Window>;
const BrowseLoading = () => <Window><BrowseFrame testID="frames-browse-loading" header={{title: f('movies')}} state={{kind: 'loading'}}/></Window>;

function Episodes() {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Section title={f('episodes')}><View style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}>
    {(['episode1', 'episode2', 'episode3'] as const).map(name => <View key={name} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      <ArtBox kind="still" icon="i-tv" source={name === 'episode3' ? undefined : asset('bd-tlou.jpg')} width={theme.isTV ? 384 : 136}/>
      <View style={{flex: 1}}><Text numberOfLines={2} style={[frame.type.rowLabel, {color: theme.colors.text}]}>{f(name)}</Text><Text style={[frame.type.rowHelp, {color: theme.colors.textTertiary}]}>{f(`${name}Meta`)}</Text></View>
    </View>)}
  </View></Section>;
}
function Title({variant}: {variant: 'full' | 'missing' | 'loading'}) {
  const [tab, setTab] = useState('episodes'), full = variant !== 'missing', {form} = useFrameMetrics();
  return <Window><TitleFrame testID={`frames-title-${variant}`} back={{label: f('back'), onPress: none}} topAction={{icon: 'i-cast', label: f('playOn'), onPress: none}}
    art={<AmbientArt treatment="title" foreground={full ? asset('bd-tlou.jpg') : undefined}/>}
    title={full ? f('tlou') : f('longTitle')} metadata={<HeroMetadata facts={fact(full ? f('tlouFacts') : f('longFacts'))} centered={form === 'phone'}/>}
    primary={{icon: 'i-play', label: f('playEpisode'), onPress: none}}
    actions={full ? [{icon: 'i-plus', label: f('myList'), onPress: none}, {icon: 'i-dl', label: f('download'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}] : []}
    synopsis={full ? f('tlouSynopsis') : undefined}
    tabs={{items: [{value: 'episodes', label: f('episodes')}, {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}], value: tab, onValueChange: setTab}}
    state={variant === 'loading' ? {kind: 'loading'} : undefined}>
    <Episodes/>
  </TitleFrame></Window>;
}

function PlaybackSections() {
  const [autoplay, setAutoplay] = useState(true);
  return <>
    <SectionCard label={f('nextEpisode')}>
      <SettingsRow first label={f('autoplay')} control={<Switch checked={autoplay} onCheckedChange={setAutoplay} accessibilityLabel={f('autoplay')}/>}/>
      <SettingsRow label={f('wait')} description={f('waitHelp')} value={f('waitValue')} onPress={none}/>
    </SectionCard>
    <SectionCard label={f('skipping')}>
      <SettingsRow first icon="i-skip" label={f('intros')} value={f('ask')} onPress={none}/>
      <SettingsRow icon="i-skip" label={f('credits')} value={f('ask')} onPress={none}/>
    </SectionCard>
  </>;
}
/** Rule 9 in one page: a section that failed, one that kept its last list and says it is old, one loading and one empty. */
function StateSections() {
  return <>
    <SectionCard label={f('sources')}><StateRow kind="error" icon="i-warn" title={f('sourcesFailed')} reason={f('sourcesReason')} action={{label: f('tryAgain'), onPress: none}}/></SectionCard>
    <SectionCard label={f('sources')} stale={{label: f('sourcesStale'), retryLabel: f('tryAgain'), onRetry: none}}>
      <SettingsRow first icon="i-live" label={f('source1')} description={f('source1Meta')} navigates onPress={none}/>
      <SettingsRow icon="i-live" label={f('source2')} description={f('source2Meta')} navigates onPress={none}/>
    </SectionCard>
    <SectionCard label={f('guide')}><StateRow kind="loading"/></SectionCard>
    <SectionCard label={f('recordings')}><StateRow kind="empty" icon="i-rec" title={f('noRecordings')} reason={f('noRecordingsReason')} action={{label: f('openGuide'), onPress: none}}/></SectionCard>
  </>;
}
function SettingsList() {
  const [page, setPage] = useState<string>(), {form} = useFrameMetrics(), wide = form !== 'phone';
  const list = <SectionCard label={f('account')}>
    {([['playback', 'i-play'], ['audioSubtitles', 'i-cc'], ['quality', 'i-quality'], ['music', 'i-music'], ['appearance', 'i-sliders'], ['liveTv', 'i-live']] as const).map(([name, icon], index) =>
      <SettingsRow key={name} first={index === 0} icon={icon} label={f(name)} navigates onPress={() => setPage(name)}/>)}
  </SectionCard>;
  return <Window><SettingsListFrame testID="frames-settings-list" open onOpenChange={none} bar={{title: f('settings'), action: {label: f('done'), onPress: none}}} list={list}
    page={page || wide ? <SettingsPageFrame title={f('playback')} back={{label: f('settings'), onPress: () => setPage(undefined)}} intro={f('playbackIntro')}><PlaybackSections/></SettingsPageFrame> : undefined}/></Window>;
}
const SettingsPage = () => <Window><SettingsPageFrame testID="frames-settings-page" title={f('liveTv')} back={{label: f('settings'), onPress: none}} intro={f('playbackIntro')}><StateSections/></SettingsPageFrame></Window>;

const Sheet = () => <Window><BrowseFrame header={{title: f('liveTv')}}><View/></BrowseFrame>
  <SheetFrame kind="bottom" testID="frames-sheet" open onOpenChange={none} title={f('addSource')} footer={<Button variant="glass" align="stretch" onPress={none}>{f('cancel')}</Button>}>
    <SettingsRow first icon="i-link" label={f('playlist')} description={f('playlistMeta')} navigates onPress={none}/>
    <SettingsRow icon="i-wifi" label={f('findTuners')} description={f('findTunersMeta')} navigates onPress={none}/>
  </SheetFrame></Window>;

function SignIn() {
  const [address, setAddress] = useState(f('addressValue'));
  return <Window><SignInFrame testID="frames-sign-in" back={{label: f('back'), onPress: none}} title={f('localServer')} lede={f('localLede')} error={f('signInError')}
    terms={<Button variant="link" size="small" onPress={none}>{f('terms')}</Button>}>
    <TextField label={f('address')} value={address} onChangeText={setAddress}/>
    <Button align="stretch" onPress={none}>{f('continue')}</Button>
  </SignInFrame></Window>;
}

/** A still scrubber for the sections: the players' own scrubber takes this slot in a screen. */
function Track({elapsed, remaining}: {elapsed: string; remaining: string}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View><View style={{height: 4, borderRadius: 2, backgroundColor: `${theme.colors.text}38`}}><View style={{width: '34%', height: 4, borderRadius: 2, backgroundColor: theme.colors.text}}/></View>
    <View style={{flexDirection: 'row', justifyContent: 'space-between', marginTop: 4}}><Text style={[frame.type.rowHelp, {color: theme.colors.textTertiary}]}>{elapsed}</Text><Text style={[frame.type.rowHelp, {color: theme.colors.textTertiary}]}>{remaining}</Text></View></View>;
}
function Player() {
  const theme = useTheme(), frame = useFrameMetrics();
  const round = (icon: 'i-rew' | 'i-pause' | 'i-ffw', label: string) => <Button key={icon} variant="icon" icon={icon} accessibilityLabel={label} onPress={none}/>;
  return <Window><PlayerChromeFrame testID="frames-player" chromeVisible picture={<AmbientArt foreground={asset('bd-tlou.jpg')}/>}
    top={<View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}><Button variant="icon" icon="i-left" accessibilityLabel={f('back')} onPress={none}/><Text numberOfLines={1} style={[frame.type.barTitle, {flex: 1, color: theme.colors.text}]}>{f('videoTitle')}</Text><Button variant="icon" icon="i-cast" accessibilityLabel={f('playOn')} onPress={none}/></View>}
    transport={<View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.lg}}>{round('i-rew', f('rewind'))}{round('i-pause', f('pause'))}{round('i-ffw', f('forward'))}</View>}
    bottom={<View style={{gap: theme.space.xs}}><Track elapsed={f('elapsed')} remaining={f('remaining')}/><View style={{flexDirection: 'row', gap: theme.space.xs}}>
      {(['subtitles', 'audio', 'qualityShort'] as const).map(name => <Button key={name} variant="glass" size="small" onPress={none}>{f(name)}</Button>)}</View></View>}
    prompts={<Button variant="glass" size="small" onPress={none}>{f('skipIntro')}</Button>}/></Window>;
}

function Audio({variant}: {variant: 'music' | 'missing' | 'book' | 'loading'}) {
  const theme = useTheme(), frame = useFrameMetrics();
  const control = (icon: AudioControl['icon'], name: FixtureName, disabled = false): AudioControl => ({icon, label: f(name), onPress: none, disabled});
  const option = (name: FixtureName, icon?: AudioOption['icon'], value?: FixtureName, disabled = false): AudioOption => ({label: f(name), icon, value: value && f(value), onPress: none, disabled});
  const book = variant === 'book', missing = variant === 'missing';
  const music = [control('i-shuffle', 'shuffle'), control('i-prev', 'previous'), control('i-pause', 'pause'), control('i-next', 'next', missing), control('i-repeat', 'repeat')] as const;
  const reading = [control('i-prev', 'previous'), control('i-rew', 'rewind'), control('i-pause', 'pause'), control('i-ffw', 'forward'), control('i-next', 'next')] as const;
  const queue = <View style={{gap: theme.space.xs}}><Text style={[frame.type.groupLabel, {color: theme.colors.textSecondary}]}>{f('upNext')}</Text>
    {(['queue1', 'queue2', 'queue3', 'queue4', 'queue5', 'queue6'] as const).map(name => <Text key={name} numberOfLines={1} style={[frame.type.rowLabel, {color: theme.colors.text}]}>{f(name)}</Text>)}</View>;
  return <Window><AudioPlayerFrame testID={`frames-audio-${variant}`} loading={variant === 'loading'}
    close={control('i-down', 'close')} more={control('i-more', 'more')} context={book ? f('playingFromListening') : missing ? f('playingFromField') : f('playingFrom')}
    artwork={missing ? undefined : asset(book ? 'pool/albums/rumours.jpg' : 'pool/albums/random-access-memories.jpg')} icon={book ? 'i-book' : 'i-music'}
    title={book ? f('hobbit') : missing ? f('longSong') : f('songTitle')} byline={book ? f('tolkien') : missing ? f('choir') : f('songArtist')} detail={book ? f('chapter') : undefined}
    scrubber={<Track elapsed={f('elapsed')} remaining={f('remaining')}/>} transport={book ? reading : music} nextLine={book ? f('timeLeft') : missing ? undefined : f('songNext')}
    options={book ? [option('speed', undefined, 'speedValue'), option('sleep', undefined, 'sleepValue'), option('chapters', undefined, 'chaptersValue'), option('thisPhone', 'i-cast')]
      : [option('lyrics', 'i-lyrics', undefined, missing), option('thisPhone', 'i-cast'), option('queue', 'i-queue')]}
    upNext={queue}/></Window>;
}

const State = () => <Window><PageStateFrame testID="frames-state" icon="i-srvoff" title={f('cantReach')} body={f('cantReachBody')}
  primaryAction={{id: 'retry', label: f('tryAgain'), onPress: none}} secondaryAction={{id: 'switch', label: f('switchServer'), onPress: none}}/></Window>;

/** The type roles, the four row kinds in one card, and the artwork boxes with and without a picture. */
function Elements() {
  const theme = useTheme(), frame = useFrameMetrics(), poster = theme.spec.card.width.poster[theme.platform], square = theme.spec.card.width.square[theme.platform];
  const roles = [['pageTitle', 'rolePageTitle', theme.colors.text], ['sectionHeading', 'roleSectionHeading', theme.colors.text], ['groupLabel', 'roleGroupLabel', theme.colors.textSecondary], ['barTitle', 'roleBarTitle', theme.colors.text],
    ['rowLabel', 'roleRowLabel', theme.colors.text], ['rowHelp', 'roleRowHelp', theme.colors.textTertiary], ['body', 'roleBody', theme.colors.textSecondary]] as const;
  const boxes = [['square', 'i-music', 'artMusic', square, undefined], ['square', 'i-book', 'artBook', square, undefined], ['poster', 'i-film', 'artPoster', poster, undefined], ['wide', 'i-tv', 'artWide', poster * 1.6, undefined],
    ['square', 'i-music', 'withArtwork', square, asset('pool/albums/thriller.jpg')], ['poster', 'i-film', 'withArtwork', poster, asset('pool/movies/dune.jpg')]] as const;
  return <View testID="frames-elements" style={{marginHorizontal: -theme.gutter, paddingVertical: frame.sectionGap, gap: frame.sectionGap}}>
    <View style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}>{roles.map(([role, name, color]) => <Text key={role} style={[frame.type[role], {color}]}>{f(name)}</Text>)}</View>
    <PlaybackSections/>
    <StateSections/>
    <Section title={f('albums')}><View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.sm, marginHorizontal: frame.gutter}}>
      {boxes.map(([kind, icon, name, width, source], index) => <View key={index} style={{width, gap: theme.space.xs}}><ArtBox kind={kind} icon={icon} width={width} source={source}/><Text numberOfLines={1} style={[theme.type.cardTitle, {color: theme.colors.text}]}>{f(name)}</Text></View>)}
    </View></Section>
    <Section title={f('newOnServer')}><View style={{flexDirection: 'row', gap: theme.space.sm, marginHorizontal: frame.gutter}}>
      <PosterCard title={f('spirited')} caption={fact('2001')} artwork={asset('pool/movies/spirited-away.jpg')} width={poster} onNavigate={none}/>
      <PosterCard title={f('longTitle')} caption={fact('1974')} width={poster} onNavigate={none}/>
      <SquareCard title={f('songTitle')} caption={fact(f('songArtist'))} width={square} onNavigate={none}/>
    </View></Section>
  </View>;
}

const whole = {screen: true} as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-backdrop', title: 'next.platform.frame.backdrop', render: () => <Window><View/></Window>, ...whole},
  {id: 'frames-browse-home', title: 'next.platform.frame.browseHome', render: BrowseHome, ...whole},
  {id: 'frames-browse-list', title: 'next.platform.frame.browseList', render: BrowseList, ...whole},
  {id: 'frames-browse-loading', title: 'next.platform.frame.browseLoading', render: BrowseLoading, ...whole},
  {id: 'frames-title', title: 'next.platform.frame.title', render: () => <Title variant="full"/>, ...whole},
  {id: 'frames-title-missing', title: 'next.platform.frame.titleMissing', render: () => <Title variant="missing"/>, ...whole},
  {id: 'frames-title-loading', title: 'next.platform.frame.titleLoading', render: () => <Title variant="loading"/>, ...whole},
  {id: 'frames-settings-list', title: 'next.platform.frame.settingsList', render: SettingsList, ...whole},
  {id: 'frames-settings-page', title: 'next.platform.frame.settingsPage', render: SettingsPage, ...whole},
  {id: 'frames-sheet', title: 'next.platform.frame.sheet', render: Sheet, ...whole},
  {id: 'frames-sign-in', title: 'next.platform.frame.signIn', render: SignIn, platforms: ['phone', 'tablet', 'web'], ...whole},
  {id: 'frames-player', title: 'next.platform.frame.player', render: Player, ...whole},
  {id: 'frames-audio', title: 'next.platform.frame.audio', render: () => <Audio variant="music"/>, ...whole},
  {id: 'frames-audio-missing', title: 'next.platform.frame.audioMissing', render: () => <Audio variant="missing"/>, ...whole},
  {id: 'frames-audio-book', title: 'next.platform.frame.audioBook', render: () => <Audio variant="book"/>, ...whole},
  {id: 'frames-audio-loading', title: 'next.platform.frame.audioLoading', render: () => <Audio variant="loading"/>, ...whole},
  {id: 'frames-state', title: 'next.platform.frame.state', render: State, ...whole},
  {id: 'frames-elements', title: 'next.platform.frame.elements', render: Elements},
];
export default galleries;
