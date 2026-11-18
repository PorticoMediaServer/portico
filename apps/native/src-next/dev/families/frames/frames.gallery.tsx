import React, {useRef, useState, type ReactNode} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {framesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures';
import {AmbientArt, Artwork, type ArtworkSource} from '../../../kit/artwork/index';
import {PosterCard, SquareCard, WideCard} from '../../../kit/cards';
import {Button, Switch, Tabs, TextField} from '../../../kit/controls';
import {ArtBox, AudioPlayerFrame, BrowseFrame, HomeHero, PageStateFrame, PlayerChromeFrame, Section, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, SignInFrame, StateRow, TitleFrame,
  type AudioControl, type AudioOption} from '../../../kit/frames';
import {frameSpec} from '@portico/design';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {PlayerChannelStep, PlayerControl, PlayerEpisodeRow, PlayerLiveActions, PlayerPanel, PlayerPanelGroup, PlayerPanelRow, PlayerPill, PlayerPrompt, PlayerScrubBar, PlayerScrubPreview, PlayerTimes, PlayerTitle, PlayerUpNext} from '../../../kit/player';
import {HeroMetadata, Row, type LayoutItemContext, type LayoutSelection} from '../../../kit/layout';
import {SettingsRow} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {MediumRail} from '../../../shell/MediumRail';
import {PhoneTabBar} from '../../../shell/PhoneTabBar';
import {phoneBarHeight, type ShellDestination} from '../../../shell/model';
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

/** A page as the app shows it: with the shell's own tab bar on a phone and its rail on a tablet, so the frame is judged
 * where it lives. The TV rail needs a signed-in session and is not drawn here. */
function Page({children, destination}: {children: ReactNode; destination: ShellDestination}) {
  const theme = useTheme(), window = useWindowDimensions(), insets = useSafeAreaInsets(), {form} = useFrameMetrics();
  const landscape = window.width > window.height, rail = form === 'medium' || form === 'expanded';
  const profileRef = useRef<View>(null);
  return <View style={{height: window.height, marginHorizontal: -theme.gutter, flexDirection: 'row'}}>
    {rail && <MediumRail libraries={[]} hasLiveTV destination={destination} profile={{id: 'gallery', name: f('profileInitial'), initial: f('profileInitial'), color: 'blue'}} unread={false} onNavigate={none} onProfile={none} profileRef={profileRef} short={window.height < theme.layout.breakpoint.phone}/>}
    <View style={{flex: 1, paddingBottom: form === 'phone' ? phoneBarHeight(insets.bottom, landscape) : 0}}>{children}</View>
    {form === 'phone' && <PhoneTabBar destination={destination} hasLiveTV onSelect={none} landscape={landscape}/>}
  </View>;
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
  return <Page destination={{kind: 'home'}}><BrowseFrame testID="frames-browse-home" hero={<HomeHero candidates={heroes} activeIndex={index} onIndexChange={setIndex}
    renderArtwork={(entry, context) => <AmbientArt foreground={art(entry.id)} visible={context.visible} foregroundStyle={context.foregroundStyle} onReady={context.onReady} onError={context.onError}/>}
    renderActions={() => <><Button icon="i-play" onPress={none}>{f('play')}</Button><Button variant="glass" icon="i-plus" onPress={none}>{f('myList')}</Button></>}/>}>
    <WideRow/><PosterRow title={f('newOnServer')}/>
  </BrowseFrame></Page>;
}
const BrowseList = () => <Page destination={{kind: 'libraries'}}><BrowseFrame testID="frames-browse-list" header={{title: f('movies'), back: {label: f('libraries'), onPress: none}, trailing: <Button variant="glass" size="small" icon="i-filter" onPress={none}>{f('filters')}</Button>}}>
  <PosterRow title={f('newOnServer')}/><PosterRow title={f('moreLikeThis')}/>
</BrowseFrame></Page>;
const BrowseLoading = () => <Window><BrowseFrame testID="frames-browse-loading" header={{title: f('movies')}} state={{kind: 'loading'}}/></Window>;

function Episodes() {
  const theme = useTheme(), frame = useFrameMetrics();
  return <Section title={f('episodes')}><View style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}>
    {(['episode1', 'episode2', 'episode3'] as const).map(name => <View key={name} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm}}>
      <ArtBox kind="still" icon="i-tv" source={name === 'episode3' ? undefined : asset('bd-tlou.jpg')} width={theme.isTV ? 384 : 136}/>
      <View style={{flex: 1}}><Text numberOfLines={2} style={[theme.type.rowLabel, {color: theme.colors.text}]}>{f(name)}</Text><Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{f(`${name}Meta`)}</Text></View>
    </View>)}
  </View></Section>;
}
function Title({variant}: {variant: 'full' | 'missing' | 'loading'}) {
  const [tab, setTab] = useState('more'), full = variant !== 'missing', {form} = useFrameMetrics();
  return <Page destination={{kind: 'libraries'}}><TitleFrame testID={`frames-title-${variant}`} back={{label: f('back'), onPress: none}} topAction={{icon: 'i-cast', label: f('playOn'), onPress: none}}
    art={<AmbientArt treatment="title" foreground={full ? asset('bd-insideout.jpg') : undefined}/>}
    title={full ? f('insideOut') : f('longTitle')} metadata={<HeroMetadata facts={fact(full ? `${f('insideOutFacts')} · ${f('insideOutRating')}` : f('longFacts'))} centered={form === 'phone'}/>}
    primary={full ? {icon: 'i-play', label: f('resume'), progress: .09, remaining: f('resumeLeft'), startOver: {label: f('startOver'), onPress: none}, onPress: none} : {icon: 'i-play', label: f('play'), onPress: none}}
    actions={full ? [{icon: 'i-plus', label: f('myList'), labelled: true, onPress: none}, {icon: 'i-check', label: f('watched'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}] : []}
    tagline={full ? f('insideOutTagline') : undefined} synopsis={full ? f('insideOutSynopsis') : undefined}
    credits={full ? [{label: f('directedBy'), names: f('insideOutDirector')}, {label: f('starring'), names: f('insideOutCast')}] : undefined}
    tabs={{items: [{value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}], value: tab, onValueChange: setTab}}
    state={variant === 'loading' ? {kind: 'loading'} : undefined}>
    <PosterRow title={f('moreLikeInsideOut')}/>
  </TitleFrame></Page>;
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

type PlayerState = 'playing' | 'paused' | 'buffering' | 'scrubbing' | 'live' | 'missing';
/** The watch page under an upright phone's picture (phone-player 4): the title page's own content, without its art. */
function WatchPage() {
  const theme = useTheme(), frame = useFrameMetrics(), [tab, setTab] = useState('episodes');
  return <ScrollView contentContainerStyle={{paddingVertical: theme.space.sm, gap: theme.space.sm}}>
    <View style={{marginHorizontal: frame.gutter, gap: theme.space.tight}}><Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text}]}>{f('tlou')}</Text>
      <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{f('videoLead')}  {f('videoSubtitle')}</Text><HeroMetadata facts={fact(f('tlouFacts'))}/></View>
    <View style={{marginHorizontal: frame.gutter, flexDirection: 'row', gap: theme.spec.hero.actions.gap[theme.platform]}}><Button icon="i-pause" align="stretch" style={{flex: 1}} onPress={none}>{f('pause')}</Button>
      {([['i-plus', 'myList'], ['i-dl', 'download'], ['i-more', 'more']] as const).map(([icon, name]) => <Button key={name} variant="glass" iconOnly icon={icon} accessibilityLabel={f(name)} onPress={none}/>)}</View>
    <Tabs items={[{value: 'episodes', label: f('episodes')}, {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]} value={tab} onValueChange={setTab}/>
    <Episodes/>
  </ScrollView>;
}
/** The video player filled with the kit's player parts, with what each mock-up puts in each place for its form
 * (tablet-play and tv-player; phone-player 2c on its side; phone-player 4 upright). Nothing moves between the states. */
