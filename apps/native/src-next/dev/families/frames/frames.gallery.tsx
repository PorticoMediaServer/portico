import React, {useRef, useState, type ReactNode} from 'react';
import {Text, View, useWindowDimensions} from 'react-native';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {framesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures';
import type {ArtworkSource} from '../../../kit/artwork/index';
import {PosterCard, SquareCard} from '../../../kit/cards';
import {cardCaptionHeight} from '../../../kit/cards/caption-metrics';
import {Button, KeyHints, Switch, TextField} from '../../../kit/controls';
import {ArtBox, BrowseFrame, PageStateFrame, Section, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, SignInFrame, StateRow, TitleFrame} from '../../../kit/frames';
import {frameSpec} from '@portico/design';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {Row, type LayoutItemContext, type LayoutSelection} from '../../../kit/layout';
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
export const f = (name: FixtureName) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
/** The reference artwork the capture run serves (the same files the mock-ups use). */
export const asset = (path: string): ArtworkSource => ({uri: `http://100.120.50.24:8787/assets/${path}`, cacheKey: `frames-reference:${path}`});
export const none = () => {};
/** A stand-in signed-in scope for the shell's TV rail, which names its focus stops by it. */
const galleryAuthority = {serverId: 'gallery', userId: 'gallery', profileId: 'gallery', epoch: '1', viewerFence: '1'} as never;
/** The tablet sidebar's width (shell/NativeShell). */
const sidebarWidth = 248;
export const libraries = ([['movie', 'movies'], ['tv', 'tvShows'], ['anime', 'anime'], ['music', 'musicLibrary'], ['audiobook', 'audiobooks']] as const).map(([kind, name]) => ({id: kind, name: f(name), kind}));
export const selection: LayoutSelection = {active: false, isSelected: () => false, onToggle: none, onEnter: none};
export const card = ({onFocus, onBlur, focusId, testID}: LayoutItemContext) => ({onFocus, onBlur, focusId, testID});
/** Fixture facts: the first is the year; a runtime, a season count and a rating carry the ids the kit draws as chips. */
const chip = (part: string) => /^\d+ h( \d+ min)?$|^\d+ min$/.test(part) ? 'duration' : / seasons?$/.test(part) ? 'season-count' : /^(G|PG|PG-13|R|TV-[A-Z0-9]+)$/.test(part) ? 'content-rating' : undefined;
export const fact = (text: string) => text.split(' · ').map((part, index) => ({id: chip(part) ?? (index === 0 ? 'year' : `fact-${index}`), text: part, role: index === 0 ? 'identity' as const : 'context' as const}));

const posters = ['spirited-away', 'inside-out', 'alien', 'blade-runner', 'dune', 'barbie', 'back-to-the-future', 'the-dark-knight', 'avatar', 'aliens'].map(id => ({id, title: id.split('-').map(word => word[0].toUpperCase() + word.slice(1)).join(' '), art: asset(`pool/movies/${id}.jpg`)}));
export const wides = [{id: 'tlou', title: f('tlou'), art: asset('bd-tlou.jpg')}, {id: 'inside-out', title: f('insideOut'), art: asset('bd-insideout.jpg')}, {id: 'spirited', title: f('spirited'), art: asset('bd-spirited.jpg')}];

/** One whole window for a frame: frames keep their own safe areas and gutters, so the Kit page's gutter is undone. */
export function Window({children}: {children: ReactNode}) {
  const theme = useTheme(), window = useWindowDimensions();
  return <View style={{height: window.height, marginHorizontal: -theme.gutter}}>{children}</View>;
}

/** A page as the app shows it: with the shell's own tab bar on a phone, its rail on a tablet and its rail on a TV, so the
 * frame is judged where it lives. Every rail draws the Portico mark at its top. */
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
    {rail && <MediumRail expanded={sidebar} serverName={f('serverName')} libraries={libraries} hasLiveTV destination={destination} profile={{id: 'gallery', name: f('profileInitial'), initial: f('profileInitial'), color: 'blue'}} unread
      notificationAction={{label: f('notifications'), onPress: none}} serverAction={{id: 'activity', label: f('serverActivity'), onPress: none}} onNavigate={none} onProfile={none} profileRef={profileRef} short={window.height - insets.top - insets.bottom < 800}/>}
    {form === 'phone' && <PhoneTabBar destination={destination} hasLiveTV onSelect={none} landscape={landscape}/>}
    {/* TV: the shell's own rail, collapsed, over the page's left edge as the app draws it. */}
    {form === 'tv' && <FocusProvider><TVRail authority={galleryAuthority} libraries={libraries} hasLiveTV destination={destination} profile={{id: 'gallery', name: f('profileInitial'), initial: f('profileInitial'), color: 'blue'}} onNavigate={none} onProfile={none} profileRef={profileRef} onExpandedChange={setRailOpen}/></FocusProvider>}
  </View>;
}

