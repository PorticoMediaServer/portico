import {useEffect, useMemo, useRef, useState, type ComponentType, type CSSProperties, type ReactNode} from 'react';
import {defaultI18n, formatMessage, type MessageId} from '@i18n';
import type {FactPart} from '@portico/client-core/kit-model';
import {framesFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures.ts';
import {playerFixtureMessages} from '../../../../packages/i18n/src/catalog/en-US/next/dev/player-fixtures.ts';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {ArtworkGlow, type AmbientArtSource} from '../../kit/artwork';
import {EpisodeTile} from '../../kit/cards';
import {Button, Chip, Tabs, TextField} from '../../kit/controls';
import {AudioPlayerFrame, EpisodeList, PlayerChromeFrame, SeasonBar, episodeRowClass, type AudioControl} from '../../kit/frames';
import {HeroFacts} from '../../kit/layout';
import {PlaybackInfo, SeekLine, type PlaybackInfoProjection, type TrackChoice} from '../../kit/media';
import {playbackInfoCopy} from '../../screens/watch/info-copy';
import {galleryCase} from './dock';
import {useReferenceArtwork} from './frames';
import {Dialog} from '../../kit/overlays';
import {usePhone} from '../../kit/overlays/shared';
import {AudioPanel, PlayerControl, PlayerEndCard, PlayerStillWatching, PlayerEpisodes, PlayerLive, PlayerOptions, PlayerPill, PlayerPrompt, PlayerScrubPreview, PlayerTitle, PlayerUpNext, PlayerVolume, QueueGroup, QueueRow,
  type PlayerEpisodeList, type PlayerOptionsModel, type PlayerPanel, type PlayerPanelContext, type PlayerRadioChoice} from '../../kit/player';
import {Icon} from '../../kit/Icon';
import {livePlayerPort} from '../live-player-fixture';
import frame from '../../kit/frames/frames.module.css';

/** Family D of the Stage 1 rebuild: frame 7 (the video player) and frame 8 (the audio player), each case full, with
 * every region `frameRegions.player` and `.audio` name (packages/design) filled from the kit's own parts, reference
 * text and artwork. Every case answers to the window, so the same case is captured at 1440, 390 and 844 x 390. Serve-only. */
type FrameName = keyof typeof framesFixtureMessages extends `next.frames.fixture.${infer Name}` ? Name : never;
type PlayerName = keyof typeof playerFixtureMessages extends `next.player.fixture.${infer Name}` ? Name : never;
const f = (name: FrameName) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
const p = (name: PlayerName) => formatMessage(playerFixtureMessages[`next.player.fixture.${name}`]);
const none = () => {};
const fact = (text: string): FactPart[] => text.split(' · ').map((part, index) => ({id: index === 0 ? 'year' : `fact-${index}`, text: part, role: index === 0 ? 'identity' : 'context'}));
const reference = (path: string) => new URL(`assets/${path}`, 'http://100.120.50.24:8787/').href;

function useQuery(query: string) {
  const [on, setOn] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {const media = window.matchMedia(query), update = () => setOn(media.matches); media.addEventListener('change', update); return () => media.removeEventListener('change', update);}, [query]);
  return on;
}
/** R8: a window this short is a phone on its side, whatever its width. */
const useSideways = () => useQuery('(max-height: 500px) and (min-width: 600px)');
const useWide = () => useQuery('(min-width: 1024px)');

/** The window's layer, which the frames never draw (R10): the one cover (`portico-cover`), black for the video player,
 * and for the audio player the kit's own glow from its artwork (`ArtworkGlow`, as NowPlaying draws it); with no artwork
 * the one backdrop. The shell's gutter is the page's. */
function Layer({children, video = false, glow}: {children: ReactNode; video?: boolean; glow?: string}) {
  const art = useReferenceArtwork(), phone = usePhone();
  return <div className="portico-cover" data-cover={video ? 'player' : glow ? 'audio' : undefined} style={{position: 'relative', height: '100%', overflow: 'hidden', '--shell-gutter': 'var(--page-gutter)'} as CSSProperties}>
    {/* The reference cover stands in for the server's 64-edge ambient wash (the kit draws only that variant). */}
    {glow && <ArtworkGlow source={{...art(glow), variant: 'ambient-v1', maxEdge: 64, version: '0'.repeat(64)} as AmbientArtSource} phone={phone} />}
    <div style={{position: 'relative', height: '100%', overflowY: 'auto'}}>{children}</div>
  </div>;
}

const Still = () => <img src={reference('bd-tlou.jpg')} alt="" style={{width: '100%', height: '100%', objectFit: 'cover'}} />;
/** The players' scrubber at a still position: one continuous line with the recap, intro and credits tinted on it
 * (desktop-player 1 draws no chapter marks on the line). */