function Player({state = 'playing', over, id = `frames-player-${state}`}: {state?: PlayerState; /** A panel or card of the player's own, drawn over it as the player screen does. */ over?: ReactNode; id?: string}) {
  const window = useWindowDimensions(), {form} = useFrameMetrics(), phone = form === 'phone', upright = phone && window.height > window.width, live = state === 'live', missing = state === 'missing';
  const still = <Artwork source={asset('bd-tlou.jpg')}/>;
  const tool = (icon: AudioControl['icon'], name: FixtureName, disabled = false) => <PlayerControl key={name} onPicture icon={icon} label={f(name)} disabled={disabled} onPress={none}/>;
  const quality = <PlayerPill icon="i-quality" qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none}/>;
  return <Window><PlayerChromeFrame testID={id} chromeVisible picture={still} close={tool('i-down', 'close')} title={<PlayerTitle title={f('tlou')} lead={f('videoLead')} subtitle={f('videoSubtitle')}/>}
    tools={<>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{upright && tool('i-cc', 'subtitles', missing)}{tool('i-settings', 'playerSettings')}</>}
    scrubber={<PlayerScrubBar value={live ? .96 : .2} buffered={[{start: .2, end: live ? 1 : .34}]} marks={missing || live ? [] : [{start: .02, end: .08}, {start: .95, end: 1}]}
      preview={state === 'scrubbing' ? <PlayerScrubPreview at={.6} time={f('previewTime')}>{still}</PlayerScrubPreview> : undefined}/>}
    times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration'), endsAt: live ? undefined : f('endsAt'), live: live ? f('live') : undefined}}
    transport={<><PlayerControl icon="i-prev" label={f('previous')} disabled={live || missing} onPress={none}/><PlayerControl icon="i-rew" label={f('rewind')} onPress={none}/>
      <PlayerControl primary icon={state === 'paused' ? 'i-play' : 'i-pause'} busy={state === 'buffering'} label={f(state === 'paused' ? 'play' : 'pause')} onPress={none}/>
      <PlayerControl icon="i-ffw" label={f('forward')} disabled={live} onPress={none}/><PlayerControl icon="i-next" label={f('next')} disabled={live || missing} onPress={none}/></>}
    leading={<Button variant="glass" size="small" icon="i-episodes" disabled={live} onPress={none}>{f('episodesButton')}</Button>}
    trailing={upright ? <>{quality}<PlayerControl icon="i-full" label={f('fullScreen')} onPress={none}/></> : <>
      <PlayerPill icon="i-cc" name={f('subtitles')} value={f('english')} label={f('subtitles')} disabled={missing} onPress={none}/>
      <PlayerPill icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} disabled={missing} onPress={none}/>{quality}</>}
    prompts={state === 'playing' && !over ? <PlayerPrompt label={f('skipIntro')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}}/> : undefined}
    body={<WatchPage/>}/>{over}</Window>;
}

