import React, {useRef, useState, type ReactNode} from 'react';
import {Platform, ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {framesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures';
import {AmbientArt, Artwork, ChannelMark, type ArtworkSource} from '../../../kit/artwork/index';
import {EpisodeTile, PosterCard, SquareCard, WideCard} from '../../../kit/cards';
import {Button, KeyHints, Switch, Tabs, TextField} from '../../../kit/controls';
import {SongRow} from '../../../kit/browse/Tracks';
import {ArtBox, AudioPlayerFrame, BrowseFrame, HomeHero, PageStateFrame, PlayerChromeFrame, Section, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, SignInFrame, StateRow, TitleFrame,
  type AudioControl, type AudioOption} from '../../../kit/frames';
import {frameSpec} from '@portico/design';
import {useFrameMetrics, usePlayerUpright} from '../../../kit/frames/metrics';
import {PlayerChannelStep, PlayerControl, PlayerFilmstrip, PlayerLiveActions, PlayerPanel, PlayerPanelGroup, PlayerPanelRow, PlayerPill, PlayerPrompt, PlayerScrubBar, PlayerScrubPreview, PlayerTimes, PlayerTitle, PlayerUpNext} from '../../../kit/player';
import {HeroMetadata, Row, SeasonBar, type LayoutItemContext, type LayoutSelection} from '../../../kit/layout';
import {LargeTitleHeader, SettingsNav, SettingsRow, ShortcutTiles} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {MediumRail} from '../../../shell/MediumRail';
import {TVRail} from '../../../shell/TVRail';
import {tvRailScrim} from '../../../shell/NativeShell';
import {FocusProvider, StartFocusScope, focusTestID} from '../../../kit/focus';
import {PhoneTabBar} from '../../../shell/PhoneTabBar';
import {mediumRailMetrics, phoneBarHeight, type ShellDestination} from '../../../shell/model';
import {NativePageInsetProvider} from '../../../kit/layout/page-inset';
import type {Gallery} from '../../gallery-registry';

/** The screen frames, each drawn whole from the app's own components with reference text and artwork. One section
 * per frame and state; README.md beside this file lists them and how to open each. Development builds only. */
type FixtureName = keyof typeof framesFixtureMessages extends `next.frames.fixture.${infer Name}` ? Name : never;
const f = (name: FixtureName) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
/** The reference artwork the capture run serves (the same files the mock-ups use). */
export const asset = (path: string): ArtworkSource => ({uri: `http://100.120.50.24:8787/assets/${path}`, cacheKey: `frames-reference:${path}`});
const none = () => {};
/** A stand-in signed-in scope for the shell's TV rail, which names its focus stops by it. */
const galleryAuthority = {serverId: 'gallery', userId: 'gallery', profileId: 'gallery', epoch: '1', viewerFence: '1'} as never;
/** The tablet sidebar's width (shell/NativeShell). */
const sidebarWidth = 248;
const libraries = ([['movie', 'movies'], ['tv', 'tvShows'], ['anime', 'anime'], ['music', 'musicLibrary'], ['audiobook', 'audiobooks']] as const).map(([kind, name]) => ({id: kind, name: f(name), kind}));
const selection: LayoutSelection = {active: false, isSelected: () => false, onToggle: none, onEnter: none};
const card = ({onFocus, onBlur, focusId, testID}: LayoutItemContext) => ({onFocus, onBlur, focusId, testID});
/** Fixture facts: the first is the year; a runtime, a season count and a rating carry the ids the kit draws as chips. */
const chip = (part: string) => /^\d+ h( \d+ min)?$|^\d+ min$/.test(part) ? 'runtime' : / seasons?$/.test(part) ? 'seasons' : /^(G|PG|PG-13|R|TV-[A-Z0-9]+)$/.test(part) ? 'rating' : undefined;
const fact = (text: string) => text.split(' · ').map((part, index) => ({id: chip(part) ?? (index === 0 ? 'year' : `fact-${index}`), text: part, role: index === 0 ? 'identity' as const : 'context' as const}));

const posters = ['spirited-away', 'inside-out', 'alien', 'blade-runner', 'dune', 'barbie', 'back-to-the-future', 'the-dark-knight', 'avatar', 'aliens'].map(id => ({id, title: id.split('-').map(word => word[0].toUpperCase() + word.slice(1)).join(' '), art: asset(`pool/movies/${id}.jpg`)}));
const wides = [{id: 'tlou', title: f('tlou'), art: asset('bd-tlou.jpg')}, {id: 'inside-out', title: f('insideOut'), art: asset('bd-insideout.jpg')}, {id: 'spirited', title: f('spirited'), art: asset('bd-spirited.jpg')}];

/** One whole window for a frame: frames keep their own safe areas and gutters, so the Kit page's gutter is undone. */
export function Window({children}: {children: ReactNode}) {
  const theme = useTheme(), window = useWindowDimensions();
  return <View style={{height: window.height, marginHorizontal: -theme.gutter}}>{children}</View>;
}

/** A page as the app shows it: with the shell's own tab bar on a phone and its rail on a tablet, so the frame is judged
 * where it lives. The TV rail needs a signed-in session and is not drawn here. */
export function Page({children, destination}: {children: ReactNode; destination: ShellDestination}) {
  const theme = useTheme(), window = useWindowDimensions(), insets = useSafeAreaInsets(), {form} = useFrameMetrics();
  const landscape = window.width > window.height, rail = form === 'medium' || form === 'expanded', sidebar = theme.layoutMode === 'desktop';
  const inset = rail ? sidebar ? sidebarWidth : mediumRailMetrics.width : 0;
  const profileRef = useRef<View>(null), [railOpen, setRailOpen] = useState(false);
  return <View style={{height: window.height, marginHorizontal: -theme.gutter}}>
    {/* The shell draws the rail over the window's left edge and gives the page the room beside it. On TV the open rail
        pushes the page right under the shell's one rail scrim, a solid panel running into an even dim (tv-2-rail-open). */}
    <View style={{position: 'absolute', top: 0, bottom: form === 'phone' ? phoneBarHeight(insets.bottom, landscape) : 0, left: inset, right: 0, transform: railOpen ? [{translateX: theme.spec.navigation.tv.contentShift}] : undefined}}><NativePageInsetProvider value={inset}>{children}</NativePageInsetProvider></View>
    {form === 'tv' && <View pointerEvents="none" style={{position: 'absolute', inset: 0, experimental_backgroundImage: tvRailScrim(theme.colors.ink, railOpen)}}/>}
    {rail && <MediumRail expanded={sidebar} brand={sidebar} serverName={f('serverName')} libraries={libraries} hasLiveTV destination={destination} profile={{id: 'gallery', name: f('profileInitial'), initial: f('profileInitial'), color: 'blue'}} unread
      notificationAction={{label: f('notifications'), onPress: none}} serverAction={{id: 'activity', label: f('serverActivity'), onPress: none}} onNavigate={none} onProfile={none} profileRef={profileRef} short={window.height < theme.layout.breakpoint.phone}/>}
    {form === 'phone' && <PhoneTabBar destination={destination} hasLiveTV onSelect={none} landscape={landscape}/>}
    {/* TV: the shell's own rail, collapsed, over the page's left edge as the app draws it. */}
    {form === 'tv' && <FocusProvider><TVRail authority={galleryAuthority} libraries={libraries} hasLiveTV destination={destination} profile={{id: 'gallery', name: f('profileInitial'), initial: f('profileInitial'), color: 'blue'}} onNavigate={none} onProfile={none} profileRef={profileRef} onExpandedChange={setRailOpen}/></FocusProvider>}
  </View>;
}

export function PosterRow({title, fresh = false, testID}: {title: string; /** The first few are new. */ fresh?: boolean; testID?: string}) {
  const theme = useTheme(), width = theme.spec.card.width.poster[theme.column];
  return <Row testID={testID} title={title} items={posters} getKey={item => item.id} itemWidth={width} itemHeight={width * 1.5} selection={selection}
    renderItem={(item, context) => <PosterCard {...card(context)} title={item.title} artwork={item.art} width={width} isNew={fresh && posters.indexOf(item) < 3} onNavigate={none}/>}/>;
}
function WideRow({testID}: {testID?: string}) {
  const theme = useTheme(), width = theme.spec.card.width.wide[theme.column];
  return <Row testID={testID} title={f('continueWatching')} items={wides} getKey={item => item.id} itemWidth={width} itemHeight={width * 9 / 16} selection={selection}
    renderItem={(item, context) => <WideCard {...card(context)} title={item.title} artwork={item.art} width={width} progress={.4} caption={[{id: 'episode-label', text: f('wideEpisode'), role: 'identity'}, {id: 'remaining', text: f('wideLeft'), role: 'context'}]} primaryAction="play" onPlay={none} onNavigate={none}/>}/>;
}

const heroes = [
  {id: 'spirited', title: f('spirited'), reason: f('spiritedReason'), overview: f('tlouSynopsis'), facts: fact(f('spiritedFacts')), art: asset('bd-spirited.jpg')},
  {id: 'tlou', title: f('tlou'), overview: f('tlouSynopsis'), facts: fact(f('tlouFacts')), art: asset('bd-tlou.jpg')},
];
function BrowseHome() {
  // The sample hero holds its first title: the real one moves on by itself, and a shot taken mid-change shows a faded hero.
  const index = 0, setIndex = none, art = (id: string) => heroes.find(entry => entry.id === id)?.art, {form} = useFrameMetrics();
  // TV (owner, 18 Nov): Home starts on the first Continue Watching card and the hero has no buttons; its hint row says
  // what OK and Hold OK do on the focused card (final3-tv-01).
  const actions = () => form === 'tv' ? <KeyHints hints={[{key: f('keyOk'), label: f('hintResume')}, {key: f('keyHoldOk'), label: f('hintMoreOptions')}]}/>
    : <><Button icon="i-play" onPress={none}>{f('play')}</Button><Button variant="glass" icon="i-plus" onPress={none}>{f('myList')}</Button></>;
  return <Page destination={{kind: 'home'}}><BrowseFrame testID="frames-browse-home" startFocus={`${homeContinue}-item-tlou`} hero={<HomeHero candidates={heroes} activeIndex={index} onIndexChange={setIndex}
    renderArtwork={(entry, context) => <AmbientArt foreground={art(entry.id)} visible={context.visible} foregroundStyle={context.foregroundStyle} onReady={context.onReady} onError={context.onError}/>}
    renderActions={actions}/>}>
    <WideRow testID={homeContinue}/><PosterRow fresh title={f('newOnServer')}/>
  </BrowseFrame></Page>;
}
const homeContinue = 'frames-browse-home-continue', listFirst = 'frames-browse-list-new';
const BrowseList = () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseFrame testID="frames-browse-list" startFocus={`${listFirst}-item-spirited-away`} header={{title: f('movies'), back: {label: f('libraries'), onPress: none}, trailing: <Button variant="glass" size="small" icon="i-filter" onPress={none}>{f('filters')}</Button>}}>
  <PosterRow testID={listFirst} title={f('newOnServer')}/><PosterRow title={f('moreLikeThis')}/>
</BrowseFrame></Page>;
const BrowseLoading = () => <Window><BrowseFrame testID="frames-browse-loading" header={{title: f('movies')}} state={{kind: 'loading'}}/></Window>;

/** A show's episode list as the title page and the watch page draw it (phone-player 4, tablet-play 3): the kit's season
 * bar, then the kit's episode rows with their states. */
const episodes = [
  {name: 'episode1', number: 'episodeNumber1', status: 'episodeWatched', length: 'episodeLength1', watched: true, progress: undefined, playing: false, art: true},
  {name: 'episode2', number: 'episodeNumber2', status: 'episodePlaying', length: 'episodeLeft2', watched: false, progress: .26, playing: true, art: true},
  {name: 'episode3', number: 'episodeNumber3', status: 'episodeUpNext', length: 'episodeLength3', watched: false, progress: undefined, playing: false, art: false},
] as const;
function EpisodeRows({panel = false}: {panel?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics(), width = (panel ? frameSpec.player.panel.width[frame.form] || frame.width : frame.width) - 2 * (panel ? frameSpec.player.panel.padding[frame.form] : frame.gutter);
  return <View style={{gap: theme.space.sm}}>{episodes.map(episode => <EpisodeTile key={episode.name} testID={`frames-episode-${episode.name}`} layout="row" width={width} artWidth={panel ? frameSpec.player.panel.still[frame.form] : frame.form === 'phone' ? undefined : frameSpec.player.panel.still.tv}
    title={f(episode.name)} label={[{id: 'episode', text: f(episode.number), role: 'context'}]} status={f(episode.status)} length={f(episode.length)} synopsis={f('tlouSynopsis')}
    artwork={episode.art ? asset('bd-tlou.jpg') : undefined} watched={episode.watched} progress={episode.progress} playing={episode.playing} onNavigate={none} onDownload={theme.isTV ? undefined : none}/>)}</View>;
}
function Episodes() {
  const theme = useTheme(), frame = useFrameMetrics(), [hide, setHide] = useState(false);
  return <View style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}>
    <SeasonBar label={f('episodesSeason')} count={f('episodesCount')} onSeasons={none} hideWatched={hide} onHideWatched={setHide}/>
    <EpisodeRows/>
  </View>;
}
function Title({variant}: {variant: 'full' | 'missing' | 'loading'}) {
  const [tab, setTab] = useState('more'), full = variant !== 'missing', {form} = useFrameMetrics();
  return <Page destination={{kind: 'library', libraryId: 'movie'}}><TitleFrame testID={`frames-title-${variant}`} back={{label: f('back'), onPress: none}} topAction={{icon: 'i-cast', label: f('playOn'), onPress: none}}
    art={<AmbientArt treatment="title" foreground={full ? asset('bd-insideout.jpg') : undefined}/>}
    title={full ? f('insideOut') : f('longTitle')} metadata={<HeroMetadata facts={fact(full ? `${f('insideOutFacts')} · ${f('insideOutRating')}` : f('longFacts'))} centered={form === 'phone'}/>}
    primary={full ? {icon: 'i-play', label: f('resume'), progress: .09, remaining: f('resumeLeft'), startOver: {label: f('startOver'), onPress: none}, onPress: none} : {icon: 'i-play', label: f('play'), onPress: none}}
    actions={full ? [{icon: 'i-check', label: f('myList'), labelled: true, active: true, onPress: none}, {icon: 'i-dl', label: f('download'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}] : []}
    tagline={full ? f('insideOutTagline') : undefined} synopsis={full ? f('insideOutSynopsis') : undefined}
    credits={full ? [{label: f('directedBy'), names: f('insideOutDirector')}, {label: f('starring'), names: f('insideOutCast')}] : undefined}
    tabs={{items: [{value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}], value: tab, onValueChange: setTab}}
    state={variant === 'loading' ? {kind: 'loading'} : undefined}>
    <PosterRow title={f('moreLikeInsideOut')}/><WideRow/><PosterRow title={f('newOnServer')}/>
  </TitleFrame></Page>;
}

function PlaybackSections() {
  const [autoplay, setAutoplay] = useState(true);
  return <>
    <SectionCard label={f('nextEpisode')}>
      <SettingsRow first label={f('autoplay')} control={<Switch testID="frames-settings-autoplay" checked={autoplay} onCheckedChange={setAutoplay} accessibilityLabel={f('autoplay')}/>}/>
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
      <SettingsRow testID="frames-settings-source1" first icon="i-live" label={f('source1')} description={f('source1Meta')} navigates onPress={none}/>
      <SettingsRow icon="i-live" label={f('source2')} description={f('source2Meta')} navigates onPress={none}/>
    </SectionCard>
    <SectionCard label={f('guide')}><StateRow kind="loading"/></SectionCard>
    <SectionCard label={f('recordings')}><StateRow kind="empty" icon="i-rec" title={f('noRecordings')} reason={f('noRecordingsReason')} action={{label: f('openGuide'), onPress: none}}/></SectionCard>
  </>;
}
/** The Settings index is the shell's own parts, passed as the frame's list: the quick tiles and `SettingsNav` (the
 * account's pages, then the server's, the current page marked). The frame places it and the page beside it. */
export const settingsPages = {
  account: ([['profile', 'i-person', 'settingsProfile'], ['playback', 'i-play', 'playback'], ['audio', 'i-subs', 'audioSubtitles'], ['quality', 'i-sliders', 'quality'], ['music', 'i-music', 'music'], ['appearance', 'i-image', 'appearance']] as const).map(([id, icon, name]) => ({id, icon, label: f(name)})),
  server: ([['dashboard', 'i-activity', 'settingsDashboard'], ['libraries', 'i-library', 'libraries'], ['live', 'i-live', 'liveTv']] as const).map(([id, icon, name]) => ({id, icon, label: f(name)})),
};
function SettingsList() {
  const [page, setPage] = useState<string>(), {form} = useFrameMetrics(), wide = form !== 'phone';
  const tiles = ([['profiles', 'i-person', 'settingsSwitchProfile'], ['servers', 'i-server', 'switchServer'], ['notifications', 'i-bell', 'notifications'], ['together', 'i-people', 'settingsTogether']] as const).map(([id, icon, name]) => ({id, icon, label: f(name), onPress: none}));
  const list = <View>{form !== 'phone' && <LargeTitleHeader title={f('settings')} style={{paddingHorizontal: 0}}/>}{form === 'phone' && <ShortcutTiles testID="frames-settings-tiles" actions={tiles}/>}
    <SettingsNav testID="frames-settings-nav" accountPages={settingsPages.account} serverPages={settingsPages.server} viewerRole="owner" serverName={f('serverName')} selectedId={form === 'tv' ? 'playback' : page ?? (wide ? 'playback' : '')} onNavigate={form === 'tv' ? none : setPage} mode={form === 'tv' ? 'account' : 'combined'}/></View>;
  const hints = <KeyHints hints={[{key: [f('keyUp'), f('keyDown')], label: f('hintChoosePage')}, {key: f('keyRight'), label: f('hintEditRows')}, {key: f('keyLeft'), label: f('hintPages')}]}/>;
  return <Window><SettingsListFrame testID="frames-settings-list" startFocus={focusTestID('frames-settings-nav', JSON.stringify(['account', 'playback']))} hints={hints} open onOpenChange={none} bar={{title: f('settings'), action: {label: f('done'), onPress: none}}} list={list}
    page={page || wide ? <SettingsPageFrame title={f('playback')} back={{label: f('settings'), onPress: () => setPage(undefined)}} intro={f('playbackIntro')}><PlaybackSections/></SettingsPageFrame> : undefined}/></Window>;
}
const SettingsPage = () => <Window><SettingsPageFrame testID="frames-settings-page" startFocus="frames-settings-source1" title={f('liveTv')} back={{label: f('settings'), onPress: none}} intro={f('playbackIntro')}><StateSections/></SettingsPageFrame></Window>;

const Sheet = () => <Window><BrowseFrame header={{title: f('liveTv')}}><View/></BrowseFrame>
  <SheetFrame kind="task" task="task" testID="frames-sheet" startFocus="frames-sheet-playlist" hints={<KeyHints hints={[{key: f('keyOk'), label: f('hintChoose')}, {key: f('keyBack'), label: f('hintClose')}]}/>} open onOpenChange={none} title={f('addSource')}>
    <SettingsRow testID="frames-sheet-playlist" first icon="i-link" label={f('playlist')} description={f('playlistMeta')} navigates onPress={none}/>
    <SettingsRow testID="frames-sheet-tuners" icon="i-wifi" label={f('findTuners')} description={f('findTunersMeta')} navigates onPress={none}/>
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
/** The watch page under the upright player's picture (phone-player 4, tablet-play 3): the title page's own content, without its art. */
/** Under an upright live picture: what is on, on which channel, and what follows. */
function LivePage() {
  const theme = useTheme(), frame = useFrameMetrics();
  return <ScrollView contentContainerStyle={{padding: frame.gutter, gap: theme.space.sm}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}><ChannelMark mark={{text: f('liveMark'), seed: 'synthetic:sse'} as never} size={theme.targets.touch}/>
      <View style={{flex: 1}}><Text style={[theme.type.rowLabel, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{f('liveChannelName')}  {f('liveNumber')}</Text><Text style={[theme.type.rowHelp, {color: theme.colors.record}]}>{f('liveNow')}</Text></View></View>
    <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text}]}>{f('liveTitle')}</Text>
    <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{f('liveThen')}</Text>
    <View style={{flexDirection: 'row', gap: theme.spec.hero.actions.gap[theme.column]}}><Button testID="frames-live-guide" variant="glass" icon="i-list" onPress={none}>{f('liveGuide')}</Button><Button testID="frames-live-record" variant="glass" icon="i-rec" iconColor={theme.colors.record} onPress={none}>{f('liveRecord')}</Button></View>
  </ScrollView>;
}
function WatchPage({paused = false}: {paused?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics(), [tab, setTab] = useState('episodes'), wide = frame.form !== 'phone';
  const words = <View style={{flexShrink: 1, gap: theme.space.tight}}><Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text}]}>{f('tlou')}</Text>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{f('videoLead')}  {f('videoSubtitle')}</Text><HeroMetadata facts={fact(f('tlouFacts'))}/></View>;
  const actions = <View style={{flexDirection: 'row', gap: theme.spec.hero.actions.gap[theme.column]}}><Button icon={paused ? 'i-play' : 'i-pause'} align="stretch" style={wide ? undefined : {flex: 1}} onPress={none}>{f(paused ? 'play' : 'pause')}</Button>
    {([['i-plus', 'myList'], ['i-dl', 'download'], ['i-more', 'more']] as const).map(([icon, name]) => <Button key={name} variant="glass" iconOnly icon={icon} accessibilityLabel={f(name)} onPress={none}/>)}</View>;
  return <ScrollView contentContainerStyle={{paddingVertical: theme.space.sm, gap: theme.space.sm}}>
    <View style={[{marginHorizontal: frame.gutter, gap: theme.space.sm}, wide && {flexDirection: 'row', alignItems: 'flex-end', justifyContent: 'space-between'}]}>{words}{actions}</View>
    <Tabs items={[{value: 'episodes', label: f('episodes')}, {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]} value={tab} onValueChange={setTab}/>
    <Episodes/>
  </ScrollView>;
}
/** The video player filled with the kit's player parts, with what each mock-up puts in each place for its form
 * (tablet-play 1 and tv-player 1; phone-player 1 on its side; phone-player 4 and tablet-play 3 upright). Nothing moves between the states. */
function Player({state = 'playing', panel, card, id = `frames-player-${state}`, start}: {state?: PlayerState; /** A `PlayerPanel`, open. */ panel?: ReactNode; /** Up next, over the credits. */ card?: ReactNode; id?: string;
  /** TV: where the case starts (R1); Play or Pause unless a panel or card is open. */ start?: string}) {
  const {form} = useFrameMetrics(), phone = form === 'phone', tv = form === 'tv', upright = usePlayerUpright(), tablet = !phone && !tv && !upright, live = state === 'live', missing = state === 'missing', scrubbing = state === 'scrubbing';
  const still = <Artwork source={asset('bd-tlou.jpg')}/>;
  const tool = (icon: AudioControl['icon'], name: FixtureName, disabled = false) => <PlayerControl key={name} testID={`${id}-${name}`} onPicture icon={icon} label={f(name)} disabled={disabled} onPress={none}/>;
  const plain = (icon: AudioControl['icon'], name: FixtureName, disabled = false) => <PlayerControl key={name} testID={`${id}-${name}`} icon={icon} label={f(name)} disabled={disabled} onPress={none}/>;
  const quality = <PlayerPill testID={`${id}-quality`} icon={tablet ? undefined : 'i-quality'} name={tv ? f('qualityShort') : undefined} qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none}/>;
  const subtitles = <PlayerPill testID={`${id}-subtitles`} icon="i-cc" name={f('subtitles')} value={f('english')} label={f('subtitles')} disabled={missing} onPress={none}/>;
  const audio = <PlayerPill testID={`${id}-audio`} icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} disabled={missing} onPress={none}/>;
  // On touch and on TV the knob goes to the time being previewed (phone-player 1b, tv-player 2).
  const at = scrubbing ? .65 : live ? .96 : .29;
  return <Window><PlayerChromeFrame testID={id} startFocus={start ?? `${id}-toggle`} chromeVisible scrubbing={scrubbing} panel={panel} card={card} picture={still} close={tool('i-down', 'close')} title={<PlayerTitle title={f('tlou')} lead={f('videoLead')} subtitle={f('videoSubtitle')}/>}
    tools={upright ? <>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{tool('i-cc', 'subtitles', missing)}{tool('i-settings', 'playerSettings')}</>
      : tablet ? <>{tool('i-cast', 'playOn')}{tool('i-more', 'more')}{tool('i-x', 'dismiss')}</> : <>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{tool('i-settings', 'playerSettings')}</>}
    scrubber={<PlayerScrubBar value={at} buffered={[{start: at, end: live ? 1 : at + .14}]} marks={missing || live ? [] : [{start: .02, end: .08}, {start: .95, end: 1}]}
      preview={scrubbing && !tv ? <PlayerScrubPreview at={at} time={f('previewTime')}>{still}</PlayerScrubPreview> : undefined}
      filmstrip={scrubbing && tv ? <PlayerFilmstrip frames={(['film1', 'film2', 'previewTime', 'film4', 'film5'] as const).map(name => ({time: f(name), picture: still}))}/> : undefined}
      labels={scrubbing && tv ? [{at: 0, text: f('markRecap')}, {at: .05, text: f('markIntro')}, {at: 1, text: f('markCredits'), end: true}] : undefined}/>}
    hints={<KeyHints hints={[{key: f('keyMove'), label: f('hintMove')}, {key: f('keyHold'), label: f('hintFaster')}, {key: f('keyOk'), label: f('hintPlayFrom')}, {key: f('keyBack'), label: f('hintReturn')}]}/>}
    times={{position: f(scrubbing ? 'previewTime' : 'elapsed'), remaining: f('remaining'), duration: f('duration'), endsAt: live ? undefined : f('endsAt'), live: live ? f('live') : undefined, clock: f('clock')}}
    transport={<><PlayerControl testID={`${id}-previous`} icon="i-prev" label={f('previous')} disabled={live || missing} onPress={none}/><PlayerControl testID={`${id}-rewind`} icon="i-rew" skip={10} label={f('rewind')} onPress={none}/>
      <PlayerControl testID={`${id}-toggle`} primary icon={state === 'paused' ? 'i-play' : 'i-pause'} busy={state === 'buffering'} label={f(state === 'paused' ? 'play' : 'pause')} onPress={none}/>
      <PlayerControl testID={`${id}-forward`} icon="i-ffw" skip={30} label={f('forward')} disabled={live} onPress={none}/><PlayerControl testID={`${id}-next`} icon="i-next" label={f('next')} disabled={live || missing} onPress={none}/></>}
    leading={<Button testID={`${id}-episodes`} variant="glass" size="small" icon="i-episodes" disabled={live} onPress={none}>{f('episodesButton')}</Button>}
    trailing={upright ? <>{quality}{plain('i-full', 'fullScreen')}</>
      : tablet ? <>{plain('i-cc', 'subtitles', missing)}{quality}<PlayerPill testID={`${id}-speed`} value={f('speedShort')} label={f('optSpeed')} onPress={none}/>{plain('i-pip', 'pictureInPicture')}</>
      : tv ? <>{subtitles}{audio}{quality}{plain('i-sliders', 'playerOptions')}{plain('i-pip', 'pictureInPicture')}</> : <>{subtitles}{audio}{quality}</>}
    prompts={(state === 'playing' ? <PlayerPrompt testID={`${id}-skip`} label={f('skipIntro')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}}/> : undefined)}
    body={<WatchPage paused={state === 'paused'}/>}/></Window>;
}