function Scrubber({scrubbing = false, marks = true}: {scrubbing?: boolean; marks?: boolean}) {
  const upright = usePhone(), sideways = useSideways(), at = scrubbing && (upright || sideways) ? .65 : .29;
  return <SeekLine variant="player" value={at} buffered={[{start: at, end: at + .14}]}
    marks={marks ? [{id: 'recap', start: 0, end: .014, kind: 'intro'}, {id: 'intro', start: .02, end: .08, kind: 'intro'}, {id: 'credits', start: .95, end: 1, kind: 'credits'}] : []}
    hover={scrubbing ? .65 : null} overlay={scrubbing ? <PlayerScrubPreview at={.65} time={f('previewTime')}><Still /></PlayerScrubPreview> : undefined}
    input={{min: 0, max: 1, step: .001, value: at, onChange: none, 'aria-label': f('seek')}} />;
}
const episodes = [
  {name: 'episode1', number: 'episodeNumber1', status: 'episodeWatched', length: 'episodeLength1', watched: true, progress: undefined, playing: false, art: true},
  {name: 'episode2', number: 'episodeNumber2', status: 'episodePlaying', length: 'episodeLeft2', watched: false, progress: .29, playing: true, art: true},
  {name: 'episode3', number: 'episodeNumber3', status: 'episodeUpNext', length: 'episodeLength3', watched: false, progress: undefined, playing: false, art: false},
] as const;
/** The watch page under an upright phone's picture (phone-player 4): the title page's own content, without its art. */
function WatchPage({paused = false}: {paused?: boolean}) {
  const art = useReferenceArtwork(), [tab, setTab] = useState('episodes'), [hide, setHide] = useState(false);
  return <div className={frame.frame} style={{display: 'flex', flexDirection: 'column', gap: 'var(--space-sm)', padding: 'var(--space-sm) 0 var(--frame-bottom-clear)'}}>
    <div style={{margin: '0 var(--g)', display: 'grid', gap: 'var(--space-tight)'}}><h1 className={frame.pageTitle}>{f('tlou')}</h1><p className={frame.rowLabel}>{f('videoLead')}  {f('videoSubtitle')}</p><HeroFacts parts={fact(f('tlouFacts'))} platform="phone-web" /></div>
    <div style={{margin: '0 var(--g)', display: 'flex', gap: 'var(--spec-hero-actions-gap-phone)'}}><Button variant="primary" phone icon={paused ? 'i-play' : 'i-pause'} style={{flex: 1}}>{f(paused ? 'play' : 'pause')}</Button>
      {([['i-plus', 'myList'], ['i-more', 'more']] as const).map(([icon, name]) => <Button key={name} phone iconOnly icon={icon} aria-label={f(name)} />)}</div>
    <Tabs phone items={[{value: 'episodes', label: f('episodes')}, {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]} value={tab} onValueChange={setTab} listClassName={frame.tabsInset} />
    <div style={{display: 'grid', gap: 'var(--space-sm)', margin: '0 var(--g)'}}>
      <SeasonBar season={<Chip variant="toolbar" trailingIcon="i-down">{f('episodesSeason')}</Chip>} count={f('episodesCount')} hideWatched={{label: f('hideWatched'), checked: hide, onCheckedChange: setHide}} />
      <EpisodeList layout="list">{episodes.map(episode => <EpisodeTile key={episode.name} className={episodeRowClass} context="detail" artworkKind="still" artwork={episode.art ? art('bd-tlou.jpg') : undefined} playing={episode.playing} watched={episode.watched}
        model={{title: f(episode.name), caption: [{id: 'episode', text: f(episode.number), role: 'context'}], detail: [], progress: episode.progress}}
        playback={[{id: 'state', text: f(episode.status), role: 'identity'}, {id: 'length', text: f(episode.length), role: 'context'}]} description={f('tlouSynopsis')} onNavigate={none} />)}</EpisodeList>
    </div>
  </div>;
}

type PlayerState = 'playing' | 'paused' | 'buffering' | 'scrubbing' | 'missing' | 'recap' | 'undo';
/** The video player filled with the kit's player parts, with what each mock-up puts in each place for its form
 * (desktop-player 1, phone-player 1 on its side, phone-player 4 upright). `opened` is the button whose panel shows. */
function Player({state = 'playing', panel, panelCovers, card, opened, page}: {state?: PlayerState; panel?: ReactNode; panelCovers?: 'bar' | 'all'; card?: ReactNode; opened?: 'episodes' | 'subtitles';
  /** An upright phone: what stands in the watch page's place under the strip (the end card). */ page?: ReactNode}) {
  const [volume, setVolume] = useState(.7), upright = usePhone(), sideways = useSideways(), desktop = useWide() && !sideways, missing = state === 'missing';
  const tool = (icon: AudioControl['icon'], name: FrameName, disabled = false) => <PlayerControl onPicture icon={icon} label={f(name)} disabled={disabled} onPress={none} />;
  const subtitles = <PlayerPill icon="i-subs" name={f('subtitles')} value={f('english')} label={f('subtitles')} disabled={missing} open={opened === 'subtitles'} onPress={none} />;
  const audio = <PlayerPill icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} disabled={missing} onPress={none} />;
  const quality = <PlayerPill icon={desktop ? undefined : 'i-quality'} qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} />;
  const prompt = state === 'playing' ? <PlayerPrompt label={f('skipIntro')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}} />
    : state === 'recap' ? <PlayerPrompt label={p('skipRecap')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}} />
    : state === 'undo' ? <PlayerPrompt note={p('skippedIntro')} label={p('undo')} icon="i-undo" onPress={none} /> : undefined;
  return <Layer video><PlayerChromeFrame chromeVisible={state !== 'undo'} scrubbing={state === 'scrubbing'} panel={panel} panelCovers={panelCovers} card={card} picture={<Still />} close={tool('i-down', 'close')}
    title={<PlayerTitle title={f('tlou')} lead={f('videoLead')} subtitle={opened === 'episodes' ? p('pausedAt') : f('videoSubtitle')} />}
    tools={desktop ? <>{tool('i-cast', 'playOn')}{tool('i-more', 'more')}{tool('i-x', 'dismiss')}</> : <>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{upright && tool('i-subs', 'subtitles', missing)}{tool('i-sliders', 'playerSettings')}</>}
    scrubber={<Scrubber scrubbing={state === 'scrubbing'} marks={!missing} />}
    times={{position: f('elapsed'), remaining: f('remaining'), duration: f('duration'), endsAt: f('endsAt')}}
    transport={<><PlayerControl icon="i-prev" label={f('previous')} disabled={missing} onPress={none} /><PlayerControl icon={desktop ? 'i-back' : 'i-rew'} skip={10} label={f('rewind')} onPress={none} />
      <PlayerControl primary icon={state === 'paused' ? 'i-play' : 'i-pause'} busy={state === 'buffering'} label={f(state === 'paused' ? 'play' : 'pause')} onPress={none} />
      <PlayerControl icon={desktop ? 'i-fwd' : 'i-ffw'} skip={30} label={f('forward')} onPress={none} /><PlayerControl icon="i-next" label={f('next')} disabled={missing} onPress={none} /></>}
    leading={desktop ? <Button size="small" icon="i-stack" aria-expanded={opened === 'episodes'}>{f('episodesButton')}</Button> : <PlayerPill icon="i-stack" name={f('episodesButton')} label={f('episodesButton')} open={opened === 'episodes'} onPress={none} />}
    trailing={desktop ? <><PlayerVolume value={volume} muted={false} label={f('volume')} muteLabel={f('volume')} onChange={setVolume} onMute={none} /><PlayerControl tool icon="i-subs" label={f('subtitles')} disabled={missing} open={opened === 'subtitles'} onPress={none} />{quality}
        <PlayerPill value={f('speedShort')} label={f('speed')} onPress={none} /><PlayerControl tool icon="i-pip" label={f('pictureInPicture')} onPress={none} /><PlayerControl tool icon="i-full" label={f('fullScreen')} onPress={none} /></>
      : upright ? <>{quality}<PlayerControl tool icon="i-full" label={f('fullScreen')} onPress={none} /></> : <>{subtitles}{audio}{quality}</>}
    prompts={prompt} body={page ?? <WatchPage paused={state === 'paused'} />} /></Layer>;
}