/** The player's own panels (kit/player), each over the paused player: Playback options and the lists it opens
 * (tv-player 4, tablet-play 2b, phone-player 3), then Episodes (tablet-play 2a). */
type PanelName = 'options' | 'subtitles' | 'audio' | 'quality' | 'speed';
function PlayerPanelCase({panel}: {panel: PanelName}) {
  const close = {label: f('optDone'), onPress: none}, back = {label: f('optBack'), onPress: none};
  const row = (label: FixtureName, detail?: FixtureName, selected = false, unavailable = false) => <PlayerPanelRow key={label} label={f(label)} detail={detail && f(detail)} selected={selected} unavailable={unavailable ? f('optUnavailable') : undefined} onPress={none}/>;
  const entry = (icon: AudioControl['icon'], label: FixtureName, value: FixtureName) => <PlayerPanelRow key={label} icon={icon} label={f(label)} value={f(value)} onPress={none}/>;
  const body = {
    options: <PlayerPanelGroup>{entry('i-cc', 'subtitles', 'english')}{entry('i-audio', 'audio', 'english')}{entry('i-quality', 'qualityShort', 'qualityValue')}{entry('i-gauge', 'optSpeed', 'optNormal')}{entry('i-moon', 'optSleep', 'optOff')}</PlayerPanelGroup>,
    subtitles: <PlayerPanelGroup label={f('subtitles')} value={f('english')}>{row('optOff')}{row('english', 'optSrt', true)}{row('optEnglishSdh', 'optSrt')}{row('optSpanish', 'optSrt')}{row('optFrench', 'optSrtBeside')}</PlayerPanelGroup>,
    audio: <PlayerPanelGroup label={f('audio')} value={f('english')}>{row('english', 'optAac51', true)}{row('optSpanish', 'optAacStereo')}{row('optCommentary', 'optAacStereo', false, true)}</PlayerPanelGroup>,
    quality: <PlayerPanelGroup label={f('qualityShort')} value={f('qualityValue')}>{row('optOriginal', 'optOriginalDetail')}{row('optAutomatic', 'optAutomaticDetail', true)}{row('opt720', 'opt720Detail')}{row('opt480', 'opt480Detail')}</PlayerPanelGroup>,
    speed: <PlayerPanelGroup label={f('optSpeed')} value={f('optNormal')}>{row('optSpeed05')}{row('optSpeed075')}{row('optNormal', undefined, true)}{row('optSpeed125')}{row('optSpeed15')}{row('optSpeed2')}</PlayerPanelGroup>,
  }[panel];
  return <Player state="paused" id={`frames-player-${panel}`} over={<PlayerPanel title={f('optTitle')} note={panel === 'options' ? f('optNote') : undefined} back={panel === 'options' ? undefined : back} close={close}>{body}</PlayerPanel>}/>;
}
function PlayerEpisodesCase() {
  return <Player state="paused" id="frames-player-episodes" over={<PlayerPanel title={f('episodesTitle')} close={{label: f('optDone'), onPress: none}}>
    <PlayerEpisodeRow still={asset('bd-tlou.jpg')} title={f('episode1')} facts={f('episodeMeta1')} overview={f('tlouSynopsis')} onPress={none}/>
    <PlayerEpisodeRow playing still={asset('bd-tlou.jpg')} title={f('episode2')} facts={f('episodeMeta2')} overview={f('tlouSynopsis')} onPress={none}/>
    <PlayerEpisodeRow title={f('episode3')} facts={f('episodeMeta3')} overview={f('tlouSynopsis')} onPress={none}/>
  </PlayerPanel>}/>;
}
/** Up next over the credits (tv-player 5, phone-player 6): the kit card in the player's prompt corner. */
function PlayerUpNextCase() {
  const {form} = useFrameMetrics(), spec = frameSpec.player;
  return <Player id="frames-player-up-next" over={<View style={{position: 'absolute', right: spec.edge[form], bottom: spec.prompt.bottom[form]}}>
    <PlayerUpNext compact={form !== 'tv'} item={{entryId: 'next', title: f('episode3'), durationMs: 76 * 60_000, episode: {seasonNumber: 1, number: 3}} as never} artwork={asset('bd-tlou.jpg')}
      countdown={{seconds: 42, automatic: false}} countdownTotal={60} sceneAfter={false} onPlayNow={none} onWatchCredits={none} onSkipCredits={none}/></View>}/>;
}
/** The Live TV player's controls on the player frame (tv-player 11, phone-player 1): Record and More at the top,
 * Guide, channel down and up either side of the transport, and Go to live when behind. */