/** The player's own panels (kit/player), each open on the paused player where the frame places it for the form:
 * Playback options and the lists it opens (phone-player 3 and 3b, tablet-play 2b, tv-player 4), then Episodes (tablet-play 2a). */
type PanelName = 'options' | 'subtitles' | 'audio' | 'quality' | 'speed';
function PlayerPanelCase({panel}: {panel: PanelName}) {
  const [forced, setForced] = useState(false), id = `frames-player-${panel}`;
  const close = {label: f('optDone'), onPress: none}, back = {label: f('optBack'), onPress: none};
  const row = (label: FixtureName, detail?: FixtureName, selected = false, unavailable = false, trailing?: ReactNode) => <PlayerPanelRow key={label} testID={`${id}-${label}`} label={f(label)} detail={detail && f(detail)} selected={selected} unavailable={unavailable ? f('optUnavailable') : undefined} trailing={trailing} onPress={none}/>;
  const entry = (icon: AudioControl['icon'], label: FixtureName, value: FixtureName, qualifier?: FixtureName) => <PlayerPanelRow key={label} testID={`${id}-${label}`} icon={icon} label={f(label)} value={f(value)} qualifier={qualifier && f(qualifier)} onPress={none}/>;
  const body = {
    options: <><PlayerPanelGroup>{entry('i-cc', 'subtitles', 'english')}{entry('i-audio', 'audio', 'english')}{entry('i-quality', 'qualityShort', 'qualityValue', 'optAutomaticShort')}{entry('i-gauge', 'optSpeed', 'optNormal')}{entry('i-moon', 'optSleep', 'optOff')}</PlayerPanelGroup>
      <PlayerPanelGroup label={f('optThisTitle')}>{entry('i-queue', 'upNext', 'optUpNextCount')}</PlayerPanelGroup></>,
    subtitles: <><PlayerPanelGroup>{row('optOff')}{row('english', 'optSrt', true)}{row('optEnglishSdh', 'optSrt')}{row('optSpanish', 'optSrt')}{row('optGerman', 'optNotAdded', false, false, <Button variant="glass" size="xs" icon="i-plus" onPress={none}>{f('optAdd')}</Button>)}</PlayerPanelGroup>
      <PlayerPanelGroup>{row('optForced', 'optForcedHelp', false, false, <Switch checked={forced} onCheckedChange={setForced} accessibilityLabel={f('optForced')}/>)}</PlayerPanelGroup></>,
    audio: <PlayerPanelGroup note={f('optAudioNow')}>{row('english', 'optAac51', true)}{row('optSpanish', 'optAacStereo')}{row('optCommentary', 'optAacStereo', false, true)}</PlayerPanelGroup>,
    quality: <PlayerPanelGroup note={f('optQualityNow')}>{row('optOriginal', 'optOriginalDetail')}{row('optAutomatic', 'optAutomaticDetail', true)}{row('opt720', 'opt720Detail')}{row('opt480', 'opt480Detail')}</PlayerPanelGroup>,
    speed: <PlayerPanelGroup>{row('optSpeed05')}{row('optSpeed075')}{row('optNormal', undefined, true)}{row('optSpeed125')}{row('optSpeed15')}{row('optSpeed2')}</PlayerPanelGroup>,
  }[panel];
  const title = {options: 'optTitle', subtitles: 'subtitles', audio: 'audio', quality: 'qualityShort', speed: 'optSpeed'} as const;
  // R1: a list starts on its current choice, Playback options on its first row.
  const start = {options: 'subtitles', subtitles: 'english', audio: 'english', quality: 'optAutomatic', speed: 'optNormal'} as const;
  return <Player state="paused" id={id} start={`${id}-${start[panel]}`} panel={<PlayerPanel testID={`${id}-panel`} title={f(title[panel])} note={panel === 'options' ? f('optNote') : undefined} back={panel === 'options' ? undefined : back} close={close}>{body}</PlayerPanel>}/>;
}
function PlayerEpisodesCase() {
  const [hide, setHide] = useState(false);
  return <Player state="paused" id="frames-player-episodes" start="frames-episode-episode2" panel={<PlayerPanel testID="frames-player-episodes-panel" title={f('episodesTitle')} close={{label: f('optDone'), onPress: none}} footer={f('episodesNote')}>
    <SeasonBar label={f('episodesSeason')} count={f('episodesCount')} onSeasons={none} hideWatched={hide} onHideWatched={setHide}/>
    <EpisodeRows panel/>
  </PlayerPanel>}/>;
}
/** Up next over the credits (tv-player 5, phone-player 6): the kit card in the player's prompt corner, the controls hidden. */
function PlayerUpNextCase() {
  const {form} = useFrameMetrics();
  return <Player id="frames-player-up-next" start="watch-up-next-play" card={<PlayerUpNext compact={form !== 'tv'} item={{entryId: 'next', title: f('episode3'), durationMs: 76 * 60_000, episode: {seasonNumber: 1, number: 3}} as never} artwork={asset('bd-tlou.jpg')}
    countdown={{seconds: 42, automatic: false}} countdownTotal={60} sceneAfter={false} onPlayNow={none} onWatchCredits={none} onSkipCredits={none}/>}/>;
}
/** The Live TV player's controls on the player frame (tv-player 11, tablet-play 8, phone-player 1c): the channel's
 * mark and the Live mark in the title block, Record and More at the top, Guide, channel down and up either side of
 * the transport, the live bar, and Go to live in the prompt corner when behind. */