/** The player's own panels (kit/player), each over the player as the player screen draws it. One panel of columns on
 * the desktop (desktop-player 3: Search for subtitles, two quality lines, Playback info), a bottom sheet on an upright
 * phone and a side sheet on a phone on its side whose lists open in the same sheet (phone-player 3). */
const track = (id: string, primary: FrameName, secondary: FrameName | undefined, extra: Partial<TrackChoice> = {}): TrackChoice => ({id, primary: f(primary), secondary: secondary && f(secondary), fullName: f(primary), selected: false, ...extra});
const choice = (id: string, label: FrameName, detail: FrameName | undefined, selected = false): PlayerRadioChoice => ({id, label: f(label), detail: detail && f(detail), selected, enabled: true});
const optionsModel: PlayerOptionsModel = {
  fence: {} as PlayerOptionsModel['fence'], automatic: true, qualityLabel: f('qualityValue'), qualityAccessibleLabel: f('qualityValue'), audioLabel: f('english'), subtitleLabel: f('english'),
  subtitles: [track('off', 'optOff', undefined), track('en', 'english', 'optSrt', {selected: true}), track('en-sdh', 'optEnglishSdh', 'optSrt'), track('es', 'optSpanish', 'optSrt'), track('fr', 'optFrench', 'optSrtBeside')],
  subtitleAdditions: [],
  subtitleSearch: {reason: p('searchSubtitlesReason'), busy: false, searched: false, canRetry: false, results: [], credit: '', search: async () => {}, choose: async () => {}} as unknown as PlayerOptionsModel['subtitleSearch'],
  audio: [track('en', 'english', 'optAac51', {selected: true}), track('es', 'optSpanish', 'optAacStereo'), track('commentary', 'optCommentary', 'optAacStereo', {unavailable: true})],
  audioNotes: [f('optAudioNote')],
  quality: [choice('original', 'optOriginal', 'optOriginalDetail'), choice('auto', 'optAutomatic', 'optAutomaticDetail', true), choice('720', 'opt720', 'opt720Detail'), choice('480', 'opt480', 'opt480Detail')],
  qualityNotes: [f('optQualityNote'), p('qualityPlaying')],
  speed: [['0.5', 'optSpeed05'], ['0.75', 'optSpeed075'], ['1', 'optNormal'], ['1.25', 'optSpeed125'], ['1.5', 'optSpeed15'], ['2', 'optSpeed2']].map(([id, label]) => ({id, label: f(label as FrameName), selected: id === '1', enabled: true})),
  subtitleTiming: {offsetSeconds: 0, shift: async () => {}}, subtitleStyle: {size: 'medium', background: 'translucent', set: none}, sleepTimer: {minutes: 0, set: none}, subtitleForced: {only: false, set: none},
  isCurrent: () => true, selectAudio: async () => {}, selectSubtitle: async () => {}, selectQuality: async () => {}, selectSpeed: none,
};
function PlayerPanelCase({panel}: {panel: PlayerPanel}) {
  const upright = usePhone(), sideways = useSideways(), anchor = useRef<HTMLButtonElement>(null);
  const context: PlayerPanelContext = {panel, snapshot: {queueView: {remainingCount: 3}} as unknown as PlayerPanelContext['snapshot'], layout: upright || sideways ? 'phone' : 'desktop', landscape: sideways,
    phoneTop: 220, available: ['options', 'subtitles', 'audio', 'quality', 'speed', 'episodes', 'queue', 'info'], close: none, show: none, returnFocusRef: anchor};
  return <Player state="paused" opened={upright || sideways ? undefined : 'subtitles'} panelCovers={sideways ? 'all' : undefined}
    panel={<PlayerOptions model={optionsModel} context={context} i18n={defaultI18n} reportError={none} openSettings={none} info={{} as never} />} />;
}
const episodeList: PlayerEpisodeList = {seasonId: 's1', seasons: [{id: 's1', number: 1, title: ''}, {id: 's2', number: 2, title: ''}],
  rows: ([f('episode1'), f('episode2'), f('episode3'), p('episode4')]).map((title, index) => ({itemId: `episode${index + 1}`, title, number: index + 1, stillUrl: index === 3 ? 'pool/tv/the-last-of-us--bd.jpg' : 'bd-tlou.jpg',
    overview: f('tlouSynopsis'), airDate: ['2023-01-15', '2023-01-22', '2023-01-29', '2023-02-05'][index], durationSeconds: [4860, 3180, 4560, 2760][index],
    progressSeconds: index === 1 ? 842 : undefined, watched: index === 0, playing: index === 1, next: index === 2}))};
