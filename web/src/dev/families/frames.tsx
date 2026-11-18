import {useEffect, useMemo, useRef, useState, type ComponentType, type CSSProperties, type ReactNode} from 'react';
import {defaultI18n, formatMessage, type MessageId} from '@i18n';
import {ArtworkScheduler} from '@core/artwork';
import type {FactPart} from '@portico/client-core/kit-model';
import {framesFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures.ts';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {AmbientArt, ChannelMark, type ArtworkImage, type ArtworkSource} from '../../kit/artwork';
import {EpisodeTile, PosterCard, SquareCard, WideCard} from '../../kit/cards';
import {SongRow} from '../../kit/browse';
import {Button, Switch, Tabs, TextField} from '../../kit/controls';
import {ArtBox, AudioPlayerFrame, BrowseFrame, EpisodeRows, HomeHero, SeasonBar, PageStateFrame, PlayerChromeFrame, Section, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, SignInFrame, StateRow, TitleFrame,
  type AudioControl, type AudioOption} from '../../kit/frames';
import {Icon} from '../../kit/Icon';
import {HeroFacts, Shelf} from '../../kit/layout';
import {SeekLine} from '../../kit/media';
import {PlayerControl, PlayerEpisodes, PlayerLive, PlayerOptions, PlayerPill, PlayerPrompt, PlayerScrubPreview, PlayerTimes, PlayerTitle, PlayerUpNext, PlayerVolume,
  type PlayerEpisodeList, type PlayerOptionsModel, type PlayerPanel, type PlayerPanelContext, type PlayerRadioChoice} from '../../kit/player';
import type {TrackChoice} from '../../kit/media';
import {livePlayerPort} from '../live-player-fixture';
import {usePhone} from '../../kit/overlays/shared';
import {SettingsNav, SettingsRow, TopBarActivity, type ActivityPanelView} from '../../kit/shell';
import {DesktopNavigationUnderline} from '../../shell/DesktopNavigationUnderline';
import {shellFixtures, shellReference as shellSample} from '../../../../packages/i18n/src/catalog/en-US/next/dev/shell-fixtures';
import {ProfileAvatarFace} from '../../kit/shell';
import frame from '../../kit/frames/frames.module.css';
import shell from '../../shell/shell.module.css';
import wordmark from '../../shell/wordmark.svg';

/** The screen frames, each drawn whole from the app's own components with reference text and artwork. One case per
 * frame and state, with the native gallery's ids; README.md in kit/frames lists them and how to open each.
 * Every case answers to the window's width, so the same case is captured at 390 and at 1440. Serve-only. */
type FixtureName = keyof typeof framesFixtureMessages extends `next.frames.fixture.${infer Name}` ? Name : never;
const f = (name: FixtureName) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
const none = () => {};
const fact = (text: string): FactPart[] => text.split(' · ').map((part, index) => ({id: index === 0 ? 'year' : `fact-${index}`, text: part, role: index === 0 ? 'identity' : 'context'}));

/** The reference artwork the capture run serves (the same files the native gallery and the mock-ups use). */
export function useReferenceArtwork() {
  const scheduler = useMemo(() => new ArtworkScheduler<ArtworkImage>({load: (key, signal) => new Promise((resolve, reject) => {
    const picture = new Image();
    const clear = () => {signal.removeEventListener('abort', abort); picture.onload = null; picture.onerror = null;};
    const abort = () => {clear(); picture.src = ''; reject(new DOMException('Aborted', 'AbortError'));};
    signal.addEventListener('abort', abort, {once: true});
    picture.onload = () => {void picture.decode().then(() => {clear(); if (signal.aborted) reject(new DOMException('Aborted', 'AbortError')); else resolve({src: picture.src, width: picture.naturalWidth, height: picture.naturalHeight});}, error => {clear(); reject(error);});};
    picture.onerror = () => {clear(); reject(new Error('Reference artwork failed to decode.'));};
    if (signal.aborted) abort(); else picture.src = new URL(`assets/${key}`, 'http://100.120.50.24:8787/').href;
  }), classify: () => ({retryable: false}), concurrency: 4, priorityExtra: 0, backoffMs: []}), []);
  useEffect(() => () => scheduler.dispose(), [scheduler]);
  return (key: string): ArtworkSource => ({scheduler, key});
}

/** One whole window: the standard backdrop the window draws, and the shell's gutter, which a frame takes inside the shell. */
export function Window({children}: {children: ReactNode}) {
  return <div className="portico-backdrop" style={{height: '100%', overflowY: 'auto', '--shell-gutter': 'var(--page-gutter)'} as CSSProperties}>{children}</div>;
}
/** A page as the app shows it: inside the shell's own top bar (from 1024) or wordmark bar and tab bar (phone width),
 * drawn with the shell's stylesheet so the frame is judged where it lives. Between 600 and 1023 the shell's rail is not drawn here. */
/** The owner's server activity in the top bar, with two people watching (mock.html home). Only what the closed button reads. */
const activityView = {phase: 'ready', serverName: '', health: 'normal', streamCount: 2, taskCount: 0, streams: [], scans: [], tasks: []} as unknown as ActivityPanelView;
export function Page({children, current, home = false}: {children: ReactNode; current: 'home' | 'movies' | 'settings'; home?: boolean}) {
  const phone = usePhone(), wide = useWide(), tools = (name: 'i-search' | 'i-bookmark' | 'i-bell', label: FixtureName) => <a className={shell.icon} aria-label={f(label)}><Icon name={name} size={22} /></a>;
  // The shell's underline measures the list it is in, so it is mounted once that list exists.
  const destinations = useRef<HTMLUListElement>(null), [listed, setListed] = useState(false);
  useEffect(() => setListed(true), []);
  const avatar = <span className={shell.avatar}><ProfileAvatarFace initial={f('profileInitial')} /></span>, page = (id: typeof current) => current === id ? 'page' as const : undefined;
  return <div className={`portico-backdrop ${shell.shell}`} data-desktop={wide} data-navigation={wide ? 'topbar' : phone ? 'tabs' : undefined} style={{position: 'relative', height: '100%', overflowY: 'auto', boxSizing: 'border-box'}}>
    <div className={shell.navigation}>{wide ? <nav className={shell.topnav}>
      <a className={shell.brand}><img src={wordmark} alt="" /></a>
      <ul ref={destinations} className={shell.destinations}><li><a aria-current={page('home')}>{f('home')}</a></li><li><a aria-current={page('movies')}>{f('movies')}</a></li>{(['tvShows', 'anime', 'music', 'audiobooks', 'liveTvShort'] as const).map(name => <li key={name}><a>{f(name)}</a></li>)}{listed && <DesktopNavigationUnderline list={destinations} />}</ul>
      <div className={shell.tools}>{tools('i-search', 'search')}{tools('i-bookmark', 'saved')}{tools('i-bell', 'notifications')}<TopBarActivity viewerRole="owner" view={activityView} open={false} onOpenChange={none} />{avatar}</div>
    </nav> : phone ? <>
      {home && <header className={shell.phoneHeader}><a className={shell.brand}><img src={wordmark} alt="" /></a><div className={shell.phoneTools}>{avatar}</div></header>}
      <nav className={shell.tabbar}>{([['i-home', 'home', 'home'], ['i-search', 'search', ''], ['i-grid', 'libraries', 'movies'], ['i-tv', 'liveTvShort', ''], ['i-bookmark', 'saved', '']] as const).map(([icon, name, id]) =>
        <a key={name} aria-current={id && current === id ? 'page' : undefined}><Icon name={icon} size={22} /><span>{f(name)}</span></a>)}</nav>
    </> : null}</div>
    {children}
  </div>;
}

const posters = ['spirited-away', 'inside-out', 'alien', 'blade-runner', 'dune', 'barbie', 'back-to-the-future', 'the-dark-knight', 'avatar', 'aliens'].map(id => ({id, title: id.split('-').map(word => word[0].toUpperCase() + word.slice(1)).join(' ')}));
const wides = [{id: 'tlou', title: f('tlou'), art: 'bd-tlou.jpg'}, {id: 'inside-out', title: f('insideOut'), art: 'bd-insideout.jpg'}, {id: 'spirited', title: f('spirited'), art: 'bd-spirited.jpg'}];
export function PosterRow({title, fresh = false}: {title: string; /** The first few are new. */ fresh?: boolean}) {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Shelf heading={title} platform={phone ? 'phone-web' : 'desktop'} items={posters} getKey={item => item.id}
    renderItem={item => <PosterCard isNew={fresh && posters.indexOf(item) < 3} model={{title: item.title, caption: []}} artwork={art(`pool/movies/${item.id}.jpg`)} style={{width: 'var(--card-poster-width)'}} onNavigate={none} />} />;
}
function WideRow() {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Shelf heading={f('continueWatching')} platform={phone ? 'phone-web' : 'desktop'} items={wides} getKey={item => item.id}
    renderItem={item => <WideCard model={{title: item.title, caption: [{id: 'episode-label', text: f('wideEpisode'), role: 'identity'}], detail: [{id: 'remaining', text: f('wideLeft'), role: 'context'}], progress: .4}} artwork={art(item.art)} style={{width: 'var(--card-wide-width)'}} onNavigate={none} />} />;
}

const heroes = [
  {id: 'spirited', title: f('spirited'), reason: f('spiritedReason'), overview: f('tlouSynopsis'), facts: fact(f('spiritedFacts')), art: 'bd-spirited.jpg'},
  {id: 'tlou', title: f('tlou'), overview: f('tlouSynopsis'), facts: fact(f('tlouFacts')), art: 'bd-tlou.jpg'},
];
function BrowseHome() {
  const art = useReferenceArtwork(), [index, setIndex] = useState(0), phone = usePhone();
  return <Page current="home" home><BrowseFrame hero={<HomeHero candidates={heroes} activeIndex={index} onIndexChange={setIndex}
    renderArtwork={(entry, context) => <AmbientArt treatment={phone ? 'phone' : 'hero'} foreground={art(entry.art)} visible={context.visible} onReady={context.onReady} onError={context.onError} />}
    renderActions={() => <><Button variant="primary" phone={phone} icon="i-play">{f('play')}</Button><Button phone={phone} icon="i-plus">{f('myList')}</Button></>} />}>
    <WideRow /><PosterRow fresh title={f('newOnServer')} />
  </BrowseFrame></Page>;
}
const BrowseList = () => <Page current="movies"><BrowseFrame header={{title: f('movies'), back: {label: f('libraries'), onPress: none}, trailing: <Button size="small" icon="i-filter">{f('filters')}</Button>}}>
  <PosterRow title={f('newOnServer')} /><PosterRow title={f('moreLikeThis')} />
</BrowseFrame></Page>;
const BrowseLoading = () => <Window><BrowseFrame header={{title: f('movies')}} state={{kind: 'loading'}} /></Window>;

/** A show's episode list as the title page and the watch page draw it (phone-player 4, tablet-play 3): the kit's season
 * bar, then the kit's episode tiles as rows with their states. */
const episodes = [
  {name: 'episode1', number: 'episodeNumber1', status: 'episodeWatched', length: 'episodeLength1', watched: true, progress: undefined, playing: false, art: true},
  {name: 'episode2', number: 'episodeNumber2', status: 'episodePlaying', length: 'episodeLeft2', watched: false, progress: .29, playing: true, art: true},
  {name: 'episode3', number: 'episodeNumber3', status: 'episodeUpNext', length: 'episodeLength3', watched: false, progress: undefined, playing: false, art: false},
] as const;
function Episodes() {
  const art = useReferenceArtwork(), [hide, setHide] = useState(false);
  return <div style={{display: 'grid', gap: 'var(--space-sm)', margin: '0 var(--g)'}}>
    <SeasonBar label={f('episodesSeason')} count={f('episodesCount')} onSeasons={none} hideWatched={hide} hideWatchedLabel={f('hideWatched')} onHideWatched={setHide} />
    <EpisodeRows>{episodes.map(episode => <EpisodeTile key={episode.name} context="detail" artworkKind="still" artwork={episode.art ? art('bd-tlou.jpg') : undefined} playing={episode.playing} watched={episode.watched}
      model={{title: f(episode.name), caption: [{id: 'episode', text: f(episode.number), role: 'context'}], detail: [], progress: episode.progress}}
      playback={[{id: 'state', text: f(episode.status), role: 'identity'}, {id: 'length', text: f(episode.length), role: 'context'}]} description={f('tlouSynopsis')} onNavigate={none} />)}</EpisodeRows>
  </div>;
}
function Title({variant}: {variant: 'full' | 'missing' | 'loading'}) {
  const art = useReferenceArtwork(), [tab, setTab] = useState('more'), full = variant !== 'missing', phone = usePhone();
  return <Page current="movies"><TitleFrame back={{label: f('back'), onPress: none}} topAction={{icon: 'i-cast', label: f('playOn'), onPress: none}}
    art={<AmbientArt treatment={phone ? 'phone' : 'title'} foreground={full ? art('bd-insideout.jpg') : undefined} />}
    title={full ? f('insideOut') : f('longTitle')} metadata={<HeroFacts parts={full ? [...fact(f('insideOutFacts')), {id: 'content-rating', text: f('insideOutRating'), role: 'context'}] : fact(f('longFacts'))} platform={phone ? 'phone-web' : 'desktop'} />}
    primary={full ? {icon: 'i-play', label: f('resume'), progress: .09, remaining: f('resumeLeft'), startOver: {label: f('startOver'), onPress: none}, onPress: none} : {icon: 'i-play', label: f('play'), onPress: none}}
    actions={full ? [{icon: 'i-plus', label: f('myList'), labelled: true, onPress: none}, {icon: 'i-check', label: f('watched'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}] : []}
    tagline={full ? f('insideOutTagline') : undefined} synopsis={full ? f('insideOutSynopsis') : undefined}
    credits={full ? [{label: f('directedBy'), names: f('insideOutDirector')}, {label: f('starring'), names: f('insideOutCast')}] : undefined}
    tabs={{items: [{value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}], value: tab, onValueChange: setTab}}
    state={variant === 'loading' ? {kind: 'loading'} : undefined}>
    <PosterRow title={f('moreLikeInsideOut')} />
  </TitleFrame></Page>;
}

const value = (text: string) => <span className={frame.rowHelp} style={{display: 'inline-flex', alignItems: 'center', gap: 4}}>{text}<Icon name="i-right" size={18} /></span>;
function PlaybackSections() {
  const [autoplay, setAutoplay] = useState(true);
  return <>
    <SectionCard label={f('nextEpisode')}>
      <SettingsRow label={f('autoplay')}><Switch checked={autoplay} onCheckedChange={setAutoplay} aria-label={f('autoplay')} /></SettingsRow>
      <SettingsRow label={f('wait')} help={f('waitHelp')}>{value(f('waitValue'))}</SettingsRow>
    </SectionCard>
    <SectionCard label={f('skipping')}>
      <SettingsRow label={f('intros')}>{value(f('ask'))}</SettingsRow>
      <SettingsRow label={f('credits')}>{value(f('ask'))}</SettingsRow>
    </SectionCard>
  </>;
}
/** Rule 9 in one page: a section that failed, one that kept its last list and says it is old, one loading and one empty. */
function StateSections() {
  return <>
    <SectionCard label={f('sources')}><StateRow kind="error" icon="i-warn" title={f('sourcesFailed')} reason={f('sourcesReason')} action={{label: f('tryAgain'), onPress: none}} /></SectionCard>
    <SectionCard label={f('sources')} stale={{label: f('sourcesStale'), retryLabel: f('tryAgain'), onRetry: none}}>
      <SettingsRow label={f('source1')} help={f('source1Meta')}>{value('')}</SettingsRow>
      <SettingsRow label={f('source2')} help={f('source2Meta')}>{value('')}</SettingsRow>
    </SectionCard>
    <SectionCard label={f('guide')}><StateRow kind="loading" /></SectionCard>
    <SectionCard label={f('recordings')}><StateRow kind="empty" icon="i-rec" title={f('noRecordings')} reason={f('noRecordingsReason')} action={{label: f('openGuide'), onPress: none}} /></SectionCard>
  </>;
}
/** The Settings index is the shell's own `SettingsNav` (search, the account's pages, the server's pages, the current
 * page marked), passed as the frame's list: the frame places it and the page beside it (desktop-settings 1, phone-web 3). */
function SettingsList() {
  const [page, setPage] = useState<string | undefined>(), phone = usePhone(), wide = !phone, [query, setQuery] = useState('');
  const list = <SettingsNav layout={phone ? 'phone' : 'desktop'} accountName={shellSample.current.name} serverName={shellFixtures['next.dev.shell.serverName']} viewerRole="owner" account={shellSample.accountNav} server={shellSample.serverNav}
    selectedId={page ?? (wide ? 'playback' : '')} onNavigate={setPage} query={query} onQueryChange={setQuery} onSearchResult={none} />;
  return <Page current="settings"><SettingsListFrame bar={{title: f('settings'), action: {label: f('done'), onPress: none}}} list={list}
    page={page || wide ? <SettingsPageFrame title={f('playback')} back={{label: f('settings'), onPress: () => setPage(undefined)}} intro={f('playbackIntro')}><PlaybackSections /></SettingsPageFrame> : undefined} /></Page>;
}
const SettingsPage = () => <Window><SettingsPageFrame title={f('liveTv')} back={{label: f('settings'), onPress: none}} intro={f('playbackIntro')}><StateSections /></SettingsPageFrame></Window>;

const Sheet = () => <Window><BrowseFrame header={{title: f('liveTv')}} />
  <SheetFrame open onOpenChange={none} task="task" title={f('addSource')}>
    <SettingsRow label={f('playlist')} help={f('playlistMeta')}>{value('')}</SettingsRow>
    <SettingsRow label={f('findTuners')} help={f('findTunersMeta')}>{value('')}</SettingsRow>
  </SheetFrame></Window>;

function SignIn() {
  const [address, setAddress] = useState(f('addressValue'));
  // The sign-in page sits on the window's one backdrop, as every page does (auth 4).
  return <Window><SignInFrame back={{label: f('back'), onPress: none}} title={f('localServer')} lede={f('localLede')} error={f('signInError')}>
    <TextField label={f('address')} value={address} onChange={event => setAddress(event.currentTarget.value)} />
    <Button variant="primary" onClick={none}>{f('continue')}</Button>
  </SignInFrame></Window>;
}

/** A still frame standing in for the video surface (a plain picture: the player's surface is a video element, not kit artwork). */
const Still = () => <img src="http://100.120.50.24:8787/assets/bd-tlou.jpg" alt="" style={{width: '100%', height: '100%', objectFit: 'cover'}} />;
/** The players' own scrubber and read-outs (kit/media SeekLine, kit/player PlayerTimes) at a still position. */
function Scrubber({scrubbing = false, live = false, chapters = true}: {scrubbing?: boolean; live?: boolean; chapters?: boolean}) {
  // On touch the knob goes with the finger to the time being previewed (phone-player 1b); a pointer's preview is a hover and the knob stays (desktop-player 1).
  const upright = usePhone(), sideways = useLandscapePhone(), touch = upright || sideways, at = live ? .96 : scrubbing && touch ? .65 : .29;
  return <SeekLine variant="player" value={at} buffered={[{start: at, end: live ? 1 : at + .14}]}
    marks={chapters && !live ? [{id: 'intro', start: .02, end: .08, kind: 'intro'}, {id: 'c1', start: .31, kind: 'chapter'}, {id: 'c2', start: .62, kind: 'chapter'}, {id: 'credits', start: .95, end: 1, kind: 'credits'}] : []}
    hover={scrubbing ? .65 : null} overlay={scrubbing ? <PlayerScrubPreview at={.65} time={f('previewTime')}><Still /></PlayerScrubPreview> : undefined}
    input={{min: 0, max: 1, step: .001, value: at, onChange: none, 'aria-label': f('seek')}} />;
}
/** From 1024 the bar has room for volume, speed and picture in picture. */
export function useWide() {
  const [wide, setWide] = useState(() => window.matchMedia('(min-width: 1024px)').matches);
  useEffect(() => {const media = window.matchMedia('(min-width: 1024px)'), update = () => setWide(media.matches); media.addEventListener('change', update); return () => media.removeEventListener('change', update);}, []);
  return wide;
}
/** The watch page under an upright phone's picture (phone-player 4): the title page's own content, without its art. */
function WatchPage({paused = false}: {paused?: boolean}) {
  const [tab, setTab] = useState('episodes');
  return <div className={frame.frame} style={{display: 'flex', flexDirection: 'column', gap: 'var(--space-sm)', padding: 'var(--space-sm) 0 var(--frame-bottom-clear)'}}>
    <div style={{margin: '0 var(--g)', display: 'grid', gap: 'var(--space-tight)'}}><h1 className={frame.pageTitle}>{f('tlou')}</h1><p className={frame.rowLabel}>{f('videoLead')}  {f('videoSubtitle')}</p><HeroFacts parts={fact(f('tlouFacts'))} platform="phone-web" /></div>
    <div style={{margin: '0 var(--g)', display: 'flex', gap: 'var(--spec-hero-actions-gap-phone)'}}><Button variant="primary" phone icon={paused ? 'i-play' : 'i-pause'} style={{flex: 1}}>{f(paused ? 'play' : 'pause')}</Button>
      {/* The web has no downloads, so its row is Add and More. */}
      {([['i-plus', 'myList'], ['i-more', 'more']] as const).map(([icon, name]) => <Button key={name} phone iconOnly icon={icon} aria-label={f(name)} />)}</div>
    <Tabs phone items={[{value: 'episodes', label: f('episodes')}, {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]} value={tab} onValueChange={setTab} listClassName={frame.tabsInset} />
    <Episodes />
  </div>;
}
/** A phone on its side: short and wide. */
function useLandscapePhone() {
  const query = '(max-height: 500px) and (min-width: 600px)', [on, setOn] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {const media = window.matchMedia(query), update = () => setOn(media.matches); media.addEventListener('change', update); return () => media.removeEventListener('change', update);}, []);
  return on;
}
/** The video player filled with the kit's player parts, with what each mock-up puts in each place for its form
 * (desktop-player 1, phone-player 2c on its side, phone-player 4 upright). Nothing moves between the states. */
function Player({state = 'playing', panel, panelCovers, card}: {state?: 'playing' | 'paused' | 'buffering' | 'scrubbing' | 'live' | 'missing'; /** One of the player's own panels, open, and what it lies over. */ panel?: ReactNode; panelCovers?: 'bar' | 'all'; /** Up next, over the credits. */ card?: ReactNode}) {
  const [volume, setVolume] = useState(.7), upright = usePhone(), sideways = useLandscapePhone(), desktop = useWide() && !sideways, live = state === 'live', missing = state === 'missing';
  const tool = (icon: AudioControl['icon'], name: FixtureName, disabled = false) => <PlayerControl onPicture icon={icon} label={f(name)} disabled={disabled} onPress={none} />;
  const subtitles = <PlayerPill icon="i-cc" name={f('subtitles')} value={f('english')} label={f('subtitles')} disabled={missing} onPress={none} />;
  const audio = <PlayerPill icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} disabled={missing} onPress={none} />;
  const quality = <PlayerPill icon={desktop ? undefined : 'i-quality'} qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} />;
  return <div data-mode={upright ? 'portrait' : undefined} style={{position: 'relative', height: '100%'}}><PlayerChromeFrame chromeVisible scrubbing={state === 'scrubbing'} panel={panel} panelCovers={panelCovers} card={card} picture={<Still />} close={tool('i-down', 'close')} title={<PlayerTitle title={f('tlou')} lead={f('videoLead')} subtitle={f('videoSubtitle')} />}
    tools={desktop ? <>{tool('i-cast', 'playOn')}{tool('i-more', 'more')}{tool('i-x', 'dismiss')}</> : <>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{upright && tool('i-cc', 'subtitles', missing)}{tool('i-settings', 'playerSettings')}</>}
    scrubber={<Scrubber scrubbing={state === 'scrubbing'} live={live} chapters={!missing} />}
    times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration'), endsAt: live ? undefined : f('endsAt'), live: live ? f('live') : undefined}}
    transport={<><PlayerControl icon="i-prev" label={f('previous')} disabled={live || missing} onPress={none} /><PlayerControl icon={desktop ? 'i-back' : 'i-rew'} skip={10} label={f('rewind')} onPress={none} />
      <PlayerControl primary icon={state === 'paused' ? 'i-play' : 'i-pause'} busy={state === 'buffering'} label={f(state === 'paused' ? 'play' : 'pause')} onPress={none} />
      <PlayerControl icon={desktop ? 'i-fwd' : 'i-ffw'} skip={30} label={f('forward')} disabled={live} onPress={none} /><PlayerControl icon="i-next" label={f('next')} disabled={live || missing} onPress={none} /></>}
    leading={<Button size="small" icon="i-stack" disabled={live}>{f('episodesButton')}</Button>}
    trailing={desktop ? <><PlayerVolume value={volume} muted={false} label={f('volume')} muteLabel={f('volume')} onChange={setVolume} onMute={none} /><PlayerControl icon="i-cc" label={f('subtitles')} disabled={missing} active={!!panel && panelCovers === undefined} onPress={none} />{quality}
        <PlayerPill value={f('speedShort')} label={f('speed')} disabled={live} onPress={none} /><PlayerControl icon="i-pip" label={f('pictureInPicture')} onPress={none} /><PlayerControl icon="i-full" label={f('fullScreen')} onPress={none} /></>
      : upright ? <>{quality}<PlayerControl icon="i-full" label={f('fullScreen')} onPress={none} /></> : <>{subtitles}{audio}{quality}</>}
    prompts={state === 'playing' ? <PlayerPrompt label={f('skipIntro')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}} /> : undefined}
    body={<WatchPage paused={state === 'paused'} />} /></div>;
}