function PlayerLiveControlsCase() {
  const {form} = useFrameMetrics(), tv = form === 'tv', phone = form === 'phone', upright = usePlayerUpright(), id = 'frames-player-live-controls';
  const theme = useTheme(), still = <Artwork source={asset('bd-tlou.jpg')}/>, markSize = theme.spec.transport.control.size[theme.column];
  const quality = <PlayerPill testID={`${id}-quality`} name={tv ? f('qualityShort') : undefined} qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none}/>;
  const plain = (icon: AudioControl['icon'], name: FixtureName) => <PlayerControl key={name} testID={`${id}-${name}`} icon={icon} label={f(name)} onPress={none}/>;
  return <Window><PlayerChromeFrame testID={id} startFocus={`${id}-toggle`} chromeVisible picture={still} close={<PlayerControl testID={`${id}-close`} onPicture icon="i-down" label={f('close')} onPress={none}/>}
    title={<PlayerTitle mark={<ChannelMark mark={{text: f('liveMark'), seed: 'synthetic:sse'} as never} size={markSize}/>} title={f('liveTitle')} lead={f('liveChannelName')} subtitle={f('liveNumber')} live={f('liveNow')} detail={f('liveThen')}/>}
    tools={<>{upright && <PlayerControl testID={`${id}-guide`} onPicture icon="i-list" label={f('guide')} onPress={none}/>}<PlayerLiveActions enabled compact={phone || upright} record={{label: f('liveRecord'), onPress: none}} more={{onPress: none}}/></>}
    scrubber={<PlayerScrubBar value={.68} from={.55} buffered={[{start: .55, end: .7}]} liveEdge={.7}/>}
    times={{position: f('liveClock'), behind: f('liveBehind'), endsAt: f('liveEnds')}}
    transport={<><PlayerChannelStep testID={`${id}-channel-down`} direction="down" number={f('liveDown')} label={f('liveChannelDown')} onPress={none}/><PlayerControl testID={`${id}-rewind`} icon="i-rew" skip={10} label={f('rewind')} onPress={none}/>
      <PlayerControl testID={`${id}-toggle`} primary icon="i-pause" label={f('pause')} onPress={none}/><PlayerControl testID={`${id}-forward`} icon="i-ffw" skip={30} label={f('forward')} onPress={none}/>
      <PlayerChannelStep testID={`${id}-channel-up`} direction="up" number={f('liveUp')} label={f('liveChannelUp')} onPress={none}/></>}
    leading={<Button testID={`${id}-guide`} variant="glass" size="small" icon="i-list" onPress={none}>{f('liveGuide')}</Button>}
    trailing={tv ? <><PlayerPill testID={`${id}-captions`} name={f('captions')} value={f('optOff')} label={f('captions')} onPress={none}/>{quality}{plain('i-sliders', 'playerOptions')}{plain('i-pip', 'pictureInPicture')}</>
      : phone || upright ? quality : <>{plain('i-cc', 'subtitles')}{quality}{plain('i-pip', 'pictureInPicture')}</>}
    prompts={<PlayerPill testID={`${id}-go-live`} icon="i-rec" tone="record" value={f('liveGoToLive')} label={f('liveGoToLive')} onPress={none}/>}
    body={<LivePage/>}/></Window>;
}