/** Episodes per form (owner ruling 5): the drawer across the bottom on the web (desktop-player f6), over the whole
 * window on a phone on its side; upright, Episodes is the watch page's own tab under the picture (phone-player 4). */
function PlayerEpisodesCase() {
  const art = useReferenceArtwork(), upright = usePhone(), sideways = useSideways(), anchor = useRef<HTMLButtonElement>(null);
  if (upright) return <Player state="paused" opened="episodes" />;
  const context: PlayerPanelContext = {panel: 'episodes', snapshot: {} as PlayerPanelContext['snapshot'], layout: sideways ? 'phone' : 'desktop', landscape: sideways, phoneTop: 220, close: none, show: none, returnFocusRef: anchor};
  return <Player state="paused" opened="episodes" panelCovers={sideways ? 'all' : 'bar'} panel={<PlayerEpisodes episodes={episodeList} context={context} artwork={art} onPlay={none} onSeason={none} reportError={none} />} />;
}
/** Up next over the credits: desktop-player 4 (Play now, Skip credits, Watch credits and the scene after the credits)
 * and 4s (the countdown), phone-player 6. */
function PlayerUpNextCase({variant}: {variant: 'plain' | 'credits' | 'countdown'}) {
  const upright = usePhone(), sideways = useSideways(), wide = useWide();
  const actions = [{id: 'play', label: f('upNextPlayNow'), primary: true, glyph: 'play' as const, onPress: none}, ...(variant === 'credits' ? [{id: 'skip', label: p('skipCredits'), glyph: 'next' as const, onPress: none}] : []), {id: 'credits', label: f('upNextWatchCredits'), onPress: none}];
  return <Player card={<PlayerUpNext layout={upright || sideways ? 'phone' : wide ? 'desktop' : 'tablet'} heading={f('upNextHeading')} still={<Still />} title={f('episode3')} facts={[f('upNextCode'), f('upNextDuration')]}
    when={variant === 'credits' ? p('startsAfterScene') : variant === 'countdown' ? p('playingIn') : f('upNextWhen')} progress={variant === 'countdown' ? .8 : undefined} note={variant === 'credits' ? p('sceneAfter') : undefined} actions={actions} />} />;
}
/** The end of a film (desktop-player 4b, phone-player 6d): the kit's end card over the picture. */
function PlayerEndCase() {
  // The screen's own test: under 1024 wide is the phone layout (WatchScreen). Upright, the card takes the watch page's place.
  const art = useReferenceArtwork(), phone = !useWide(), upright = usePhone();
  const related = (['spirited-away', 'inside-out', 'alien', 'blade-runner', 'dune', 'barbie'] as const).map(id => ({id, title: id.split('-').map(word => word[0]!.toUpperCase() + word.slice(1)).join(' '), poster: art(`pool/movies/${id}.jpg`)}));
  const end = <div style={{position: upright ? 'relative' : 'absolute', inset: 0, height: upright ? '100%' : undefined}}><PlayerEndCard layout={phone ? 'phone' : 'desktop'} canReplay onReplay={none} onClose={none} reportError={none} artwork={() => undefined}
    card={{title: f('insideOut'), year: 2015, genres: ['Animation', 'Family'], related, rating: {value: null, set: async () => {}}, back: none, openRelated: none}} /></div>;
  return upright ? <Player state="paused" page={end} /> : <Player card={end} />;
}