/** The player's own panels (kit/player), each over the player as the player screen draws it, with a fixed sample of
 * what a playing title offers. The form follows the window: one panel of columns on the desktop (desktop-player 3), a
 * bottom sheet on an upright phone and a side sheet on a phone on its side (phone-player 3). */
const track = (id: string, primary: FixtureName, secondary: FixtureName | undefined, extra: Partial<TrackChoice> = {}): TrackChoice => ({id, primary: f(primary), secondary: secondary && f(secondary), fullName: f(primary), selected: false, ...extra});
const choice = (id: string, label: FixtureName, detail: FixtureName | undefined, selected = false): PlayerRadioChoice => ({id, label: f(label), detail: detail && f(detail), selected, enabled: true});
const optionsModel: PlayerOptionsModel = {
  fence: {} as PlayerOptionsModel['fence'], automatic: true, qualityLabel: f('qualityValue'), qualityAccessibleLabel: f('qualityValue'), audioLabel: f('english'), subtitleLabel: f('english'),
  subtitles: [track('off', 'optOff', undefined), track('en', 'english', 'optSrt', {selected: true}), track('en-sdh', 'optEnglishSdh', 'optSrt'), track('es', 'optSpanish', 'optSrt'), track('fr', 'optFrench', 'optSrtBeside')],
  subtitleAdditions: [track('de', 'optGerman', 'optNotAdded', {addable: true})],
  audio: [track('en', 'english', 'optAac51', {selected: true}), track('es', 'optSpanish', 'optAacStereo'), track('commentary', 'optCommentary', 'optAacStereo', {unavailable: true})],
  audioNotes: [f('optAudioNote')],
  quality: [choice('original', 'optOriginal', 'optOriginalDetail'), choice('auto', 'optAutomatic', 'optAutomaticDetail', true), choice('720', 'opt720', 'opt720Detail'), choice('480', 'opt480', 'opt480Detail')],
  qualityNotes: [f('optQualityNote')],
  speed: [['0.5', 'optSpeed05'], ['0.75', 'optSpeed075'], ['1', 'optNormal'], ['1.25', 'optSpeed125'], ['1.5', 'optSpeed15'], ['2', 'optSpeed2']].map(([id, label]) => ({id, label: f(label as FixtureName), selected: id === '1', enabled: true})),
  subtitleTiming: {offsetSeconds: 0, shift: async () => {}}, subtitleStyle: {size: 'medium', background: 'translucent', set: none}, sleepTimer: {minutes: 0, set: none}, subtitleForced: {only: false, set: none},
  isCurrent: () => true, selectAudio: async () => {}, selectSubtitle: async () => {}, selectQuality: async () => {}, selectSpeed: none,
};
function PlayerPanelCase({panel}: {panel: PlayerPanel}) {
  const upright = usePhone(), sideways = useLandscapePhone(), anchor = useRef<HTMLButtonElement>(null);
  const context: PlayerPanelContext = {panel, snapshot: {queueView: {remainingCount: 3}} as unknown as PlayerPanelContext['snapshot'], layout: upright || sideways ? 'phone' : 'desktop', landscape: sideways,
    phoneTop: 220, available: ['options', 'subtitles', 'audio', 'quality', 'speed', 'episodes', 'queue'], close: none, show: none, returnFocusRef: anchor};
  return <Player state="paused" panelCovers={sideways ? 'all' : undefined} panel={<PlayerOptions model={optionsModel} context={context} i18n={defaultI18n} reportError={none} openSettings={none} />} />;
}
const episodeList: PlayerEpisodeList = {seasonId: 's1', seasons: [{id: 's1', number: 1, title: ''}, {id: 's2', number: 2, title: ''}],
  rows: (['episode1', 'episode2', 'episode3'] as const).map((name, index) => ({itemId: name, title: f(name), number: index + 1, stillUrl: 'bd-tlou.jpg', overview: f('tlouSynopsis'),
    durationSeconds: [4860, 3180, 4560][index], progressSeconds: index === 1 ? 842 : index === 0 ? 4860 : undefined, watched: index === 0, playing: index === 1, next: index === 2}))};