function Audio({variant}: {variant: 'music' | 'missing' | 'book' | 'loading'}) {
  const theme = useTheme(), frame = useFrameMetrics(), tv = frame.form === 'tv', id = `frames-audio-${variant}`;
  const control = (icon: AudioControl['icon'], name: FixtureName, extra: Partial<AudioControl> = {}): AudioControl => ({icon, label: f(name), onPress: none, testID: `${id}-${name}`, ...extra});
  const option = (name: FixtureName, icon?: AudioOption['icon'], value?: FixtureName, disabled = false): AudioOption => ({label: f(name), icon, value: value && f(value), onPress: none, disabled, testID: `${id}-option-${name}`});
  const book = variant === 'book', missing = variant === 'missing';
  // The device this is playing on is named for what it is; a TV has no such entry and offers Add to playlist there (music-plus).
  const device = {inline: true, ...option(Platform.OS === 'ios' ? frame.form === 'phone' ? 'thisPhone' : 'thisTablet' : frame.form === 'phone' ? 'thisAndroid' : 'thisAndroidTablet', Platform.OS === 'ios' ? 'i-airplay' : 'i-cast')};
  const music = [control('i-shuffle', 'shuffle'), control('i-prev', 'previous'), control('i-pause', 'pause'), control('i-next', 'next', {disabled: missing}), control('i-repeat', 'repeat', {active: true})] as const;
  const reading = [control('i-prev', 'previous'), control('i-rew', 'rewind', {skip: 10}), control('i-pause', 'pause'), control('i-ffw', 'forward', {skip: 30}), control('i-next', 'next')] as const;
  // The queue is the screen's content: here the kit's song rows under their group labels (desktop-listen "now").
  const song = (key: string, title: string, duration: FixtureName, playing = false) => <SongRow key={key} testID={`${id}-queue-${key}`} focusId={`${id}-queue-${key}`} onPlay={none} onEnter={none}
    row={{id: key, playing, artwork: <Artwork source={asset('pool/albums/after-hours.jpg')}/>, duration: {id: 'duration', text: f(duration), role: 'context'}, text: {id: key, title, caption: [{id: 'artist', text: f('queueArtist'), role: 'context'}], detail: []}}}/>;
  const label = (name: FixtureName) => <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, fontFamily: theme.type.groupLabel.fontFamily, marginTop: theme.space.xs}]}>{f(name)}</Text>;
  // An audiobook's side column is its chapters, the one being read marked.
  const chapters = <View>{label('chapters')}{(['chapterRow7', 'chapterRow8', 'chapterRow9', 'chapterRow10'] as const).map((name, index) => <SongRow key={name} testID={`${id}-chapter-${name}`} focusId={`${id}-chapter-${name}`} onPlay={none} onEnter={none}
    row={{id: name, playing: index === 1, duration: {id: 'duration', text: f(`queueDuration${index + 1}` as FixtureName), role: 'context'}, text: {id: name, title: f(name), caption: [], detail: []}}}/>)}</View>;
  const queue = <View>{label('queueNow')}{song('now', f('songTitle'), 'queueDurationNow', true)}{label('queueNextFrom')}
    {(['queue1', 'queue2', 'queue3', 'queue4', 'queue5', 'queue6'] as const).map((name, index) => song(name, f(name), `queueDuration${index + 1}` as FixtureName))}</View>;
  return <Window><AudioPlayerFrame testID={id} startFocus={`${id}-pause`} loading={variant === 'loading'}
    close={control('i-down', 'close')} more={control('i-more', 'more')} favourite={control('i-heart', 'favourite')} context={book ? f('playingFromListening') : missing ? f('playingFromField') : f('playingFrom')} source={book ? f('playingSourceListening') : missing ? f('playingSourceField') : f('playingSource')}
    artwork={missing ? undefined : asset(book ? 'pool/books/the-hobbit.jpg' : 'pool/albums/after-hours.jpg')} icon={book ? 'i-book' : 'i-music'}
    title={book ? f('hobbit') : missing ? f('longSong') : f('songTitle')} byline={book ? f('tolkien') : missing ? f('choir') : f('songArtist')} chapter={book ? {label: f('chapter'), of: f('chapterOf')} : undefined}
    scrubber={<View style={{gap: frameSpec.player.timesGap[frame.form]}}><PlayerScrubBar value={.34} tone="white" knob={!tv}/><PlayerTimes position={f(book ? 'bookElapsed' : 'audioElapsed')} middle={book ? f('timeLeft') : undefined} remaining={f(book ? 'bookRemaining' : 'audioRemaining')}/></View>}
    transport={book ? reading : music} next={book || missing ? undefined : {label: f('nextLabel'), title: f('nextTitle')}}
    options={book ? [option('speed', undefined, 'speedValue'), {...option('sleep', undefined, 'sleepValue'), active: true}, option('chapters', undefined, 'chaptersValue'), ...(tv ? [] : [device])]
      : [option('lyrics', 'i-lyrics', undefined, missing), tv ? option('addToPlaylist', 'i-listplus') : device, option('upNext', 'i-queue')]}
    upNext={missing ? undefined : book ? chapters : queue}/></Window>;
}