/** Still watching? (phone-player 6e): the kit's prompt over the paused player; nothing starts until the viewer says so. */
const PlayerStillWatchingCase = () => <Player state="paused" card={<PlayerStillWatching episode={f('upNextCode')} title={f('episode3')} onContinue={none} onReplay={none} onClose={none} />} />;

/** Playback info (playback-info 1, 15): the kit's panel beside the paused picture on the web, a sheet under an upright
 * phone's picture, with one converted film's route, reason and sections. */
const infoProjection: PlaybackInfoProjection = {itemTitle: p('infoTitle'), deviceName: p('infoDevice'), route: 'converted', explanation: p('infoReason'), networkAndBitrate: p('infoNetwork'), sections: [
  {kind: 'picture', route: 'converted', losses: [p('infoVisionLost')], rows: [
    {field: 'container', source: p('infoMkv'), delivered: p('infoMp4'), changed: true}, {field: 'codec', source: p('infoHevc'), delivered: p('infoHevc'), changed: false},
    {field: 'hdr', source: p('infoVision'), delivered: p('infoHdr10'), changed: true}, {field: 'bitDepth', source: p('info10bit'), delivered: p('info10bit'), changed: false},
    {field: 'resolution', source: p('info4k'), delivered: p('info4k'), changed: false}, {field: 'frameRate', source: p('infoFps'), delivered: p('infoFps'), changed: false}]},
  {kind: 'sound', route: 'converted', losses: [p('infoAtmosLost')], rows: [
    {field: 'format', source: p('infoDdp'), delivered: p('infoAac'), changed: true}, {field: 'channels', source: p('info51'), delivered: p('info51'), changed: false},
    {field: 'sampleRate', source: p('info48'), delivered: p('info48'), changed: false}, {field: 'output', delivered: p('infoDecoded'), changed: false}]},
  {kind: 'subtitles', route: 'original', losses: [], rows: [{field: 'track', source: p('infoSrt'), delivered: p('infoSrt'), changed: false}, {field: 'shown', delivered: p('infoDrawn'), changed: false}]},
]};
function PlayerInfoCase() {
  const upright = usePhone(), anchor = useRef<HTMLElement>(null), copy = useMemo(() => playbackInfoCopy(defaultI18n), []);
  return <Player state="paused" card={<PlaybackInfo projection={infoProjection} copy={copy} layout={upright ? 'phone' : 'desktop'} open onOpenChange={none} returnFocusRef={anchor} phoneTop={upright ? 220 : undefined} />} />;
}

/** The player minimised into the dock (desktop-player f5, phone-player 5) and playing on another device (tablet-play 13,
 * phone-player 7): the dock family's own bar at the foot of a page. */
const dockCases = {
  video: [galleryCase({}), galleryCase({layout: 'phone', web: true})], music: [galleryCase({kind: 'music'}), galleryCase({layout: 'phone', kind: 'music', web: true})],
  casting: [galleryCase({remote: 'playing', remoteKind: 'cast'}), galleryCase({layout: 'phone', remote: 'playing', remoteKind: 'cast'})],
  connecting: [galleryCase({remote: 'connecting', remoteKind: 'cast'}), galleryCase({layout: 'phone', remote: 'connecting', remoteKind: 'cast'})],
} as const;
function PlayerDockCase({kind}: {kind: keyof typeof dockCases}) {
  const upright = usePhone(), Bar = dockCases[kind][upright ? 1 : 0];
  return <Layer><img src={reference('bd-sev.jpg')} alt="" style={{position: 'absolute', inset: 0, width: '100%', height: '100%', objectFit: 'cover', opacity: .45}} />
    <div style={{position: 'absolute', left: 0, right: 0, bottom: 0}}><Bar /></div></Layer>;
}

/** The Live TV player's own chrome (kit/player `PlayerLive`; desktop-live "live-player", phone-player 1c), drawn from
 * one port of channel facts. Upright, the channel's page is under the picture. */