function PlayerEpisodesCase() {
  const art = useReferenceArtwork(), upright = usePhone(), sideways = useLandscapePhone(), anchor = useRef<HTMLButtonElement>(null);
  const context: PlayerPanelContext = {panel: 'episodes', snapshot: {} as PlayerPanelContext['snapshot'], layout: upright || sideways ? 'phone' : 'desktop', landscape: sideways, phoneTop: 220, close: none, show: none, returnFocusRef: anchor};
  return <Player state="paused" panelCovers={sideways ? 'all' : upright ? undefined : 'bar'} panel={<PlayerEpisodes episodes={episodeList} context={context} artwork={art} onPlay={none} onSeason={none} reportError={none} />} />;
}
function PlayerUpNextCase() {
  const upright = usePhone(), sideways = useLandscapePhone(), wide = useWide();
  return <Player state="playing" card={<PlayerUpNext layout={upright || sideways ? 'phone' : wide ? 'desktop' : 'tablet'} heading={f('upNextHeading')} still={<Still />} title={f('episode3')} facts={[f('upNextCode'), f('upNextDuration')]}
    when={f('upNextWhen')} progress={.4} actions={[{id: 'play', label: f('upNextPlayNow'), primary: true, glyph: 'play', onPress: none}, {id: 'credits', label: f('upNextWatchCredits'), onPress: none}]} />} />;
}

