import {useEffect, useMemo, useState, type ComponentType, type CSSProperties, type ReactNode} from 'react';
import {formatMessage, type MessageId} from '@i18n';
import {ArtworkScheduler} from '@core/artwork';
import type {FactPart} from '@portico/client-core/kit-model';
import {framesFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures.ts';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {AmbientArt, type ArtworkImage, type ArtworkSource} from '../../kit/artwork';
import {PosterCard, SquareCard, WideCard} from '../../kit/cards';
import {Button, Switch, Tabs, TextField} from '../../kit/controls';
import {ArtBox, AudioPlayerFrame, BrowseFrame, HomeHero, PageStateFrame, PlayerChromeFrame, Section, SectionCard, SettingsListFrame, SettingsPageFrame, SheetFrame, SignInFrame, StateRow, TitleFrame,
  type AudioControl, type AudioOption} from '../../kit/frames';
import {Icon} from '../../kit/Icon';
import {HeroFacts, Shelf} from '../../kit/layout';
import {SeekLine} from '../../kit/media';
import {PlayerControl, PlayerPill, PlayerPrompt, PlayerScrubPreview, PlayerTimes, PlayerTitle, PlayerVolume} from '../../kit/player';
import {usePhone} from '../../kit/overlays/shared';
import {SettingsRow} from '../../kit/shell';
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
function useReferenceArtwork() {
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
function Window({children}: {children: ReactNode}) {
  return <div className="portico-backdrop" style={{height: '100%', overflowY: 'auto', '--shell-gutter': 'var(--page-gutter)'} as CSSProperties}>{children}</div>;
}
/** A page as the app shows it: inside the shell's own top bar (from 1024) or wordmark bar and tab bar (phone width),
 * drawn with the shell's stylesheet so the frame is judged where it lives. Between 600 and 1023 the shell's rail is not drawn here. */
function Page({children, current, home = false}: {children: ReactNode; current: 'home' | 'movies' | 'settings'; home?: boolean}) {
  const phone = usePhone(), wide = useWide(), tools = (name: 'i-search' | 'i-bookmark' | 'i-bell', label: FixtureName) => <a className={shell.icon} aria-label={f(label)}><Icon name={name} size={22} /></a>;
  const avatar = <span className={shell.avatar}><ProfileAvatarFace initial={f('profileInitial')} /></span>, page = (id: typeof current) => current === id ? 'page' as const : undefined;
  return <div className={`portico-backdrop ${shell.shell}`} data-desktop={wide} data-navigation={wide ? 'topbar' : phone ? 'tabs' : undefined} style={{position: 'relative', height: '100%', overflowY: 'auto', boxSizing: 'border-box'}}>
    <div className={shell.navigation}>{wide ? <nav className={shell.topnav}>
      <a className={shell.brand}><img src={wordmark} alt="" /></a>
      <ul className={shell.destinations}><li><a aria-current={page('home')}>{f('home')}</a></li><li><a aria-current={page('movies')}>{f('movies')}</a></li>{(['tvShows', 'anime', 'music', 'audiobooks', 'liveTvShort'] as const).map(name => <li key={name}><a>{f(name)}</a></li>)}</ul>
      <div className={shell.tools}>{tools('i-search', 'search')}{tools('i-bookmark', 'saved')}{tools('i-bell', 'notifications')}{avatar}</div>
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
function PosterRow({title}: {title: string}) {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Shelf heading={title} platform={phone ? 'phone-web' : 'desktop'} items={posters} getKey={item => item.id}
    renderItem={item => <PosterCard model={{title: item.title, caption: []}} artwork={art(`pool/movies/${item.id}.jpg`)} style={{width: 'var(--card-poster-width)'}} onNavigate={none} />} />;
}
function WideRow() {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Shelf heading={f('continueWatching')} platform={phone ? 'phone-web' : 'desktop'} items={wides} getKey={item => item.id}
    renderItem={item => <WideCard model={{title: item.title, caption: [], progress: .4}} artwork={art(item.art)} style={{width: 'var(--card-wide-width)'}} onNavigate={none} />} />;
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
    <WideRow /><PosterRow title={f('newOnServer')} />
  </BrowseFrame></Page>;
}
const BrowseList = () => <Page current="movies"><BrowseFrame header={{title: f('movies'), back: {label: f('libraries'), onPress: none}, trailing: <Button size="small" icon="i-filter">{f('filters')}</Button>}}>
  <PosterRow title={f('newOnServer')} /><PosterRow title={f('moreLikeThis')} />
</BrowseFrame></Page>;
const BrowseLoading = () => <Window><BrowseFrame header={{title: f('movies')}} state={{kind: 'loading'}} /></Window>;

function Episodes() {
  const art = useReferenceArtwork();
  return <Section title={f('episodes')}><div style={{display: 'grid', gap: 'var(--space-sm)', margin: '0 var(--g)'}}>
    {(['episode1', 'episode2', 'episode3'] as const).map(name => <div key={name} style={{display: 'flex', alignItems: 'center', gap: 'var(--space-sm)'}}>
      <div style={{width: 'calc(var(--card-poster-width) * 1.15)', flex: 'none'}}><ArtBox kind="wide" icon="i-tv" source={name === 'episode3' ? undefined : art('bd-tlou.jpg')} /></div>
      <div><b className={`${frame.rowLabel} ${frame.clamp2}`}>{f(name)}</b><p className={frame.rowHelp}>{f(`${name}Meta`)}</p></div>
    </div>)}
  </div></Section>;
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
function SettingsList() {
  const [page, setPage] = useState<string>(), wide = !usePhone();
  const list = <SectionCard label={f('account')}>
    {(['playback', 'audioSubtitles', 'quality', 'music', 'appearance', 'liveTv'] as const).map(name => <div key={name} onClick={() => setPage(name)}><SettingsRow label={f(name)}>{value('')}</SettingsRow></div>)}
  </SectionCard>;
  return <Page current="settings"><SettingsListFrame bar={{title: f('settings'), action: {label: f('done'), onPress: none}}} list={list}
    page={page || wide ? <SettingsPageFrame title={f('playback')} back={{label: f('settings'), onPress: () => setPage(undefined)}} intro={f('playbackIntro')}><PlaybackSections /></SettingsPageFrame> : undefined} /></Page>;
}
const SettingsPage = () => <Window><SettingsPageFrame title={f('liveTv')} back={{label: f('settings'), onPress: none}} intro={f('playbackIntro')}><StateSections /></SettingsPageFrame></Window>;

const Sheet = () => <Window><BrowseFrame header={{title: f('liveTv')}} />
  <SheetFrame open onOpenChange={none} title={f('addSource')} footer={<Button onClick={none}>{f('cancel')}</Button>}>
    <SettingsRow label={f('playlist')} help={f('playlistMeta')}>{value('')}</SettingsRow>
    <SettingsRow label={f('findTuners')} help={f('findTunersMeta')}>{value('')}</SettingsRow>
  </SheetFrame></Window>;

function SignIn() {
  const [address, setAddress] = useState(f('addressValue'));
  return <SignInFrame back={{label: f('back'), onPress: none}} title={f('localServer')} lede={f('localLede')} error={f('signInError')}>
    <TextField label={f('address')} value={address} onChange={event => setAddress(event.currentTarget.value)} />
    <Button variant="primary" onClick={none}>{f('continue')}</Button>
  </SignInFrame>;
}

/** A still frame standing in for the video surface (a plain picture: the player's surface is a video element, not kit artwork). */
const Still = () => <img src="http://100.120.50.24:8787/assets/bd-tlou.jpg" alt="" style={{objectFit: 'cover'}} />;
/** The players' own scrubber and read-outs (kit/media SeekLine, kit/player PlayerTimes) at a still position. */
function Scrubber({scrubbing = false, live = false, chapters = true}: {scrubbing?: boolean; live?: boolean; chapters?: boolean}) {
  return <SeekLine variant="player" value={live ? .96 : .2} buffered={[{start: .2, end: live ? 1 : .34}]}
    marks={chapters && !live ? [{id: 'intro', start: .02, end: .08, kind: 'intro'}, {id: 'c1', start: .31, kind: 'chapter'}, {id: 'c2', start: .62, kind: 'chapter'}, {id: 'credits', start: .95, end: 1, kind: 'credits'}] : []}
    hover={scrubbing ? .6 : null} overlay={scrubbing ? <PlayerScrubPreview at={.6} time={f('previewTime')}><Still /></PlayerScrubPreview> : undefined}
    input={{min: 0, max: 1, step: .001, value: live ? .96 : .2, onChange: none, 'aria-label': f('seek')}} />;
}
/** From 1024 the bar has room for volume, speed and picture in picture. */
function useWide() {
  const [wide, setWide] = useState(() => window.matchMedia('(min-width: 1024px)').matches);
  useEffect(() => {const media = window.matchMedia('(min-width: 1024px)'), update = () => setWide(media.matches); media.addEventListener('change', update); return () => media.removeEventListener('change', update);}, []);
  return wide;
}
/** The watch page under an upright phone's picture (phone-player 4): the title page's own content, without its art. */
function WatchPage() {
  const [tab, setTab] = useState('episodes');
  return <div className={frame.frame} style={{display: 'flex', flexDirection: 'column', gap: 'var(--space-sm)', padding: 'var(--space-sm) 0 var(--frame-bottom-clear)'}}>
    <div style={{margin: '0 var(--g)', display: 'grid', gap: 'var(--space-tight)'}}><h1 className={frame.pageTitle}>{f('tlou')}</h1><p className={frame.rowLabel}>{f('videoLead')}  {f('videoSubtitle')}</p><HeroFacts parts={fact(f('tlouFacts'))} platform="phone-web" /></div>
    <div style={{margin: '0 var(--g)', display: 'flex', gap: 'var(--spec-hero-actions-gap-phone)'}}><Button variant="primary" phone icon="i-pause" style={{flex: 1}}>{f('pause')}</Button>
      {([['i-plus', 'myList'], ['i-dl', 'download'], ['i-more', 'more']] as const).map(([icon, name]) => <Button key={name} phone iconOnly icon={icon} aria-label={f(name)} />)}</div>
    <Tabs phone items={[{value: 'episodes', label: f('episodes')}, {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]} value={tab} onValueChange={setTab} style={{padding: '0 var(--g)'}} />
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
function Player({state = 'playing'}: {state?: 'playing' | 'paused' | 'buffering' | 'scrubbing' | 'live' | 'missing'}) {
  const [volume, setVolume] = useState(.7), upright = usePhone(), sideways = useLandscapePhone(), desktop = useWide() && !sideways, live = state === 'live', missing = state === 'missing';
  const tool = (icon: AudioControl['icon'], name: FixtureName, disabled = false) => <PlayerControl onPicture icon={icon} label={f(name)} disabled={disabled} onPress={none} />;
  const subtitles = <PlayerPill icon="i-cc" name={f('subtitles')} value={f('english')} label={f('subtitles')} disabled={missing} onPress={none} />;
  const audio = <PlayerPill icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} disabled={missing} onPress={none} />;
  const quality = <PlayerPill icon={desktop ? undefined : 'i-quality'} qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} />;
  return <PlayerChromeFrame chromeVisible picture={<Still />} close={tool('i-down', 'close')} title={<PlayerTitle title={f('tlou')} lead={f('videoLead')} subtitle={f('videoSubtitle')} />}
    tools={desktop ? <>{tool('i-cast', 'playOn')}{tool('i-more', 'more')}{tool('i-x', 'dismiss')}</> : <>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{upright && tool('i-cc', 'subtitles', missing)}{tool('i-settings', 'playerSettings')}</>}
    scrubber={<Scrubber scrubbing={state === 'scrubbing'} live={live} chapters={!missing} />}
    times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration'), endsAt: live ? undefined : f('endsAt'), live: live ? f('live') : undefined}}
    transport={<><PlayerControl icon="i-prev" label={f('previous')} disabled={live || missing} onPress={none} /><PlayerControl icon={desktop ? 'i-back' : 'i-rew'} skip={10} label={f('rewind')} onPress={none} />
      <PlayerControl primary icon={state === 'paused' ? 'i-play' : 'i-pause'} busy={state === 'buffering'} label={f(state === 'paused' ? 'play' : 'pause')} onPress={none} />
      <PlayerControl icon={desktop ? 'i-fwd' : 'i-ffw'} skip={30} label={f('forward')} disabled={live} onPress={none} /><PlayerControl icon="i-next" label={f('next')} disabled={live || missing} onPress={none} /></>}
    leading={<Button size="small" icon="i-stack" disabled={live}>{f('episodesButton')}</Button>}
    trailing={desktop ? <><PlayerVolume value={volume} muted={false} label={f('volume')} muteLabel={f('volume')} onChange={setVolume} onMute={none} /><PlayerControl icon="i-cc" label={f('subtitles')} disabled={missing} onPress={none} />{quality}
        <PlayerPill value={f('speedShort')} label={f('speed')} disabled={live} onPress={none} /><PlayerControl icon="i-pip" label={f('pictureInPicture')} onPress={none} /><PlayerControl icon="i-full" label={f('fullScreen')} onPress={none} /></>
      : upright ? <>{quality}<PlayerControl icon="i-full" label={f('fullScreen')} onPress={none} /></> : <>{subtitles}{audio}{quality}</>}
    prompts={state === 'playing' ? <PlayerPrompt label={f('skipIntro')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}} /> : undefined}
    body={<WatchPage />} />;
}

function Audio({variant}: {variant: 'music' | 'missing' | 'book' | 'loading'}) {
  const art = useReferenceArtwork();
  const control = (icon: AudioControl['icon'], name: FixtureName, disabled = false): AudioControl => ({icon, label: f(name), onPress: none, disabled});
  const option = (name: FixtureName, icon?: AudioOption['icon'], shown?: FixtureName, disabled = false): AudioOption => ({label: f(name), icon, value: shown && f(shown), onPress: none, disabled});
  const book = variant === 'book', missing = variant === 'missing';
  const music = [control('i-shuffle', 'shuffle'), control('i-prev', 'previous'), control('i-pause', 'pause'), control('i-next', 'next', missing), control('i-repeat', 'repeat')] as const;
  const reading = [control('i-prev', 'previous'), control('i-rew', 'rewind'), control('i-pause', 'pause'), control('i-ffw', 'forward'), control('i-next', 'next')] as const;
  const queue = <div style={{display: 'grid', gap: 'var(--space-sm)'}}><h3 className={frame.groupLabel}>{f('upNext')}</h3>{(['queue1', 'queue2', 'queue3', 'queue4', 'queue5', 'queue6'] as const).map(name => <span key={name} className={frame.rowLabel}>{f(name)}</span>)}</div>;
  return <Window><AudioPlayerFrame loading={variant === 'loading'}
    close={control('i-down', 'close')} more={control('i-more', 'more')} context={book ? f('playingFromListening') : missing ? f('playingFromField') : f('playingFrom')}
    artwork={missing ? undefined : art(book ? 'pool/albums/rumours.jpg' : 'pool/albums/random-access-memories.jpg')} icon={book ? 'i-book' : 'i-music'}
    title={book ? f('hobbit') : missing ? f('longSong') : f('songTitle')} byline={book ? f('tolkien') : missing ? f('choir') : f('songArtist')} detail={book ? f('chapter') : undefined}
    scrubber={<><SeekLine variant="player" value={.34} input={{min: 0, max: 1, step: .001, value: .34, onChange: none, 'aria-label': f('seek')}} /><PlayerTimes position={f('elapsed')} remaining={f('remaining')} /></>} transport={book ? reading : music} nextLine={book ? f('timeLeft') : missing ? undefined : f('songNext')}
    options={book ? [option('speed', undefined, 'speedValue'), option('sleep', undefined, 'sleepValue'), option('chapters', undefined, 'chaptersValue'), option('thisPhone', 'i-cast')]
      : [option('lyrics', 'i-lyrics', undefined, missing), option('thisPhone', 'i-cast'), option('queue', 'i-queue')]}
    upNext={queue} /></Window>;
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
    <Section title={f('newOnServer')}><div style={{display: 'flex', gap: 'var(--space-sm)', margin: '0 var(--g)'}}>
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
const RegionsSheet = () => <Window><SheetFrame open onOpenChange={none} title={f('ownBarTitle')} footer={<Region name="regionFooter" />}><Region name="regionSheetRows" flexible height="calc(var(--frame-row-two-line) * 3)" /></SheetFrame></Window>;
const RegionsSignIn = () => <SignInFrame back={own.back} title={f('ownTitle')} lede={f('ownLede')}><Region name="regionForm" flexible height="calc(var(--frame-row-two-line) * 4)" /></SignInFrame>;
const RegionsPlayer = () => <PlayerChromeFrame chromeVisible picture={<Region name="regionPicture" />} close={<Region name="regionClose" height="var(--target-touch)" />} title={<Region name="regionPlayerTitle" height="var(--target-touch)" />}
  tools={<Region name="regionTools" height="var(--target-touch)" />} scrubber={<Region name="regionScrubber" height="var(--space-md)" />} times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration')}}
  transport={<Region name="regionTransport" height="var(--target-touch)" />} leading={<Region name="regionLeading" height="var(--target-touch)" />} trailing={<Region name="regionTrailing" height="var(--target-touch)" />}
  prompts={<Region name="regionPrompts" height="var(--target-touch)" />} body={<Region name="regionWatchPage" flexible height="calc(var(--frame-row-two-line) * 4)" />} />;
function RegionsAudio() {
  const slot = (icon: AudioControl['icon']): AudioControl => ({icon, label: f('ownAction') + icon, onPress: none});
  return <Window><AudioPlayerFrame close={slot('i-down')} more={slot('i-more')} context={f('ownContext')} icon="i-music" title={f('ownTitle')} byline={f('ownByline')} scrubber={<Region name="regionScrubber" height="var(--frame-audio-scrubber)" />}
    transport={[slot('i-shuffle'), slot('i-prev'), slot('i-pause'), slot('i-next'), slot('i-repeat')]} nextLine={f('ownNext')}
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
  section('player', 'player', Player), {...section('player-landscape', 'player', Player), width: 844, height: 390},
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