function PlayerLiveCase({guide = false}: {guide?: boolean}) {
  const [volume, setVolume] = useState(.7), wide = useWide(), sideways = useSideways(), upright = usePhone(), pointer = wide && !sideways;
  const chrome = <PlayerLive port={livePlayerPort} playing initialMiniGuideOpen={guide} onCollapse={none} onStop={none} onPlayPause={none} onSkip={none} onSeek={none} onMore={none} reportError={none}
    right={upright ? <PlayerPill qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} />
      : sideways ? <><PlayerPill icon="i-subs" name={f('subtitles')} value={f('optOff')} label={f('subtitles')} onPress={none} /><PlayerPill icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} onPress={none} />
        <PlayerPill icon="i-quality" qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} /></> : <>{pointer && <PlayerVolume value={volume} muted={false} label={f('volume')} muteLabel={f('volume')} onChange={setVolume} onMute={none} />}<PlayerControl tool icon="i-subs" label={f('subtitles')} onPress={none} />
      <PlayerPill qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none} /><PlayerControl tool icon="i-pip" label={f('pictureInPicture')} onPress={none} /><PlayerControl tool icon="i-full" label={f('fullScreen')} onPress={none} /></>} />;
  const picture = <div style={{position: 'absolute', inset: 0, display: 'grid'}}><Still /></div>;
  if (upright) return <Layer video><div style={{position: 'relative', aspectRatio: '16 / 9', overflow: 'hidden'}}>{picture}{chrome}</div>
    <div className={frame.frame} style={{display: 'grid', gap: 'var(--space-sm)', padding: 'var(--space-sm) var(--g)', background: 'var(--color-ink)'}}>
      <p className={frame.rowLabel}><b>{f('liveChannelName')}  {f('liveNumber')}</b>  <b style={{color: 'var(--color-record)'}}>{f('liveNow')}</b></p>
      <h1 className={frame.pageTitle}>{f('liveTitle')}</h1><p className={frame.intro}>{f('liveThen')}</p>
      <div style={{display: 'flex', gap: 'var(--spec-hero-actions-gap-phone)'}}><Button phone icon="i-list">{f('liveGuide')}</Button><Button phone icon="i-rec">{f('liveRecord')}</Button></div>
    </div></Layer>;
  return <Layer video><div style={{position: 'relative', height: '100%', overflow: 'hidden'}}>{picture}{chrome}</div></Layer>;
}

/** The audio player (frame 8) with its full regions: desktop-listen "now", "now-states" and "book-player",
 * final3-mobileweb-03 frame 8. `panel` is the queue (or an audiobook's chapters, or Lyrics). */