/** The Live TV player's own chrome (kit/player `PlayerLive`; desktop-live "live-player", phone-player 1): the channel
 * and programme lockup, Record, the programme-long bar with the live edge and Go to live, channel down and up, and
 * Guide with its short guide. It is drawn from one port of channel facts, here a fixed sample. */
function PlayerLiveCase({guide = false}: {guide?: boolean}) {
  const [volume, setVolume] = useState(.7), wide = useWide(), sideways = useLandscapePhone(), upright = usePhone(), pointer = wide && !sideways;
  const chrome = <PlayerLive port={livePlayerPort} playing initialMiniGuideOpen={guide} onCollapse={none} onStop={none} onPlayPause={none} onSkip={none} onSeek={none} onMore={none} reportError={none}
    right={upright ? <PlayerPill qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} /> : <>{pointer && <PlayerVolume value={volume} muted={false} label={f('volume')} muteLabel={f('volume')} onChange={setVolume} onMute={none} />}<PlayerControl icon="i-cc" label={f('subtitles')} onPress={none} />
      <PlayerPill qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} /><PlayerControl icon="i-pip" label={f('pictureInPicture')} onPress={none} /><PlayerControl icon="i-full" label={f('fullScreen')} onPress={none} /></>} />;
  const picture = <div style={{position: 'absolute', inset: 0, display: 'grid'}}><Still /></div>;
  // Upright: the picture is a 16:9 strip and the channel's page is under it, as the native apps draw it.
  if (upright) return <div style={{height: '100%', overflowY: 'auto', background: 'var(--color-ink)'}}>
    <div style={{position: 'relative', aspectRatio: '16 / 9', overflow: 'hidden'}}>{picture}{chrome}</div>
    <div className={frame.frame} style={{display: 'grid', gap: 'var(--space-sm)', padding: 'var(--space-sm) var(--g)'}}>
      <p className={frame.rowLabel} style={{display: 'flex', alignItems: 'center', gap: 'var(--space-control-gap)'}}><ChannelMark mark={{sourceId: 'synthetic', id: 'sse', text: f('liveMark'), seed: 'synthetic:sse'}} size={40} /><b>{f('liveChannelName')}  {f('liveNumber')}</b><b style={{color: 'var(--color-record)'}}>{f('liveNow')}</b></p>
      <h1 className={frame.pageTitle}>{f('liveTitle')}</h1><p className={frame.intro}>{f('liveThen')}</p>
      <div style={{display: 'flex', gap: 'var(--spec-hero-actions-gap-phone)'}}><Button phone icon="i-list">{f('liveGuide')}</Button><Button phone icon="i-rec">{f('liveRecord')}</Button></div>
    </div>
  </div>;
  return <div style={{position: 'relative', height: '100%', overflow: 'hidden', background: 'var(--color-ink)'}}>{picture}{chrome}</div>;
}