function PlayerLiveControlsCase() {
  const still = <Artwork source={asset('bd-tlou.jpg')}/>;
  return <Window><PlayerChromeFrame testID="frames-player-live-controls" chromeVisible picture={still} close={<PlayerControl onPicture icon="i-down" label={f('close')} onPress={none}/>}
    title={<PlayerTitle title={f('liveTitle')} lead={f('liveChannel')} subtitle={f('liveTimes')}/>}
    tools={<PlayerLiveActions enabled compact={false} record={{label: f('liveRecord'), onPress: none}} more={{onPress: none}}/>}
    scrubber={<PlayerScrubBar value={.68} buffered={[{start: .55, end: .7}]}/>}
    times={{position: f('liveClock'), remaining: f('liveEnds'), duration: f('liveEnds'), live: f('liveBehind')}}
    transport={<><PlayerChannelStep direction="down" number={f('liveDown')} label={f('liveChannelDown')} onPress={none}/><PlayerControl icon="i-rew" label={f('rewind')} onPress={none}/>
      <PlayerControl primary icon="i-pause" label={f('pause')} onPress={none}/><PlayerControl icon="i-ffw" label={f('forward')} onPress={none}/>
      <PlayerChannelStep direction="up" number={f('liveUp')} label={f('liveChannelUp')} onPress={none}/></>}
    leading={<Button variant="glass" size="small" icon="i-list" onPress={none}>{f('liveGuide')}</Button>}
    trailing={<><PlayerPill icon="i-rec" value={f('liveGoToLive')} label={f('liveGoToLive')} onPress={none}/><PlayerPill icon="i-quality" qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none}/></>}
    body={<WatchPage/>}/></Window>;
}