type AudioVariant = 'music' | 'missing' | 'book' | 'loading' | 'lyrics' | 'choose' | 'save' | 'queue';
function Audio({variant}: {variant: AudioVariant}) {
  // A phone on its side shows the queue as a sheet too, only while it is open.
  const art = useReferenceArtwork(), sideways = useSideways(), phone = usePhone() || sideways, [volume, setVolume] = useState(.72);
  const control = (icon: AudioControl['icon'], label: string, extra: Partial<AudioControl> = {}): AudioControl => ({icon, label, onPress: none, ...extra});
  const book = variant === 'book', missing = variant === 'missing', loading = variant === 'loading';
  const [tab, setTab] = useState(variant === 'lyrics' || variant === 'choose' ? 'lyrics' : book ? 'chapters' : 'queue');
  const music = [control('i-shuffle', f('shuffle')), control('i-prev', f('previous')), control('i-pause', f('pause')), control('i-next', f('next'), {disabled: missing}), control('i-repeat', f('repeat'), {active: true})] as const;
  const reading = [control('i-prev', f('previous')), control('i-back', p('back15'), {skip: 15}), control('i-pause', f('pause')), control('i-fwd', f('forward'), {skip: 30}), control('i-next', f('next'))] as const;
  const moves = [{label: p('moveUp'), icon: 'i-arrow-up' as const, onPress: none}, {label: p('moveDown'), icon: 'i-arrow-down' as const, onPress: none}, {label: p('removeFromQueue'), icon: 'i-x' as const, onPress: none}];
  const row = phone ? {more: {label: f('more'), onPress: none}} : {actions: moves};
  const songs = [['added1', 'pool/albums/rumours.jpg'], ['added2', 'pool/albums/hit-me-hard-and-soft.jpg'], ['added3', 'pool/albums/abbey-road-2019-mix.jpg'], ['added4', 'pool/albums/random-access-memories.jpg'], ['added5', 'pool/albums/21.jpg']] as const;
  const queue = <>
    <QueueGroup label={f('queueNow')} />
    <QueueRow lead="playing" artwork={art('pool/albums/after-hours.jpg')} title={f('songTitle')} subtitle={f('songArtist')} duration={p('songLength')} onPress={none} {...(phone ? row : {})} />
    <QueueGroup label={f('queueNextFrom')} />
    <QueueRow lead="handle" artwork={art('pool/albums/after-hours.jpg')} title={f('queue1')} subtitle={f('songArtist')} duration={f('queueDuration1')} onPress={none} {...row} />
    <QueueGroup label={p('addedByYou')} hint={phone ? p('reorderTouch') : p('reorderPointer')} />
    {songs.map(([name, cover]) => <QueueRow key={name} lead="handle" artwork={art(cover)} title={p(name)} subtitle={p(`${name}Artist`)} duration={p(`${name}Length`)} onPress={none} {...row} />)}
  </>;
  const chapterRows = <>
    <QueueGroup label={p('chaptersCount')} hint={p('chapterOfTotal')} />
    {(['chapter11', 'chapter12', 'chapter13', 'chapter14'] as const).map(name => <QueueRow key={name} lead="done" title={p(name)} duration={p(`${name}Length`)} onPress={none} />)}
    <QueueRow lead="playing" title={p('bookChapter')} progress={.38} duration={p('chapterLeft')} onPress={none} />
    {(['chapter16', 'chapter17', 'chapter18'] as const).map((name, index) => <QueueRow key={name} lead={16 + index} title={p(name)} duration={p(`${name}Length`)} onPress={none} />)}
  </>;
  // Lyrics (desktop-listen "now-states"): none for this song, with the owner's pointer to the library's setting; or a choice between two sets.
  const lyrics = variant === 'choose' ? <div className={frame.frame} style={{display: 'grid', gap: 'var(--space-xs)', padding: 'var(--space-md)'}}>
      <p className={frame.rowLabel}><b>{p('lyricsChoose')}</b></p><p className={frame.rowHelp}>{p('lyricsChooseBody')}</p>
      {([['lyricsSynced', 'lyricsSyncedDetail', true], ['lyricsPlain', 'lyricsPlainDetail', false]] as const).map(([name, detail, on]) => <Button key={name} variant="text" aria-pressed={on} style={{justifyContent: 'space-between', height: 'auto', padding: 'var(--space-sm)', textAlign: 'start'}}>
        <span style={{display: 'grid'}}><b className={frame.rowLabel}>{p(name)}</b><small className={frame.rowHelp}>{p(detail)}</small></span>{on && <Icon name="i-check" />}</Button>)}
    </div>
    : <div className={frame.frame} style={{display: 'grid', justifyItems: 'center', gap: 'var(--space-sm)', padding: 'var(--space-lg) var(--space-md)', textAlign: 'center'}}>
      <Icon name="i-lyrics" size={40} /><p className={frame.rowLabel}><b>{p('lyricsNone')}</b></p><p className={frame.rowHelp}>{p('lyricsNoneBody')}</p>
      <div style={{justifySelf: 'stretch', display: 'flex', alignItems: 'center', gap: 'var(--space-sm)', padding: 'var(--space-sm)', borderRadius: 'var(--frame-card-radius)', background: 'var(--color-raised-high)', textAlign: 'start'}}>
        <span className={frame.rowHelp} style={{flex: 1}}><b style={{color: 'var(--color-text)'}}>{p('lyricsOwner')}</b><br />{p('lyricsOwnerBody')}</span><Button size="xs">{p('librarySettings')}</Button></div>
    </div>;
  const tabs = book ? {items: [{value: 'chapters', label: f('chapters'), icon: 'i-chapters' as const}, {value: 'queue', label: f('upNext'), icon: 'i-queue' as const}], value: tab, onValueChange: setTab}
    : {items: [{value: 'queue', label: f('upNext'), icon: 'i-queue' as const}, {value: 'lyrics', label: f('lyrics'), icon: 'i-lyrics' as const}], value: tab, onValueChange: setTab};
  const footer = book ? {text: p('chaptersHint')} : {text: p('queueCount'), action: {label: p('saveAsPlaylist'), icon: 'i-list-plus' as const, onPress: none}};
  const body = tab === 'lyrics' ? lyrics : book && tab === 'chapters' ? chapterRows : queue;
  // At phone width the queue is a sheet only while it is open; nothing queued leaves the panel out.
  const panel = missing || (phone && !['queue', 'lyrics', 'choose'].includes(variant)) ? undefined
    : <AudioPanel label={f('upNext')} tabs={tabs} footer={tab === 'lyrics' ? undefined : footer}>{body}</AudioPanel>;
  const choices = (labels: readonly string[], chosen: number) => labels.map((label, index) => ({label, selected: index === chosen, onPress: none}));
  const cover = missing ? undefined : book ? 'pool/books/the-martian-unabridged.jpg' : 'pool/albums/after-hours.jpg';
  const save = variant === 'save' && <Dialog open onOpenChange={none} title={p('saveAsPlaylist')} footer={<><Button variant="glass" size="small">{f('cancel')}</Button><Button variant="primary" size="small">{p('save')}</Button></>}>
    <p className={frame.rowHelp}>{p('saveBody')}</p><TextField label={p('playlistName')} value={p('playlistNameValue')} onChange={none} /></Dialog>;
  return <Layer glow={loading ? undefined : cover}><AudioPlayerFrame loading={loading}
    close={control('i-down', f('close'))} more={control('i-more', f('more'))} favourite={control('i-heart-f', f('favourite'), {active: true})} titleMore={control('i-more', p('more'))} device={control('i-cast', f('thisBrowser'))}
    context={phone && !book ? f('playingFrom') : f('playingFromListening')} source={book ? p('audiobooks') : missing ? f('playingSourceField') : f('playingSource')} notice={book ? p('wentBack') : undefined}
    artwork={cover ? art(cover) : undefined} icon={book ? 'i-book' : 'i-music'} title={book ? p('martian') : missing ? f('longSong') : f('songTitle')} badge={book ? p('unabridged') : undefined}
    byline={book ? [p('weir'), p('readBy')] : missing ? [f('choir')] : [f('songArtist'), f('playingSource')]}
    chapter={book ? {label: p('bookChapter'), of: phone ? f('chapterOf') : p('bookChapterOf')} : undefined}
    scrubber={<SeekLine variant="player" value={book ? .58 : .51} input={{min: 0, max: 1, step: .001, value: book ? .58 : .51, onChange: none, 'aria-label': f('seek')}} />}
    times={book ? phone ? {position: f('bookElapsed'), middle: f('timeLeft'), end: f('bookRemaining')} : {position: p('bookPosition'), end: p('bookLeft')} : {position: f('audioElapsed'), end: phone ? f('audioRemaining') : p('songLength')}}
    transport={book ? reading : music} volume={<PlayerVolume value={volume} muted={false} label={f('volume')} muteLabel={f('volume')} onChange={setVolume} onMute={none} />}
    next={book || missing ? undefined : {label: f('nextLabel'), title: f('nextTitle')}}
    lyrics={book ? undefined : control('i-lyrics', f('lyrics'), {disabled: missing})} queue={book ? undefined : control('i-queue', f('upNext'), {disabled: missing})}
    speed={book ? {label: f('speed'), value: f('speedValue'), help: p('speedHelp'), onPress: none, choices: choices([f('optSpeed05'), f('optSpeed075'), p('speed1'), f('optSpeed125'), f('optSpeed15'), p('speed175'), f('optSpeed2'), p('speed25'), p('speed3')], 3)} : undefined}
    sleep={book ? {label: f('sleep'), value: f('sleepValue'), icon: 'i-moon', help: p('sleepHelp'), active: true, onPress: none, choices: choices([f('optOff'), p('sleep15'), p('sleep30'), p('sleep45'), p('sleep60'), p('sleep90'), p('sleepEnd')], 2)} : undefined}
    chapters={book ? {label: f('chapters'), value: f('chaptersValue'), icon: 'i-list', onPress: none} : undefined}
    panel={panel} />{save}</Layer>;
}