function Audio({variant}: {variant: 'music' | 'missing' | 'book' | 'loading'}) {
  const art = useReferenceArtwork();
  const control = (icon: AudioControl['icon'], name: FixtureName, disabled = false, extra: Partial<AudioControl> = {}): AudioControl => ({icon, label: f(name), onPress: none, disabled, ...extra});
  const option = (name: FixtureName, icon?: AudioOption['icon'], shown?: FixtureName, disabled = false): AudioOption => ({label: f(name), icon, value: shown && f(shown), onPress: none, disabled});
  const book = variant === 'book', missing = variant === 'missing';
  const music = [control('i-shuffle', 'shuffle'), control('i-prev', 'previous'), control('i-pause', 'pause'), control('i-next', 'next', missing), control('i-repeat', 'repeat', false, {active: true})] as const;
  const reading = [control('i-prev', 'previous'), control('i-rew', 'rewind', false, {skip: 10}), control('i-pause', 'pause'), control('i-ffw', 'forward', false, {skip: 30}), control('i-next', 'next')] as const;
  // The queue is the screen's content: here the kit's song rows under their group labels (desktop-listen "now").
  const song = (key: string, title: string, duration: FixtureName, playing = false) => <SongRow key={key} context="search" onPlay={none}
    row={{id: key, playing, artwork: <ArtBox kind="square" icon="i-music" source={art('pool/albums/after-hours.jpg')} />, duration: {id: 'duration', text: f(duration), role: 'context'}, text: {id: key, title, caption: [{id: 'artist', text: f('queueArtist'), role: 'context'}], detail: []}}} />;
  // An audiobook's side column is its chapters, the one being read marked.
  const chapters = <div><h3 className={frame.rowHelp}>{f('chapters')}</h3>{(['chapterRow7', 'chapterRow8', 'chapterRow9', 'chapterRow10'] as const).map((name, index) => <SongRow key={name} onPlay={none}
    row={{id: name, playing: index === 1, number: index + 7, duration: {id: 'duration', text: f(`queueDuration${index + 1}` as FixtureName), role: 'context'}, text: {id: name, title: f(name), caption: [], detail: []}}} />)}</div>;
  const queue = <div><h3 className={frame.rowHelp}>{f('queueNow')}</h3>{song('now', f('songTitle'), 'queueDurationNow', true)}<h3 className={frame.rowHelp}>{f('queueNextFrom')}</h3>
    {(['queue1', 'queue2', 'queue3', 'queue4', 'queue5', 'queue6'] as const).map((name, index) => song(name, f(name), `queueDuration${index + 1}` as FixtureName))}</div>;
  return <Window><AudioPlayerFrame loading={variant === 'loading'}
    close={control('i-down', 'close')} more={control('i-more', 'more')} context={book ? f('playingFromListening') : missing ? f('playingFromField') : f('playingFrom')} source={book ? f('playingSourceListening') : missing ? f('playingSourceField') : f('playingSource')}
    favourite={{icon: 'i-heart', label: f('favourite'), onPress: none}}
    artwork={missing ? undefined : art(book ? 'pool/books/the-hobbit.jpg' : 'pool/albums/after-hours.jpg')} icon={book ? 'i-book' : 'i-music'}
    title={book ? f('hobbit') : missing ? f('longSong') : f('songTitle')} byline={book ? f('tolkien') : missing ? f('choir') : f('songArtist')} chapter={book ? {label: f('chapter'), of: f('chapterOf')} : undefined}
    scrubber={<><SeekLine variant="player" value={.34} input={{min: 0, max: 1, step: .001, value: .34, onChange: none, 'aria-label': f('seek')}} /><PlayerTimes position={f(book ? 'bookElapsed' : 'audioElapsed')} middle={book ? f('timeLeft') : undefined} remaining={f(book ? 'bookRemaining' : 'audioRemaining')} /></>} transport={book ? reading : music} next={book || missing ? undefined : {label: f('nextLabel'), title: f('nextTitle')}}
    options={book ? [option('speed', undefined, 'speedValue'), {...option('sleep', undefined, 'sleepValue'), active: true}, option('chapters', undefined, 'chaptersValue'), option('thisBrowser', 'i-cast')]
      : [option('lyrics', 'i-lyrics', undefined, missing), {...option('thisBrowser', 'i-cast'), inline: true}, option('upNext', 'i-queue')]}
    upNext={missing ? undefined : book ? chapters : queue} /></Window>;
}