function Audio({variant}: {variant: 'music' | 'missing' | 'book' | 'loading'}) {
  const theme = useTheme(), frame = useFrameMetrics();
  const control = (icon: AudioControl['icon'], name: FixtureName, disabled = false): AudioControl => ({icon, label: f(name), onPress: none, disabled});
  const option = (name: FixtureName, icon?: AudioOption['icon'], value?: FixtureName, disabled = false): AudioOption => ({label: f(name), icon, value: value && f(value), onPress: none, disabled});
  const book = variant === 'book', missing = variant === 'missing';
  const music = [control('i-shuffle', 'shuffle'), control('i-prev', 'previous'), control('i-pause', 'pause'), control('i-next', 'next', missing), control('i-repeat', 'repeat')] as const;
  const reading = [control('i-prev', 'previous'), control('i-rew', 'rewind'), control('i-pause', 'pause'), control('i-ffw', 'forward'), control('i-next', 'next')] as const;
  const queue = <View style={{gap: theme.space.xs}}><Text style={[theme.type.groupLabel, {color: theme.colors.textSecondary}]}>{f('upNext')}</Text>
    {(['queue1', 'queue2', 'queue3', 'queue4', 'queue5', 'queue6'] as const).map(name => <Text key={name} numberOfLines={1} style={[theme.type.rowLabel, {color: theme.colors.text}]}>{f(name)}</Text>)}</View>;
  return <Window><AudioPlayerFrame testID={`frames-audio-${variant}`} loading={variant === 'loading'}
    close={control('i-down', 'close')} more={control('i-more', 'more')} context={book ? f('playingFromListening') : missing ? f('playingFromField') : f('playingFrom')}
    artwork={missing ? undefined : asset(book ? 'pool/albums/rumours.jpg' : 'pool/albums/random-access-memories.jpg')} icon={book ? 'i-book' : 'i-music'}
    title={book ? f('hobbit') : missing ? f('longSong') : f('songTitle')} byline={book ? f('tolkien') : missing ? f('choir') : f('songArtist')} detail={book ? f('chapter') : undefined}
    scrubber={<View style={{gap: frameSpec.player.timesGap[frame.form]}}><PlayerScrubBar value={.34}/><PlayerTimes position={f('elapsed')} remaining={f('remaining')}/></View>} transport={book ? reading : music} nextLine={book ? f('timeLeft') : missing ? undefined : f('songNext')}
    options={book ? [option('speed', undefined, 'speedValue'), option('sleep', undefined, 'sleepValue'), option('chapters', undefined, 'chaptersValue'), option('thisPhone', 'i-cast')]
      : [option('lyrics', 'i-lyrics', undefined, missing), option('thisPhone', 'i-cast'), option('queue', 'i-queue')]}
    upNext={queue}/></Window>;
}