export function PosterRow({title, fresh = false, testID}: {title: string; /** The first few are new. */ fresh?: boolean; testID?: string}) {
  const theme = useTheme(), width = theme.spec.card.width.poster[theme.column], caption = cardCaptionHeight(theme, 2);
  return <Row testID={testID} title={title} items={posters} getKey={item => item.id} itemWidth={width} itemHeight={width * 1.5 + caption} selection={selection}
    renderItem={(item, context) => <PosterCard {...card(context)} title={item.title} artwork={item.art} width={width} isNew={fresh && posters.indexOf(item) < 3} onNavigate={none}/>}/>;
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
  return <Page destination={{kind: 'settings'}}><SettingsListFrame testID="frames-settings-list" startFocus={focusTestID('frames-settings-nav', JSON.stringify(['account', 'playback']))} hints={hints} open onOpenChange={none} bar={{title: f('settings'), action: {label: f('done'), onPress: none}}} list={list}
    page={page || wide ? <SettingsPageFrame title={f('playback')} back={{label: f('settings'), onPress: () => setPage(undefined)}} intro={f('playbackIntro')}><PlaybackSections/></SettingsPageFrame> : undefined}/></Page>;
}
const SettingsPage = () => <Page destination={{kind: 'settings'}}><SettingsPageFrame testID="frames-settings-page" startFocus="frames-settings-source1" title={f('liveTv')} back={{label: f('settings'), onPress: none}} intro={f('playbackIntro')}><StateSections/></SettingsPageFrame></Page>;

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
export function Region({name, label, flexible = false, height}: {name?: FixtureName; /** Another family's own region name. */ label?: string; flexible?: boolean; height?: number}) {
  const theme = useTheme(), frame = useFrameMetrics(), color = flexible ? theme.colors.amber : theme.colors.accentStrong;
  return <View style={{flex: height === undefined ? 1 : undefined, height, minHeight: height === undefined ? frame.rowTwoLine : undefined, overflow: 'hidden', alignSelf: 'stretch', borderWidth: 2, borderStyle: 'dashed', borderColor: color, borderRadius: theme.baseRadii.control, padding: theme.space.xs, justifyContent: 'center'}}>
    <Text style={[theme.type.rowLabel, {color}]}>{label ?? (name && f(name))}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{f(flexible ? 'regionFlexible' : 'regionScreen')}</Text>
  </View>;
}
/** An outlined region the size of a row of cards, at the frame's gutter. */
export function Block({name, flexible, rows = 3}: {name: FixtureName; flexible?: boolean; rows?: number}) {
  const frame = useFrameMetrics();
  return <View style={{marginHorizontal: frame.gutter}}><Region name={name} flexible={flexible} height={frame.rowTwoLine * rows}/></View>;
}
const own = {back: {label: f('ownBack'), onPress: none}, action: {label: f('ownAction'), onPress: none}};
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
const RegionsState = () => <Window><PageStateFrame icon="i-info" title={f('ownStateTitle')} body={f('ownStateBody')} primaryAction={{id: 'primary', label: f('ownPrimary'), onPress: none}} secondaryAction={{id: 'secondary', label: f('ownSecondary'), onPress: none}}/></Window>;

const whole = {screen: true} as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-backdrop', title: 'next.platform.frame.backdrop', render: () => <Window><View/></Window>, ...whole},
  {id: 'frames-settings-list', title: 'next.platform.frame.settingsList', render: SettingsList, ...whole},
  {id: 'frames-settings-page', title: 'next.platform.frame.settingsPage', render: SettingsPage, ...whole},
  {id: 'frames-sheet', title: 'next.platform.frame.sheet', render: Sheet, ...whole},
  {id: 'frames-sign-in', title: 'next.platform.frame.signIn', render: SignIn, platforms: ['phone', 'tablet', 'web'], ...whole},
  {id: 'frames-state', title: 'next.platform.frame.state', render: State, ...whole},
  {id: 'frames-elements', title: 'next.platform.frame.elements', render: Elements},
  {id: 'frames-regions-title', title: 'next.platform.frame.regionsTitle', render: RegionsTitle, ...whole},
  {id: 'frames-regions-settings', title: 'next.platform.frame.regionsSettings', render: RegionsSettings, ...whole},
  {id: 'frames-regions-sheet', title: 'next.platform.frame.regionsSheet', render: RegionsSheet, ...whole},
  {id: 'frames-regions-sign-in', title: 'next.platform.frame.regionsSignIn', render: RegionsSignIn, platforms: ['phone', 'tablet', 'web'], ...whole},
  {id: 'frames-regions-state', title: 'next.platform.frame.regionsState', render: RegionsState, ...whole},
];
export default galleries;