const State = () => <Page current="home"><PageStateFrame icon="i-srvoff" title={f('cantReach')} body={f('cantReachBody')} retry={{progress: .6, label: f('retryLabel'), detail: f('retryDetail')}}
  primaryAction={{id: 'retry', label: f('tryAgain'), icon: 'i-retry', onPress: none}} secondaryAction={{id: 'switch', label: f('switchServer'), icon: 'i-swap', onPress: none}} /></Page>;

/** The type roles, the row kinds in one card, the in-section states and the artwork boxes with and without a picture. */
function Elements() {
  const art = useReferenceArtwork();
  const roles = [['pageTitle', 'rolePageTitle'], ['rowHeading', 'roleSectionHeading'], ['groupLabel', 'roleGroupLabel'], ['barTitle', 'roleBarTitle'], ['rowLabel', 'roleRowLabel'], ['rowHelp', 'roleRowHelp'], ['intro', 'roleBody']] as const;
  const boxes = [['square', 'i-music', 'artMusic', undefined], ['square', 'i-book', 'artBook', undefined], ['poster', 'i-film', 'artPoster', undefined], ['wide', 'i-tv', 'artWide', undefined],
    ['square', 'i-music', 'withArtwork', 'pool/albums/thriller.jpg'], ['poster', 'i-film', 'withArtwork', 'pool/movies/dune.jpg']] as const;
  return <Window><div className={frame.frame} style={{display: 'flex', flexDirection: 'column', gap: 'var(--frame-section-gap)', padding: 'var(--frame-section-gap) 0'}}>
    <div style={{display: 'grid', gap: 'var(--space-sm)', margin: '0 var(--g)'}}>{roles.map(([role, name]) => <p key={role} className={frame[role]}>{f(name)}</p>)}</div>
    <PlaybackSections />
    <StateSections />
    <Section title={f('albums')}><div style={{display: 'flex', flexWrap: 'wrap', gap: 'var(--space-sm)', margin: '0 var(--g)'}}>
      {boxes.map(([kind, icon, name, source], index) => <div key={index} style={{width: kind === 'wide' ? 'calc(var(--card-poster-width) * 1.6)' : kind === 'square' ? 'var(--card-square-width)' : 'var(--card-poster-width)'}}>
        <ArtBox kind={kind} icon={icon} source={source ? art(source) : undefined} /><p className={frame.rowHelp}>{f(name)}</p></div>)}
    </div></Section>
    <Section title={f('newOnServer')}><div style={{display: 'flex', flexWrap: 'wrap', gap: 'var(--space-sm)', margin: '0 var(--g)'}}>
      <PosterCard model={{title: f('spirited'), caption: fact('2001')}} artwork={art('pool/movies/spirited-away.jpg')} style={{width: 'var(--card-poster-width)'}} onNavigate={none} />
      <PosterCard model={{title: f('longTitle'), caption: fact('1974')}} style={{width: 'var(--card-poster-width)'}} onNavigate={none} />
      <SquareCard model={{title: f('songTitle'), caption: fact(f('songArtist'))}} style={{width: 'var(--card-square-width)'}} onNavigate={none} />
    </div></Section>
  </div></Window>;
}