const section = (id: string, label: string, render: ComponentType, height = 900): GalleryCase => ({id, labelKey: `next.platform.frame.${label}` as MessageId, width: 1440, height, render});
export default defineGalleryFamily({id: 'frames-player', labelKey: 'next.platform.frame.playerFamily', cases: [
  section('player', 'player', () => <Player />),
  ...(['paused', 'buffering', 'scrubbing', 'missing'] as const).map(state => section(`player-${state}`, 'player', () => <Player state={state} />)),
  section('player-skip', 'playerSkip', () => <Player state="recap" />), section('player-undo', 'playerSkip', () => <Player state="undo" />),
  section('player-options', 'playerOptions', () => <PlayerPanelCase panel="options" />), section('player-subtitles', 'playerSubtitles', () => <PlayerPanelCase panel="subtitles" />), section('player-audio', 'playerAudio', () => <PlayerPanelCase panel="audio" />),
  section('player-quality', 'playerQuality', () => <PlayerPanelCase panel="quality" />), section('player-speed', 'playerSpeed', () => <PlayerPanelCase panel="speed" />),
  section('player-episodes', 'playerEpisodes', PlayerEpisodesCase),
  section('player-up-next', 'playerUpNext', () => <PlayerUpNextCase variant="plain" />), section('player-up-next-credits', 'playerUpNextCredits', () => <PlayerUpNextCase variant="credits" />),
  section('player-up-next-countdown', 'playerUpNextCountdown', () => <PlayerUpNextCase variant="countdown" />), section('player-end-card', 'playerEndCard', PlayerEndCase),
  section('player-still-watching', 'playerStillWatching', PlayerStillWatchingCase), section('player-info', 'playerInfo', PlayerInfoCase),
  section('player-dock', 'playerDock', () => <PlayerDockCase kind="video" />), section('player-dock-music', 'playerDock', () => <PlayerDockCase kind="music" />),
  section('player-casting', 'playerDock', () => <PlayerDockCase kind="casting" />), section('player-casting-connecting', 'playerDock', () => <PlayerDockCase kind="connecting" />),
  section('player-live-controls', 'playerLiveControls', () => <PlayerLiveCase />), section('player-live-guide', 'playerLiveGuide', () => <PlayerLiveCase guide />),
  section('audio', 'audio', () => <Audio variant="music" />), section('audio-lyrics', 'audioLyrics', () => <Audio variant="lyrics" />), section('audio-lyrics-choose', 'audioLyricsChoose', () => <Audio variant="choose" />),
  section('audio-save', 'audioSave', () => <Audio variant="save" />), section('audio-queue', 'audioQueue', () => <Audio variant="queue" />),
  section('audio-missing', 'audioMissing', () => <Audio variant="missing" />), section('audio-book', 'audioBook', () => <Audio variant="book" />), section('audio-loading', 'audioLoading', () => <Audio variant="loading" />),
]});