const State = () => <Page destination={{kind: 'home'}}><PageStateFrame testID="frames-state" icon="i-srvoff" title={f('cantReach')} body={f('cantReachBody')} retry={{progress: .6, label: f('retryLabel'), detail: f('retryDetail')}}
  primaryAction={{id: 'retry', label: f('tryAgain'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'switch', label: f('switchServer'), icon: 'i-swap', onPress: none}}/></Page>;

/** The type roles, the four row kinds in one card, and the artwork boxes with and without a picture. */
function Elements() {
  const theme = useTheme(), frame = useFrameMetrics(), poster = theme.spec.card.width.poster[theme.platform], square = theme.spec.card.width.square[theme.platform];
  const roles = [['pageTitle', 'rolePageTitle', theme.colors.text], ['rowHeading', 'roleSectionHeading', theme.colors.text], ['groupLabel', 'roleGroupLabel', theme.colors.textSecondary], ['barTitle', 'roleBarTitle', theme.colors.text],
    ['rowLabel', 'roleRowLabel', theme.colors.text], ['rowHelp', 'roleRowHelp', theme.colors.textTertiary], ['intro', 'roleBody', theme.colors.textSecondary]] as const;
  const boxes = [['square', 'i-music', 'artMusic', square, undefined], ['square', 'i-book', 'artBook', square, undefined], ['poster', 'i-film', 'artPoster', poster, undefined], ['wide', 'i-tv', 'artWide', poster * 1.6, undefined],
    ['square', 'i-music', 'withArtwork', square, asset('pool/albums/thriller.jpg')], ['poster', 'i-film', 'withArtwork', poster, asset('pool/movies/dune.jpg')]] as const;
  return <View testID="frames-elements" style={{marginHorizontal: -theme.gutter, paddingVertical: frame.sectionGap, gap: frame.sectionGap}}>
    <View style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}>{roles.map(([role, name, color]) => <Text key={role} style={[theme.type[role], {color}]}>{f(name)}</Text>)}</View>
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

/** Decision 4: each frame empty, as the frames page draws it. What the frame owns is drawn by the frame, with stand-in
 * words; what the screen supplies is an outlined box that says so, and the one flexible region is marked. */
function Region({name, flexible = false, height}: {name: FixtureName; flexible?: boolean; height?: number}) {
  const theme = useTheme(), frame = useFrameMetrics(), color = flexible ? theme.colors.amber : theme.colors.accentStrong;
  return <View style={{flex: height === undefined ? 1 : undefined, height, minHeight: height === undefined ? frame.rowTwoLine : undefined, overflow: 'hidden', alignSelf: 'stretch', borderWidth: 2, borderStyle: 'dashed', borderColor: color, borderRadius: theme.baseRadii.control, padding: theme.space.xs, justifyContent: 'center'}}>
    <Text style={[theme.type.rowLabel, {color}]}>{f(name)}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f(flexible ? 'regionFlexible' : 'regionScreen')}</Text>
  </View>;
}
/** An outlined region the size of a row of cards, at the frame's gutter. */
function Block({name, flexible, rows = 3}: {name: FixtureName; flexible?: boolean; rows?: number}) {
  const frame = useFrameMetrics();
  return <View style={{marginHorizontal: frame.gutter}}><Region name={name} flexible={flexible} height={frame.rowTwoLine * rows}/></View>;
}
const own = {back: {label: f('ownBack'), onPress: none}, action: {label: f('ownAction'), onPress: none}};
const RegionsBrowse = () => {
  const frame = useFrameMetrics();
  return <Window><BrowseFrame hero={<View style={{height: frameSpec.hero.home.height[frame.form]}}><Region name="regionHero"/></View>}><Block name="regionRows" flexible/><Block name="regionRows" flexible/></BrowseFrame></Window>;
};
const RegionsTitle = () => <Window><TitleFrame back={own.back} topAction={{icon: 'i-cast', label: f('ownAction'), onPress: none}} art={<Region name="regionArt"/>} title={f('ownTitle')}
  metadata={<Text style={{color: 'transparent'}}>{f('ownMeta')}</Text>} primary={{icon: 'i-play', label: f('ownPrimary'), onPress: none}}
  actions={[{icon: 'i-plus', label: f('ownAction'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]} synopsis={f('ownSynopsis')}
  tabs={{items: [{value: 'one', label: f('ownTab1')}, {value: 'two', label: f('ownTab2')}], value: 'one', onValueChange: none}}><Block name="regionPanel" flexible/></TitleFrame></Window>;
function RegionsSettings() {
  const {form} = useFrameMetrics();
  const page = <SettingsPageFrame title={f('ownTitle')} back={own.back} action={own.action} intro={f('ownIntro')}><Block name="regionSections" flexible/><Block name="regionSections" flexible/></SettingsPageFrame>;
  return <Window><SettingsListFrame open onOpenChange={none} bar={{title: f('ownBarTitle'), action: own.action}} list={<Block name="regionList" flexible rows={5}/>} page={form === 'phone' ? undefined : page}/></Window>;
}
const RegionsSheet = () => <Window><SheetFrame kind="bottom" open onOpenChange={none} title={f('ownBarTitle')} footer={<Region name="regionFooter" height={frameSpec.sheet.footerClear}/>}><Block name="regionSheetRows" flexible/></SheetFrame></Window>;
const RegionsSignIn = () => <Window><SignInFrame back={own.back} title={f('ownTitle')} lede={f('ownLede')} terms={<Text style={{color: 'transparent'}}>{f('terms')}</Text>}><Region name="regionForm" flexible height={frameSpec.signIn.column.medium}/></SignInFrame></Window>;
const RegionsPlayer = () => <Window><PlayerChromeFrame chromeVisible picture={<Region name="regionPicture"/>} close={<Region name="regionClose" height={frameSpec.header.backRow}/>} title={<Region name="regionPlayerTitle" height={frameSpec.header.backRow}/>}
  tools={<Region name="regionTools" height={frameSpec.header.backRow}/>} scrubber={<Region name="regionScrubber" height={frameSpec.player.portrait.control}/>} times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration')}}
  transport={<View style={{width: frameSpec.audio.controlsWidth.expanded}}><Region name="regionTransport" height={frameSpec.header.backRow}/></View>} leading={<Region name="regionLeading" height={frameSpec.header.backRow}/>} trailing={<Region name="regionTrailing" height={frameSpec.header.backRow}/>}
  prompts={<View style={{width: frameSpec.audio.upNextWidth.expanded}}><Region name="regionPrompts" height={frameSpec.header.backRow}/></View>} body={<Region name="regionWatchPage" flexible/>}/></Window>;
function RegionsAudio() {
  const slot = (icon: AudioControl['icon']): AudioControl => ({icon, label: f('ownAction') + icon, onPress: none});
  return <Window><AudioPlayerFrame close={slot('i-down')} more={slot('i-more')} context={f('ownContext')} icon="i-music" title={f('ownTitle')} byline={f('ownByline')} scrubber={<Region name="regionScrubber" height={frameSpec.audio.scrubber.phone}/>}
    transport={[slot('i-shuffle'), slot('i-prev'), slot('i-pause'), slot('i-next'), slot('i-repeat')]} nextLine={f('ownNext')}
    options={[{label: f('ownOption'), icon: 'i-lyrics', onPress: none}, {label: f('ownOption') + ' ', icon: 'i-cast', onPress: none}, {label: f('ownOption') + '  ', icon: 'i-queue', onPress: none}]} upNext={<Region name="regionUpNext" flexible height={frameSpec.audio.artwork.phone}/>}/></Window>;
}
const RegionsState = () => <Window><PageStateFrame icon="i-info" title={f('ownStateTitle')} body={f('ownStateBody')} primaryAction={{id: 'primary', label: f('ownPrimary'), onPress: none}} secondaryAction={{id: 'secondary', label: f('ownSecondary'), onPress: none}}/></Window>;

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
  {id: 'frames-player', title: 'next.platform.frame.player', render: () => <Player/>, ...whole},
  ...(['paused', 'buffering', 'scrubbing', 'live', 'missing'] as const).map(state => ({id: `frames-player-${state}`, title: 'next.platform.frame.player' as const, render: () => <Player state={state}/>, ...whole})),
  ...(['options', 'subtitles', 'audio', 'quality', 'speed'] as const).map(panel => ({id: `frames-player-${panel}`, title: `next.platform.frame.player${panel[0]!.toUpperCase()}${panel.slice(1)}` as 'next.platform.frame.playerOptions', render: () => <PlayerPanelCase panel={panel}/>, ...whole})),
  {id: 'frames-player-episodes', title: 'next.platform.frame.playerEpisodes', render: PlayerEpisodesCase, ...whole},
  {id: 'frames-player-up-next', title: 'next.platform.frame.playerUpNext', render: PlayerUpNextCase, ...whole},
  {id: 'frames-player-live-controls', title: 'next.platform.frame.playerLiveControls', render: PlayerLiveControlsCase, ...whole},
  {id: 'frames-audio', title: 'next.platform.frame.audio', render: () => <Audio variant="music"/>, ...whole},
  {id: 'frames-audio-missing', title: 'next.platform.frame.audioMissing', render: () => <Audio variant="missing"/>, ...whole},
  {id: 'frames-audio-book', title: 'next.platform.frame.audioBook', render: () => <Audio variant="book"/>, ...whole},
  {id: 'frames-audio-loading', title: 'next.platform.frame.audioLoading', render: () => <Audio variant="loading"/>, ...whole},
  {id: 'frames-state', title: 'next.platform.frame.state', render: State, ...whole},
  {id: 'frames-elements', title: 'next.platform.frame.elements', render: Elements},
  {id: 'frames-regions-browse', title: 'next.platform.frame.regionsBrowse', render: RegionsBrowse, ...whole},
  {id: 'frames-regions-title', title: 'next.platform.frame.regionsTitle', render: RegionsTitle, ...whole},
  {id: 'frames-regions-settings', title: 'next.platform.frame.regionsSettings', render: RegionsSettings, ...whole},
  {id: 'frames-regions-sheet', title: 'next.platform.frame.regionsSheet', render: RegionsSheet, ...whole},
  {id: 'frames-regions-sign-in', title: 'next.platform.frame.regionsSignIn', render: RegionsSignIn, platforms: ['phone', 'tablet', 'web'], ...whole},
  {id: 'frames-regions-player', title: 'next.platform.frame.regionsPlayer', render: RegionsPlayer, ...whole},
  {id: 'frames-regions-audio', title: 'next.platform.frame.regionsAudio', render: RegionsAudio, ...whole},
  {id: 'frames-regions-state', title: 'next.platform.frame.regionsState', render: RegionsState, ...whole},
];
export default galleries;