/** Decision 4: each frame empty, as the frames page draws it. What the frame owns is drawn by the frame, with stand-in
 * words; what the screen supplies is an outlined box that says so, and the one flexible region is marked. */
function Region({name, flexible = false, height}: {name: FixtureName; flexible?: boolean; height?: string}) {
  const color = flexible ? 'var(--color-amber)' : 'var(--color-accent-strong)';
  return <div style={{boxSizing: 'border-box', height: height ?? '100%', minHeight: height ? undefined : 'var(--frame-row-two-line)', overflow: 'hidden', border: `2px dashed ${color}`, borderRadius: 'var(--radius-control)', padding: 'var(--space-xs)', display: 'grid', alignContent: 'center'}}>
    <b className={frame.rowLabel} style={{color}}>{f(name)}</b><span className={frame.rowHelp}>{f(flexible ? 'regionFlexible' : 'regionScreen')}</span></div>;
}
const Block = ({name, flexible, rows = 3}: {name: FixtureName; flexible?: boolean; rows?: number}) => <div style={{margin: '0 var(--g)'}}><Region name={name} flexible={flexible} height={`calc(var(--frame-row-two-line) * ${rows})`} /></div>;
const own = {back: {label: f('ownBack'), onPress: none}, action: {label: f('ownAction'), onPress: none}};
const RegionsBrowse = () => <Window><BrowseFrame hero={<div style={{height: 'var(--frame-hero-home-height)'}}><Region name="regionHero" /></div>}><Block name="regionRows" flexible /><Block name="regionRows" flexible /></BrowseFrame></Window>;
const RegionsTitle = () => <Window><TitleFrame back={own.back} topAction={{icon: 'i-cast', label: f('ownAction'), onPress: none}} art={<Region name="regionArt" />} title={f('ownTitle')}
  metadata={<span className={frame.rowHelp}>{f('ownMeta')}</span>} primary={{icon: 'i-play', label: f('ownPrimary'), onPress: none}}
  actions={[{icon: 'i-plus', label: f('ownAction'), onPress: none}, {icon: 'i-more', label: f('more'), onPress: none}]} synopsis={f('ownSynopsis')}
  tabs={{items: [{value: 'one', label: f('ownTab1')}, {value: 'two', label: f('ownTab2')}], value: 'one', onValueChange: none}}><Block name="regionPanel" flexible /></TitleFrame></Window>;