const State = () => <Page destination={{kind: 'home'}}><PageStateFrame testID="frames-state" icon="i-srvoff" title={f('cantReach')} body={f('cantReachBody')} retry={{progress: .6, label: f('retryLabel'), detail: f('retryDetail')}}
  primaryAction={{id: 'retry', label: f('tryAgain'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'switch', label: f('switchServer'), icon: 'i-swap', onPress: none}}/></Page>;

/** The type roles, the four row kinds in one card, and the artwork boxes with and without a picture. */
function Elements() {
  const theme = useTheme(), frame = useFrameMetrics(), poster = theme.spec.card.width.poster[theme.column], square = theme.spec.card.width.square[theme.column];
  const roles = [['pageTitle', 'rolePageTitle', theme.colors.text], ['rowHeading', 'roleSectionHeading', theme.colors.text], ['groupLabel', 'roleGroupLabel', theme.colors.textSecondary], ['barTitle', 'roleBarTitle', theme.colors.text],
    ['rowLabel', 'roleRowLabel', theme.colors.text], ['rowHelp', 'roleRowHelp', theme.colors.textTertiary], ['intro', 'roleBody', theme.colors.textSecondary]] as const;
  const boxes = [['square', 'i-music', 'artMusic', square, undefined], ['square', 'i-book', 'artBook', square, undefined], ['poster', 'i-film', 'artPoster', poster, undefined], ['wide', 'i-tv', 'artWide', poster * 1.6, undefined],
    ['square', 'i-music', 'withArtwork', square, asset('pool/albums/thriller.jpg')], ['poster', 'i-film', 'withArtwork', poster, asset('pool/movies/dune.jpg')]] as const;
  return <StartFocusScope id="frames-settings-autoplay"><View testID="frames-elements" style={{marginHorizontal: -theme.gutter, paddingVertical: frame.sectionGap, gap: frame.sectionGap}}>
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
  </View></StartFocusScope>;
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
const RegionsSheet = () => <Window><SheetFrame kind="task" task="task" open onOpenChange={none} title={f('ownBarTitle')} foot={<Region name="regionFooter" height={frameSpec.sheet.footerClear}/>}><Block name="regionSheetRows" flexible/></SheetFrame></Window>;
const RegionsSignIn = () => <Window><SignInFrame back={own.back} title={f('ownTitle')} lede={f('ownLede')} terms={<Text style={{color: 'transparent'}}>{f('terms')}</Text>}><Region name="regionForm" flexible height={frameSpec.signIn.column.medium}/></SignInFrame></Window>;
const RegionsPlayer = () => <Window><PlayerChromeFrame chromeVisible picture={<Region name="regionPicture"/>} close={<Region name="regionClose" height={frameSpec.header.backRow}/>} title={<Region name="regionPlayerTitle" height={frameSpec.header.backRow}/>}
  tools={<Region name="regionTools" height={frameSpec.header.backRow}/>} scrubber={<Region name="regionScrubber" height={frameSpec.player.portrait.control}/>} times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration')}}
  transport={<View style={{width: frameSpec.audio.controlsWidth.expanded}}><Region name="regionTransport" height={frameSpec.header.backRow}/></View>} leading={<Region name="regionLeading" height={frameSpec.header.backRow}/>} trailing={<Region name="regionTrailing" height={frameSpec.header.backRow}/>}
  prompts={<View style={{width: frameSpec.audio.upNextWidth.expanded}}><Region name="regionPrompts" height={frameSpec.header.backRow}/></View>} body={<Region name="regionWatchPage" flexible/>}/></Window>;
function RegionsAudio() {
  const slot = (icon: AudioControl['icon']): AudioControl => ({icon, label: f('ownAction') + icon, onPress: none});
  return <Window><AudioPlayerFrame close={slot('i-down')} more={slot('i-more')} context={f('ownContext')} icon="i-music" title={f('ownTitle')} byline={f('ownByline')} scrubber={<Region name="regionScrubber" height={frameSpec.audio.scrubber.phone}/>}
    transport={[slot('i-shuffle'), slot('i-prev'), slot('i-pause'), slot('i-next'), slot('i-repeat')]} next={{label: f('ownNext'), title: f('ownNext')}}
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