function RegionsSettings() {
  const phone = usePhone();
  const page = <SettingsPageFrame title={f('ownTitle')} back={own.back} action={own.action} intro={f('ownIntro')}><Block name="regionSections" flexible /><Block name="regionSections" flexible /></SettingsPageFrame>;
  return <Window><SettingsListFrame bar={{title: f('ownBarTitle'), action: own.action}} list={<Block name="regionList" flexible rows={5} />} page={phone ? undefined : page} /></Window>;
}
const RegionsSheet = () => <Window><SheetFrame open onOpenChange={none} task="task" title={f('ownBarTitle')} foot={<Region name="regionFooter" />}><Region name="regionSheetRows" flexible height="calc(var(--frame-row-two-line) * 3)" /></SheetFrame></Window>;
const RegionsSignIn = () => <SignInFrame back={own.back} title={f('ownTitle')} lede={f('ownLede')}><Region name="regionForm" flexible height="calc(var(--frame-row-two-line) * 4)" /></SignInFrame>;
const RegionsPlayer = () => <PlayerChromeFrame chromeVisible picture={<Region name="regionPicture" />} close={<Region name="regionClose" height="var(--target-touch)" />} title={<Region name="regionPlayerTitle" height="var(--target-touch)" />}
  tools={<Region name="regionTools" height="var(--target-touch)" />} scrubber={<Region name="regionScrubber" height="var(--space-md)" />} times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration')}}
  transport={<Region name="regionTransport" height="var(--target-touch)" />} leading={<Region name="regionLeading" height="var(--target-touch)" />} trailing={<Region name="regionTrailing" height="var(--target-touch)" />}
  prompts={<Region name="regionPrompts" height="var(--target-touch)" />} body={<Region name="regionWatchPage" flexible height="calc(var(--frame-row-two-line) * 4)" />} />;
function RegionsAudio() {
  const slot = (icon: AudioControl['icon']): AudioControl => ({icon, label: f('ownAction') + icon, onPress: none});
  return <Window><AudioPlayerFrame close={slot('i-down')} more={slot('i-more')} context={f('ownContext')} icon="i-music" title={f('ownTitle')} byline={f('ownByline')} scrubber={<Region name="regionScrubber" height="var(--frame-audio-scrubber)" />}
    transport={[slot('i-shuffle'), slot('i-prev'), slot('i-pause'), slot('i-next'), slot('i-repeat')]} next={{label: f('ownNext'), title: f('ownNext')}}
    options={[{label: f('ownOption'), icon: 'i-lyrics', onPress: none}, {label: f('ownOption') + ' ', icon: 'i-cast', onPress: none}, {label: f('ownOption') + '  ', icon: 'i-queue', onPress: none}]} upNext={<Region name="regionUpNext" flexible />} /></Window>;
}
const RegionsState = () => <Window><PageStateFrame icon="i-info" title={f('ownStateTitle')} body={f('ownStateBody')} primaryAction={{id: 'primary', label: f('ownPrimary'), onPress: none}} secondaryAction={{id: 'secondary', label: f('ownSecondary'), onPress: none}} /></Window>;

const section = (id: string, label: string, render: ComponentType, height = 900): GalleryCase => ({id, labelKey: `next.platform.frame.${label}` as MessageId, width: 1440, height, render});
export default defineGalleryFamily({id: 'frames', labelKey: 'next.platform.frame.family', cases: [
  section('backdrop', 'backdrop', () => <Window><div /></Window>),
  section('browse-home', 'browseHome', BrowseHome),
  section('browse-list', 'browseList', BrowseList),
  section('browse-loading', 'browseLoading', BrowseLoading),
  section('title', 'title', () => <Title variant="full" />),
  section('title-missing', 'titleMissing', () => <Title variant="missing" />),
  section('title-loading', 'titleLoading', () => <Title variant="loading" />),
  section('settings-list', 'settingsList', SettingsList),
  section('settings-page', 'settingsPage', SettingsPage),
  section('sheet', 'sheet', Sheet),
  section('sign-in', 'signIn', SignIn),
  section('player', 'player', Player),
  section('player-options', 'playerOptions', () => <PlayerPanelCase panel="options" />), section('player-subtitles', 'playerSubtitles', () => <PlayerPanelCase panel="subtitles" />), section('player-audio', 'playerAudio', () => <PlayerPanelCase panel="audio" />),
  section('player-quality', 'playerQuality', () => <PlayerPanelCase panel="quality" />), section('player-speed', 'playerSpeed', () => <PlayerPanelCase panel="speed" />),
  section('player-episodes', 'playerEpisodes', PlayerEpisodesCase), section('player-up-next', 'playerUpNext', PlayerUpNextCase),
  section('player-live-controls', 'playerLiveControls', PlayerLiveCase), section('player-live-guide', 'playerLiveGuide', () => <PlayerLiveCase guide />), {...section('player-landscape', 'player', Player), width: 844, height: 390},
  ...(['paused', 'buffering', 'scrubbing', 'live', 'missing'] as const).map(state => section(`player-${state}`, 'player', () => <Player state={state} />)),
  section('audio', 'audio', () => <Audio variant="music" />),
  section('audio-missing', 'audioMissing', () => <Audio variant="missing" />),
  section('audio-book', 'audioBook', () => <Audio variant="book" />),
  section('audio-loading', 'audioLoading', () => <Audio variant="loading" />),
  section('state', 'state', State),
  section('elements', 'elements', Elements, 1800),
  section('regions-browse', 'regionsBrowse', RegionsBrowse), section('regions-title', 'regionsTitle', RegionsTitle), section('regions-settings', 'regionsSettings', RegionsSettings), section('regions-sheet', 'regionsSheet', RegionsSheet),
  section('regions-sign-in', 'regionsSignIn', RegionsSignIn), section('regions-player', 'regionsPlayer', RegionsPlayer), section('regions-audio', 'regionsAudio', RegionsAudio), section('regions-state', 'regionsState', RegionsState),
]});
