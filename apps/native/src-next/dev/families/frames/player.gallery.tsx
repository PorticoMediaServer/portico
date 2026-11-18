import React, {useState, type ReactNode} from 'react';
import {Platform, ScrollView, StyleSheet, Text, View, useWindowDimensions} from 'react-native';
import {formatMessage} from '../../../../../../packages/i18n/src';
import {framesFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/frames-fixtures';
import {playerFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/player-fixtures';
import {AmbientArt, Artwork, ChannelMark, type ArtworkSource} from '../../../kit/artwork/index';
import {livePlayerFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/live-player-fixtures';
import type {PlaybackComparisonProjection, PlaybackInfoField} from '../../../../../../packages/client-core/src/playback-info-projection';
import {MiniGuideAll, MiniGuideHead, MiniGuideRow} from '../../../kit/live';
import {DockCase} from '../dock/dock.gallery';
import {EpisodeTile} from '../../../kit/cards';
import {Button, KeyHints, Switch, Tabs} from '../../../kit/controls';
import {AudioPlayerFrame, PlayerChromeFrame, type AudioControl, type AudioOption} from '../../../kit/frames';
import {frameSpec} from '@portico/design';
import {useFrameMetrics, usePlayerUpright} from '../../../kit/frames/metrics';
import {AudioPanel, NativeStillWatchingCard, PlayerChannelStep, PlayerEndCard, PlayerEpisodeBand, PlaybackInfoComparison, PlayerControl, PlayerFilmstrip, PlayerLiveActions, PlayerPanel, PlayerPanelGroup, PlayerPanelRow, PlayerPill, PlayerPrompt, PlayerScrubBar, PlayerScrubPreview, PlayerTitle, PlayerUpNext,
  QueueGroup, QueueRow} from '../../../kit/player';
import {HeroMetadata, SeasonBar} from '../../../kit/layout';
import {Icon} from '../../../kit/Icon';
import {useTheme} from '../../../kit/theme';
import type {Gallery} from '../../gallery-registry';

/** Frame 7 (the video player) and frame 8 (the audio player), each case full, on every form the window gives it:
 * family D of the Stage 1 rebuild. Every region the frame has (packages/design `frameRegions.player` and `.audio`) is
 * filled with the kit's own parts and the reference text and artwork. Development builds only. */
type FrameName = keyof typeof framesFixtureMessages extends `next.frames.fixture.${infer Name}` ? Name : never;
type PlayerName = keyof typeof playerFixtureMessages extends `next.player.fixture.${infer Name}` ? Name : never;
const f = (name: FrameName) => formatMessage(framesFixtureMessages[`next.frames.fixture.${name}`]);
const p = (name: PlayerName) => formatMessage(playerFixtureMessages[`next.player.fixture.${name}`]);
const asset = (path: string): ArtworkSource => ({uri: `http://100.120.50.24:8787/assets/${path}`, cacheKey: `frames-reference:${path}`});
const none = () => {};
const chip = (part: string) => /^\d+ h( \d+ min)?$|^\d+ min$/.test(part) ? 'runtime' : / seasons?$/.test(part) ? 'seasons' : /^(G|PG|PG-13|R|TV-[A-Z0-9]+)$/.test(part) ? 'rating' : undefined;
const fact = (text: string) => text.split(' · ').map((part, index) => ({id: chip(part) ?? (index === 0 ? 'year' : `fact-${index}`), text: part, role: index === 0 ? 'identity' as const : 'context' as const}));

/** One whole window. The audio player's layer draws the glow from its artwork (R10: `Layer kind="audio"`); the
 * gallery stands in for that host here, and the frame draws no backdrop of its own. The video player's layer is black. */
function Window({children, glow, video = false}: {children: ReactNode; glow?: ArtworkSource | null; video?: boolean}) {
  const theme = useTheme(), window = useWindowDimensions();
  return <View style={{height: window.height, marginHorizontal: -theme.gutter, backgroundColor: video ? theme.colors.shadow : theme.colors.ink}}>
    {glow && <View pointerEvents="none" style={[StyleSheet.absoluteFill, {overflow: 'hidden'}]}><AmbientArt source={glow as never} treatment="music" style={StyleSheet.absoluteFill}/></View>}
    {children}
  </View>;
}

/** A show's episodes as the watch page and the player's Episodes panel and band draw them (tablet-play 2a, tv-player p1 frame 3). */
const episodes = [
  {title: f('episode1'), number: f('episodeNumber1'), aired: p('aired1'), status: f('episodeWatched'), length: f('episodeLength1'), watched: true, progress: undefined, playing: false, art: 'bd-tlou.jpg', id: 'episode1'},
  {title: f('episode2'), number: f('episodeNumber2'), aired: p('aired2'), status: f('episodePlaying'), length: f('episodeLeft2'), watched: false, progress: .26, playing: true, art: 'bd-tlou.jpg', id: 'episode2'},
  {title: f('episode3'), number: f('episodeNumber3'), aired: p('aired3'), status: f('episodeUpNext'), length: f('episodeLength3'), watched: false, progress: undefined, playing: false, art: undefined, id: 'episode3'},
  {title: p('episode4'), number: p('episodeNumber4'), aired: p('aired4'), status: undefined, length: p('episodeLength4'), watched: false, progress: undefined, playing: false, art: 'pool/tv/the-last-of-us--bd.jpg', id: 'episode4'},
] as const;
function EpisodeRows({panel = false}: {panel?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics(), window = useWindowDimensions(), upright = usePlayerUpright();
  // A panel's rows fill the panel: beside the picture it is the panel's width, under an upright picture the window's.
  const box = panel ? upright ? window.width : frameSpec.player.panel.width[frame.form] : frame.width;
  const width = box - 2 * (panel ? frameSpec.player.panel.padding[frame.form] : frame.gutter);
  // The watch page keeps Download on each row (phone-player 4, tablet 8); the player's own panel has none (tablet-play 2a).
  return <View style={{gap: theme.space.sm}}>{episodes.map(episode => <EpisodeTile key={episode.id} testID={`frames-episode-${episode.id}`} layout="row" width={width} artWidth={panel ? frameSpec.player.panel.still[frame.form] : frame.form === 'phone' ? undefined : frameSpec.player.panel.still.tv}
    title={episode.title} label={[{id: 'episode', text: episode.number, role: 'context'}]} status={episode.status} length={episode.length} synopsis={f('tlouSynopsis')}
    artwork={episode.art ? asset(episode.art) : undefined} watched={episode.watched} progress={episode.progress} playing={episode.playing} onNavigate={none} onDownload={theme.isTV || panel ? undefined : none}/>)}</View>;
}
function WatchPage({paused = false}: {paused?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics(), [tab, setTab] = useState('episodes'), [hide, setHide] = useState(false), wide = frame.form !== 'phone';
  const words = <View style={{flexShrink: 1, gap: theme.space.tight}}><Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text}]}>{f('tlou')}</Text>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{f('videoLead')}  {f('videoSubtitle')}</Text><HeroMetadata facts={fact(f('tlouFacts'))}/></View>;
  const actions = <View style={{flexDirection: 'row', gap: theme.spec.hero.actions.gap[theme.column]}}><Button icon={paused ? 'i-play' : 'i-pause'} align="stretch" style={wide ? undefined : {flex: 1}} onPress={none}>{f(paused ? 'play' : 'pause')}</Button>
    {([['i-plus', 'myList'], ['i-dl', 'download'], ['i-more', 'more']] as const).map(([icon, name]) => <Button key={name} variant="glass" iconOnly icon={icon} accessibilityLabel={f(name)} onPress={none}/>)}</View>;
  return <ScrollView contentContainerStyle={{paddingVertical: theme.space.sm, gap: theme.space.sm}}>
    <View style={[{marginHorizontal: frame.gutter, gap: theme.space.sm}, wide && {flexDirection: 'row', alignItems: 'flex-end', justifyContent: 'space-between'}]}>{words}{actions}</View>
    <Tabs items={[{value: 'episodes', label: f('episodes')}, {value: 'more', label: f('moreLikeThis')}, {value: 'details', label: f('details')}]} value={tab} onValueChange={setTab}/>
    <View style={{marginHorizontal: frame.gutter, gap: theme.space.sm}}><SeasonBar label={f('episodesSeason')} count={f('episodesCount')} onSeasons={none} hideWatched={hide} onHideWatched={setHide}/><EpisodeRows/></View>
  </ScrollView>;
}
/** Under an upright live picture: the channel's page (phone-player 1c and 1e, phone-app 11): what is on, its times
 * and what follows, Guide and Record, then what comes up on this channel. */
function LivePage() {
  const theme = useTheme(), frame = useFrameMetrics();
  return <ScrollView contentContainerStyle={{padding: frame.gutter, gap: theme.space.sm}}>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap}}><ChannelMark mark={{text: f('liveMark'), seed: 'synthetic:sse'} as never} size={theme.targets.touch}/>
      <Text style={[theme.type.rowLabel, {flex: 1, color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}]}>{f('liveChannelName')}  {f('liveNumber')}  <Text style={{color: theme.colors.record}}>{'●'} {f('liveNow')}</Text></Text></View>
    <Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text}]}>{f('liveTitle')}</Text>
    <Text style={[theme.type.intro, {color: theme.colors.textSecondary}]}>{f('liveThen')}</Text>
    <View style={{flexDirection: 'row', gap: theme.spec.hero.actions.gap[theme.column]}}><Button testID="frames-live-guide" variant="glass" icon="i-list" onPress={none}>{f('liveGuide')}</Button><Button testID="frames-live-record" variant="glass" icon="i-rec" iconColor={theme.colors.record} onPress={none}>{f('liveRecord')}</Button></View>
  </ScrollView>;
}

type PlayerState = 'playing' | 'paused' | 'buffering' | 'scrubbing' | 'missing' | 'recap' | 'undo';
type Opened = 'episodes' | 'subtitles' | undefined;
/** The video player filled with the kit's player parts, with what each mock-up puts in each place for its form
 * (tablet-play 1 and tv-player 1; phone-player 1 on its side; phone-player 4 and tablet-play 3 upright). Nothing moves between the states. */
function Player({state = 'playing', panel, panelWide, band, card, opened, id = `frames-player-${state}`, start}: {state?: PlayerState; panel?: ReactNode; panelWide?: boolean; band?: ReactNode; card?: ReactNode; /** Which button opened the panel showing. */ opened?: Opened; id?: string;
  /** TV: where the case starts (R1); Play or Pause unless a panel or card is open. */ start?: string}) {
  const {form} = useFrameMetrics(), phone = form === 'phone', tv = form === 'tv', upright = usePlayerUpright(), tablet = !phone && !tv && !upright, missing = state === 'missing', scrubbing = state === 'scrubbing';
  const still = <Artwork source={asset('bd-tlou.jpg')}/>;
  const tool = (icon: AudioControl['icon'], name: FrameName, disabled = false) => <PlayerControl key={name} testID={`${id}-${name}`} onPicture icon={icon} label={f(name)} disabled={disabled} onPress={none}/>;
  const bar = (icon: AudioControl['icon'], name: FrameName, extra: {disabled?: boolean; open?: boolean; spare?: boolean} = {}) => <PlayerControl key={name} testID={`${id}-${name}`} tool icon={icon} label={f(name)} onPress={none} {...extra}/>;
  const quality = <PlayerPill testID={`${id}-quality`} icon={tablet ? undefined : 'i-quality'} name={tv ? f('qualityShort') : undefined} qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none}/>;
  const subtitles = <PlayerPill testID={`${id}-subtitles`} icon="i-subs" name={f('subtitles')} value={f('english')} label={f('subtitles')} disabled={missing} open={opened === 'subtitles'} onPress={none}/>;
  const audio = <PlayerPill testID={`${id}-audio`} icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} disabled={missing} onPress={none}/>;
  const at = scrubbing ? .65 : .29;
  const prompt = state === 'playing' ? <PlayerPrompt testID={`${id}-skip`} label={f('skipIntro')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}}/>
    : state === 'recap' ? <PlayerPrompt testID={`${id}-skip`} label={p('skipRecap')} onPress={none} dismiss={{label: f('dismiss'), onPress: none}}/>
    : state === 'undo' ? <PlayerPrompt testID={`${id}-undo`} note={p('skippedIntro')} label={p('undo')} icon="i-undo" onPress={none}/> : undefined;
  return <Window video><PlayerChromeFrame testID={id} startFocus={start ?? `${id}-toggle`} chromeVisible={state !== 'undo'} scrubbing={scrubbing} panel={panel} panelWide={panelWide} band={band} card={card} picture={still} close={tool('i-down', 'close')}
    title={<PlayerTitle title={f('tlou')} lead={f('videoLead')} subtitle={state === 'paused' && opened === 'episodes' ? p('pausedAt') : f('videoSubtitle')}/>}
    tools={upright ? <>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{tool('i-subs', 'subtitles', missing)}{tool('i-sliders', 'playerSettings')}</>
      : tablet ? <>{tool('i-cast', 'playOn')}{tool('i-more', 'more')}{tool('i-x', 'dismiss')}</> : <>{tool('i-cast', 'playOn')}{tool('i-pip', 'pictureInPicture')}{tool('i-sliders', 'playerSettings')}</>}
    scrubber={<PlayerScrubBar value={at} held={scrubbing} buffered={[{start: at, end: at + .14}]} marks={missing ? [] : [{start: 0, end: .014}, {start: .02, end: .08}, {start: .95, end: 1}]}
      preview={scrubbing && !tv ? <PlayerScrubPreview at={at} time={f('previewTime')}>{still}</PlayerScrubPreview> : undefined}
      filmstrip={scrubbing && tv ? <PlayerFilmstrip frames={(['film1', 'film2', 'previewTime', 'film4', 'film5'] as const).map(name => ({time: f(name), picture: still}))}/> : undefined}
      labels={scrubbing && tv ? [{at: 0, text: f('markRecap')}, {at: .05, text: f('markIntro')}, {at: 1, text: f('markCredits'), end: true}] : undefined}/>}
    hints={<KeyHints hints={[{key: f('keyMove'), label: f('hintMove')}, {key: f('keyHold'), label: f('hintFaster')}, {key: f('keyOk'), label: f('hintPlayFrom')}, {key: f('keyBack'), label: f('hintReturn')}]}/>}
    times={{position: f(scrubbing ? 'previewTime' : 'elapsed'), remaining: f('remaining'), duration: f('duration'), endsAt: f('endsAt'), clock: f('clock')}}
    transport={<><PlayerControl testID={`${id}-previous`} icon="i-prev" label={f('previous')} disabled={missing} onPress={none}/><PlayerControl testID={`${id}-rewind`} icon="i-rew" skip={10} label={f('rewind')} onPress={none}/>
      <PlayerControl testID={`${id}-toggle`} primary icon={state === 'paused' ? 'i-play' : 'i-pause'} busy={state === 'buffering'} label={f(state === 'paused' ? 'play' : 'pause')} onPress={none}/>
      <PlayerControl testID={`${id}-forward`} icon="i-ffw" skip={30} label={f('forward')} onPress={none}/><PlayerControl testID={`${id}-next`} icon="i-next" label={f('next')} disabled={missing} onPress={none}/></>}
    leading={<PlayerPill testID={`${id}-episodes`} icon="i-stack" name={f('episodesButton')} label={f('episodesButton')} open={opened === 'episodes'} onPress={none}/>}
    trailing={upright ? <>{quality}{bar('i-full', 'fullScreen')}</>
      : tablet ? <>{bar('i-subs', 'subtitles', {disabled: missing, open: opened === 'subtitles'})}{quality}<PlayerPill testID={`${id}-speed`} spare value={f('speedShort')} label={f('optSpeed')} onPress={none}/>{bar('i-pip', 'pictureInPicture')}</>
      : tv ? <>{subtitles}{audio}{quality}{bar('i-sliders', 'playerOptions')}{bar('i-pip', 'pictureInPicture')}</> : <>{subtitles}{audio}{quality}</>}
    prompts={prompt}
    body={<WatchPage paused={state === 'paused'}/>}/></Window>;
}

/** The player's own panels, each open on the paused player where the frame places it for the form. A TV and a tablet
 * beside the picture open the lists in place under their headings (owner ruling 4, tv-player 4, tablet-play 2b); a
 * phone and the upright form drill in from Playback options (phone-player 3 and 3b). */
type PanelName = 'options' | 'subtitles' | 'audio' | 'quality' | 'speed';
function PlayerPanelCase({panel}: {panel: PanelName}) {
  const theme = useTheme(), {form} = useFrameMetrics(), upright = usePlayerUpright(), inPlace = form === 'tv' || (form !== 'phone' && !upright);
  const [forced, setForced] = useState(false), [open, setOpen] = useState<string>(panel === 'options' ? 'subtitles' : panel), id = `frames-player-${panel}`;
  const close = {label: f('optDone'), onPress: none}, back = {label: f('optBack'), onPress: none};
  const row = (label: FrameName, detail?: FrameName, selected = false, unavailable = false, trailing?: ReactNode) => <PlayerPanelRow key={label} testID={`${id}-${label}`} label={f(label)} detail={detail && f(detail)} selected={selected} unavailable={unavailable ? f('optUnavailable') : undefined} trailing={trailing} onPress={none}/>;
  const entry = (icon: AudioControl['icon'], label: FrameName, value: FrameName, qualifier?: FrameName) => <PlayerPanelRow key={label} testID={`${id}-${label}`} icon={icon} label={f(label)} value={f(value)} qualifier={qualifier && f(qualifier)} onPress={none}/>;
  const more = (icon: AudioControl['icon'], label: PlayerName, value?: PlayerName) => <PlayerPanelRow key={label} testID={`${id}-${label}`} icon={inPlace ? undefined : icon} label={p(label)} value={value && p(value)} onPress={none}/>;
  // Timing moves the subtitles earlier or later; Search for subtitles says why it can't search (desktop-player 3, tv-player 4).
  const timing = <PlayerPanelRow key="timing" testID={`${id}-timing`} label={p('timing')} trailing={<View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
    <Button variant="glass" size="xs" iconOnly icon="i-left" accessibilityLabel={p('earlier')} onPress={none}/><Text style={[theme.type.rowLabel, {color: theme.colors.text, fontVariant: ['tabular-nums']}]}>{p('timingValue')}</Text>
    <Button variant="glass" size="xs" iconOnly icon="i-right" accessibilityLabel={p('later')} onPress={none}/></View>}/>;
  const search = <PlayerPanelRow key="search" testID={`${id}-search`} icon="i-dl" label={p('searchSubtitles')} detail={p('searchSubtitlesReason')} onPress={none}/>;
  const lists = {
    subtitles: <>{row('optOff')}{row('english', 'optSrt', true)}{row('optEnglishSdh', 'optSrt')}{row('optSpanish', 'optSrt')}{row('optGerman', 'optNotAdded', false, false, <Button variant="glass" size="xs" icon="i-plus" onPress={none}>{f('optAdd')}</Button>)}</>,
    audio: <>{row('english', 'optAac51', true)}{row('optSpanish', 'optAacStereo')}{row('optCommentary', 'optAacStereo', false, true)}</>,
    quality: <>{row('optOriginal', 'optOriginalDetail')}{row('optAutomatic', 'optAutomaticDetail', true)}{row('opt720', 'opt720Detail')}{row('opt480', 'opt480Detail')}</>,
    speed: <>{row('optSpeed05')}{row('optSpeed075')}{row('optNormal', undefined, true)}{row('optSpeed125')}{row('optSpeed15')}{row('optSpeed2')}</>,
  };
  const heading = (name: keyof typeof lists, label: FrameName, value: FrameName) => ({label: f(label), value: f(value), open: open === name, onPress: () => setOpen(open === name ? '' : name), testID: `${id}-heading-${name}`});
  const notes = {audio: [f('optAudioNow')], quality: [f('optQualityNote'), p('qualityPlaying')]} as const;
  // In place: every list under its heading, the open one showing its rows; then Timing and Search, and More.
  const placed = <>
    <PlayerPanelGroup heading={heading('subtitles', 'subtitles', 'english')}>{lists.subtitles}{timing}{search}</PlayerPanelGroup>
    <PlayerPanelGroup heading={heading('audio', 'audio', 'english')} note={notes.audio}>{lists.audio}</PlayerPanelGroup>
    <PlayerPanelGroup heading={heading('quality', 'qualityShort', 'qualityValue')} note={notes.quality}>{lists.quality}</PlayerPanelGroup>
    <PlayerPanelGroup heading={heading('speed', 'optSpeed', 'optNormal')}>{lists.speed}</PlayerPanelGroup>
    <PlayerPanelGroup label={p('more')}>{more('i-subs', 'subtitleStyle', 'subtitleStyleValue')}{more('i-chapters', 'chapters')}{more('i-info', 'playbackInfo')}</PlayerPanelGroup>
  </>;
  const drill = {
    options: <><PlayerPanelGroup>{entry('i-subs', 'subtitles', 'english')}{entry('i-audio', 'audio', 'english')}{entry('i-quality', 'qualityShort', 'qualityValue', 'optAutomaticShort')}{entry('i-gauge', 'optSpeed', 'optNormal')}{entry('i-moon', 'optSleep', 'optOff')}</PlayerPanelGroup>
      <PlayerPanelGroup label={f('optThisTitle')}>{entry('i-queue', 'upNext', 'optUpNextCount')}{more('i-info', 'playbackInfo')}</PlayerPanelGroup></>,
    subtitles: <><PlayerPanelGroup>{lists.subtitles}</PlayerPanelGroup><PlayerPanelGroup>{row('optForced', 'optForcedHelp', false, false, <Switch checked={forced} onCheckedChange={setForced} accessibilityLabel={f('optForced')}/>)}{timing}</PlayerPanelGroup><PlayerPanelGroup>{search}</PlayerPanelGroup></>,
    audio: <PlayerPanelGroup note={notes.audio}>{lists.audio}</PlayerPanelGroup>,
    quality: <PlayerPanelGroup note={notes.quality}>{lists.quality}</PlayerPanelGroup>,
    speed: <PlayerPanelGroup>{lists.speed}</PlayerPanelGroup>,
  }[panel];
  const title = {options: 'optTitle', subtitles: 'subtitles', audio: 'audio', quality: 'qualityShort', speed: 'optSpeed'} as const;
  // R1: a list starts on its current choice; Playback options on its first heading.
  const start = {options: 'heading-subtitles', subtitles: 'english', audio: 'english', quality: 'optAutomatic', speed: 'optNormal'} as const;
  return <Player state="paused" id={id} start={`${id}-${start[panel]}`} opened={form === 'phone' || upright ? undefined : 'subtitles'} panel={<PlayerPanel testID={`${id}-panel`} title={inPlace ? f('optTitle') : f(title[panel])} note={panel === 'options' && !inPlace ? f('optNote') : undefined}
    back={panel === 'options' || inPlace ? undefined : back} close={close} footer={inPlace && form !== 'tv' ? f('optNote') : undefined}>{inPlace ? placed : drill}</PlayerPanel>}/>;
}
/** Episodes as each form draws it (owner ruling 5): a column beside the picture on a tablet (tablet-play 2a) and a side
 * sheet on a phone on its side; TV's is a band of tiles across the bottom (tv-player p1 frame 3); upright, Episodes is the
 * watch page's own tab under the picture (phone-player 4), so the case is that page. */
function PlayerEpisodesCase() {
  const [hide, setHide] = useState(false), {form} = useFrameMetrics(), upright = usePlayerUpright(), theme = useTheme();
  if (upright) return <Player state="paused" id="frames-player-episodes" opened="episodes"/>;
  const season = <Button testID="frames-player-episodes-season" variant="glass" size="small" trailingIcon="i-chdown" onPress={none}>{f('episodesSeason')}</Button>;
  if (form === 'tv') return <Player state="paused" id="frames-player-episodes" start="frames-episode-episode2" opened="episodes" band={<PlayerEpisodeBand title={f('episodesTitle')} count={f('episodesCount')} season={season}>
    {episodes.map(episode => <EpisodeTile key={episode.id} testID={`frames-episode-${episode.id}`} layout="tile" title={episode.title} status={episode.playing ? episode.status : undefined} length={episode.length}
      label={[{id: 'episode', text: episode.number, role: 'context'}, {id: 'aired', text: episode.watched ? episode.status : episode.playing ? f('episodeLeft2') : episode.aired, role: 'context'}]}
      artwork={episode.art ? asset(episode.art) : undefined} progress={episode.watched ? 1 : episode.progress} onNavigate={none}/>)}
  </PlayerEpisodeBand>}/>;
  return <Player state="paused" id="frames-player-episodes" start="frames-episode-episode2" opened="episodes" panel={<PlayerPanel testID="frames-player-episodes-panel" title={f('episodesTitle')} close={{label: f('optDone'), onPress: none}}
    footer={f('episodesNote')}>
    <SeasonBar label={f('episodesSeason')} count={f('episodesCount')} onSeasons={none} hideWatched={hide} onHideWatched={setHide}/>
    <View style={{height: theme.space.xs}}/><EpisodeRows panel/>
  </PlayerPanel>}/>;
}
/** Up next over the credits (tv-player 5 and 6, phone-player 6, 6b, 6c): the kit card in the player's corner, the controls hidden. */
function PlayerUpNextCase({variant}: {variant: 'plain' | 'credits' | 'countdown'}) {
  const {form} = useFrameMetrics();
  return <Player id={`frames-player-up-next${variant === 'plain' ? '' : `-${variant}`}`} start="watch-up-next-play" card={<PlayerUpNext compact={form !== 'tv'} item={{entryId: 'next', title: f('episode3'), durationMs: 76 * 60_000, episode: {seasonNumber: 1, number: 3}} as never} artwork={asset('bd-tlou.jpg')}
    countdown={variant === 'countdown' ? {seconds: 8, automatic: true} as never : {seconds: 42, automatic: false} as never} countdownTotal={variant === 'countdown' ? 10 : 60} sceneAfter={variant === 'credits'} onPlayNow={none} onWatchCredits={none} onSkipCredits={none}/>}/>;
}
/** Still watching? (phone-player 6e): after the episodes set in Settings, nothing starts until the viewer says so. */
const PlayerStillWatching = () => <Player id="frames-player-still-watching" state="paused" start="watch-still-confirm" card={<NativeStillWatchingCard item={{entryId: 'next', title: f('episode3'), episode: {seasonNumber: 1, number: 3}} as never} onConfirm={none} onReplay={none} onClose={none}/>}/>;

/** Live TV on the player frame, drawn with a channel's data (tv-player 11, tablet-play 8, phone-player 1c and 1f): the
 * channel's mark and the Live mark in the title block, Record and More at the top, Guide, channel down and up either
 * side of the transport, the live bar, and Go to live when behind or the quiet Live mark at the edge. */
function PlayerLiveCase({edge = false, guide = false}: {edge?: boolean; /** The short guide open. */ guide?: boolean}) {
  const {form} = useFrameMetrics(), tv = form === 'tv', phone = form === 'phone', upright = usePlayerUpright(), id = guide ? 'frames-player-live-guide' : edge ? 'frames-player-live' : 'frames-player-live-controls';
  const theme = useTheme(), still = <Artwork source={asset('bd-tlou.jpg')}/>, markSize = frameSpec.player.transport.control[form];
  const quality = <PlayerPill testID={`${id}-quality`} icon={phone ? 'i-quality' : undefined} name={tv ? f('qualityShort') : undefined} qualifier={f('qualityAuto')} value={f('qualityValue')} label={f('qualityShort')} onPress={none}/>;
  const bar = (icon: AudioControl['icon'], name: FrameName) => <PlayerControl key={name} testID={`${id}-${name}`} tool icon={icon} label={f(name)} onPress={none}/>;
  return <Window video><PlayerChromeFrame testID={id} startFocus={guide ? `${tv ? 'tv' : 'watch'}-mini-guide-live/sse/202` : `${id}-toggle`} chromeVisible panel={guide ? <LiveGuide/> : undefined} picture={still} close={<PlayerControl testID={`${id}-close`} onPicture icon="i-down" label={f('close')} onPress={none}/>}
    title={<PlayerTitle mark={<ChannelMark mark={{text: f('liveMark'), seed: 'synthetic:sse'} as never} size={markSize}/>} title={f('liveTitle')} lead={f('liveChannelName')} subtitle={f('liveNumber')} live={f('liveNow')} detail={f('liveThen')}/>}
    tools={<>{upright && <PlayerControl testID={`${id}-guide`} onPicture icon="i-list" label={f('guide')} onPress={none}/>}{!phone && !upright && !tv && <PlayerControl testID={`${id}-pip`} onPicture icon="i-pip" label={f('pictureInPicture')} onPress={none}/>}
      <PlayerLiveActions enabled compact={phone || upright} record={{label: f('liveRecord'), onPress: none}} more={{onPress: none}}/>{!upright && !tv && <PlayerControl testID={`${id}-dismiss`} onPicture icon="i-x" label={f('dismiss')} onPress={none}/>}</>}
    scrubber={<PlayerScrubBar value={edge ? .7 : .68} from={.55} buffered={[{start: .55, end: .7}]} liveEdge={.7}/>}
    times={{position: f('liveClock'), behind: edge ? undefined : f('liveBehind'), endsAt: f('liveEnds'), live: edge ? p('liveEdge') : undefined}}
    transport={<><PlayerChannelStep testID={`${id}-channel-down`} direction="down" number={f('liveDown')} label={f('liveChannelDown')} onPress={none}/><PlayerControl testID={`${id}-rewind`} icon="i-rew" skip={10} label={f('rewind')} onPress={none}/>
      <PlayerControl testID={`${id}-toggle`} primary icon="i-pause" label={f('pause')} onPress={none}/><PlayerControl testID={`${id}-forward`} icon="i-ffw" skip={30} label={f('forward')} disabled={edge} onPress={none}/>
      <PlayerChannelStep testID={`${id}-channel-up`} direction="up" number={f('liveUp')} label={f('liveChannelUp')} onPress={none}/></>}
    leading={<PlayerPill testID={`${id}-guide-button`} icon="i-list" name={f('liveGuide')} label={f('liveGuide')} onPress={none}/>}
    trailing={tv ? <><PlayerPill testID={`${id}-captions`} name={f('captions')} value={f('optOff')} label={f('captions')} onPress={none}/>{quality}{bar('i-sliders', 'playerOptions')}{bar('i-pip', 'pictureInPicture')}</>
      : phone ? <><PlayerPill testID={`${id}-subtitles`} icon="i-subs" name={f('subtitles')} value={f('optOff')} label={f('subtitles')} onPress={none}/><PlayerPill testID={`${id}-audio`} icon="i-audio" name={f('audio')} value={f('english')} label={f('audio')} onPress={none}/>{quality}</>
      : upright ? quality : <>{bar('i-subs', 'subtitles')}{quality}</>}
    prompts={edge ? undefined : <PlayerPill testID={`${id}-go-live`} icon="i-rec" tone="record" value={f('liveGoToLive')} label={f('liveGoToLive')} onPress={none}/>}
    body={<LivePage/>}/></Window>;
}

/** The end of a film the queue has nothing after (tv-player 7, phone-player 6d): the kit's end card on the film's own
 * backdrop. A phone draws its compact geometry. */
function PlayerEndCase() {
  const {form} = useFrameMetrics(), related = ([['spirited-away', 'spiritedAway', 2001], ['alien', 'alien', 1979], ['blade-runner', 'bladeRunner', 1982], ['dune', 'dune', 2021], ['barbie', 'barbie', 2023], ['aliens', 'aliens', 1986]] as const);
  return <Window video><PlayerEndCard title={p('insideOut')} year={2015} runtime={p('insideOutRuntime')} backdrop={asset('pool/movies/inside-out--bd.jpg')} compact={form === 'phone'}
    rating={{stars: 4, onRate: none}} onReplay={none} onClose={none}
    related={related.map(([id, name, year]) => ({id, title: p(name), year, poster: asset(`pool/movies/${id}.jpg`), onPress: none}))}/></Window>;
}

/** The short guide over live TV (tv-player 11, tablet-play 8, phone-player 1e): the kit's guide rows in the frame's
 * panel, which lies where the screen's guide does (a column beside the picture on a tablet, over the picture's right
 * side on a phone on its side and a TV, a sheet under an upright picture). */
const guideRows = [
  {id: 'live/sse/201', name: 'sports', number: 'guide201', mark: 'markSs', title: 'homecoming', next: 'cycling', at: 'at500', progress: .7},
  {id: 'live/sse/202', name: 'sportsExtra', number: 'guide202', mark: 'markSse', title: 'cycling', next: 'stage', at: 'at500', progress: .68, watching: true},
  {id: 'live/sse/301', name: 'movies', number: 'guide301', mark: 'markLm', title: 'glass', next: 'keeper', at: 'at615', progress: .35},
  {id: 'live/sse/302', name: 'classics', number: 'guide302', mark: 'markLc', title: 'keeper', next: 'glass', at: 'at740', progress: .12},
  {id: 'live/sse/401', name: 'kids', number: 'guide401', mark: 'markPk', title: 'story', next: 'story', at: 'at430', progress: .9},
] as const;
function LiveGuide() {
  const theme = useTheme(), {form} = useFrameMetrics(), upright = usePlayerUpright(), docked = form !== 'phone' && form !== 'tv' && !upright;
  const l = (name: keyof typeof liveNames) => formatMessage(livePlayerFixtureMessages[liveNames[name]]);
  return <View testID="frames-player-live-guide-panel" style={{flex: 1, padding: theme.isTV ? 18 : 0, backgroundColor: theme.colors.raised}}>
    <MiniGuideHead docked={docked} upright={upright} close={{label: f('dismiss'), onPress: none}}/>
    <ScrollView contentContainerStyle={{paddingTop: 4, paddingHorizontal: docked || theme.isTV ? 0 : 8, paddingBottom: 8}}>
      {guideRows.map(row => <MiniGuideRow key={row.id} id={row.id} channel={{name: l(row.name), number: p(row.number), mark: {text: p(row.mark), seed: `synthetic:${row.mark}`}, tuneAvailable: true}}
        title={l(row.title)} progress={row.progress} next={{title: l(row.next), at: p(row.at)}} watching={'watching' in row} docked={docked} disabled={false} onPress={none}/>)}
      <MiniGuideAll docked={docked} disabled={false} onPress={none}/>
    </ScrollView>
  </View>;
}
const liveNames = {sports: 'next.live.fixture.sports', sportsExtra: 'next.live.fixture.sportsExtra', movies: 'next.live.fixture.movies', classics: 'next.live.fixture.classics', kids: 'next.live.fixture.kids',
  homecoming: 'next.live.fixture.homecoming', cycling: 'next.live.fixture.cycling', stage: 'next.live.fixture.stage', glass: 'next.live.fixture.glass', keeper: 'next.live.fixture.keeper', story: 'next.live.fixture.story'} as const;

/** Playback info (playback-info 1, 3, 4, 15): opened from Playback options' More, in the frame's panel over the paused
 * player; the kit's comparison draws the route, the reason and each section. */
function PlayerInfoCase() {
  const row = (field: PlaybackInfoField, source?: PlayerName, delivered?: PlayerName, changed = false) => ({field, source: source && p(source), delivered: delivered && p(delivered), changed});
  const comparison: PlaybackComparisonProjection = {route: 'converted', explanation: p('infoReason'), networkAndBitrate: p('infoNetwork'), sections: [
    {kind: 'picture', route: 'converted', losses: [p('infoVisionLost')], rows: [row('container', 'infoMkv', 'infoMp4', true), row('codec', 'infoHevc', 'infoHevc'), row('hdr', 'infoVision', 'infoHdr10', true),
      row('bitDepth', 'info10bit', 'info10bit'), row('resolution', 'info4k', 'info4k'), row('frameRate', 'infoFps', 'infoFps')]},
    {kind: 'sound', route: 'converted', losses: [p('infoAtmosLost')], rows: [row('format', 'infoDdp', 'infoAac', true), row('channels', 'info51', 'info51'), row('sampleRate', 'info48', 'info48'), row('output', undefined, 'infoDecoded')]},
    {kind: 'subtitles', route: 'original', losses: [], rows: [row('track', 'infoSrt', 'infoSrt'), row('shown', undefined, 'infoDrawn')]},
  ]};
  return <Player state="paused" id="frames-player-info" start="watch-info-picture" panelWide panel={<PlayerPanel testID="frames-player-info-panel" title={p('playbackInfo')} close={{label: f('optDone'), onPress: none}}>
    <PlaybackInfoComparison comparison={comparison} title={p('infoTitle')}/></PlayerPanel>}/>;
}

/** The player minimised (phone-player 5, 5c; tablet-play 4) and playing on another device (tablet-play 13, phone-player
 * 7): the kit dock's own bar over a page, drawn by the dock family's case with the reference artwork. */
const dockArt = {video: asset('pool/tv/slow-horses--bd.jpg'), music: asset('pool/albums/after-hours.jpg'), tvMusic: asset('pool/albums/after-hours.jpg'), book: asset('pool/books/the-martian-unabridged.jpg')};

/** The audio player (frame 8) with its full regions on every form: desktop-listen "now" and "book-player", tablet-play
 * 9, 10 and 12, phone-app 14 and 15, final3-tv-12. `panel` is the queue (or an audiobook's chapters, or Lyrics). */
type AudioVariant = 'music' | 'missing' | 'book' | 'loading' | 'lyrics' | 'queue';
function Audio({variant}: {variant: AudioVariant}) {
  const theme = useTheme(), frame = useFrameMetrics(), tv = frame.form === 'tv', phone = frame.form === 'phone', id = `frames-audio-${variant}`;
  const control = (icon: AudioControl['icon'], label: string, extra: Partial<AudioControl> = {}): AudioControl => ({icon, label, onPress: none, testID: `${id}-${label}`, ...extra});
  const book = variant === 'book', missing = variant === 'missing', loading = variant === 'loading';
  const [tab, setTab] = useState(variant === 'lyrics' ? 'lyrics' : book ? 'chapters' : 'queue');
  // The device this is playing on is named for what it is.
  const device = control(Platform.OS === 'ios' ? 'i-airplay' : 'i-cast', f(Platform.OS === 'ios' ? phone ? 'thisPhone' : 'thisTablet' : phone ? 'thisAndroid' : 'thisAndroidTablet'));
  const music = [control('i-shuffle', f('shuffle')), control('i-prev', f('previous')), control('i-pause', f('pause')), control('i-next', f('next'), {disabled: missing}), control('i-repeat', f('repeat'), {active: true})] as const;
  const reading = [control('i-prev', f('previous')), control('i-rew', p('back15'), {skip: 15}), control('i-pause', f('pause')), control('i-ffw', f('forward'), {skip: 30}), control('i-next', f('next'))] as const;
  const touch = !tv, more = (name: string) => touch ? {label: f('more'), onPress: none, testID: `${id}-more-${name}`} : undefined;
  const songs = [['added1', 'pool/albums/rumours.jpg'], ['added2', 'pool/albums/hit-me-hard-and-soft.jpg'], ['added3', 'pool/albums/abbey-road-2019-mix.jpg'], ['added4', 'pool/albums/random-access-memories.jpg'], ['added5', 'pool/albums/21.jpg']] as const;
  const queue = <>
    {!tv && <QueueGroup label={f('queueNow')}/>}
    {!tv && <QueueRow testID={`${id}-now`} lead="playing" artwork={asset('pool/albums/after-hours.jpg')} title={f('songTitle')} subtitle={f('songArtist')} duration={p('songLength')} more={more('now')} onPress={none}/>}
    <QueueGroup label={f('queueNextFrom')}/>
    <QueueRow testID={`${id}-next`} lead="handle" artwork={asset('pool/albums/after-hours.jpg')} title={f('queue1')} subtitle={f('songArtist')} duration={f('queueDuration1')} more={more('next')} onPress={none}/>
    <QueueGroup label={p('addedByYou')} hint={tv ? undefined : p('reorderTouch')}/>
    {songs.map(([name, art]) => <QueueRow key={name} testID={`${id}-${name}`} lead="handle" artwork={asset(art)} title={p(name)} subtitle={p(`${name}Artist`)} duration={p(`${name}Length`)} more={more(name)} onPress={none}/>)}
  </>;
  const chapterRows = <>
    <QueueGroup label={p('chaptersCount')} hint={p('chapterOfTotal')}/>
    {(['chapter11', 'chapter12', 'chapter13', 'chapter14'] as const).map(name => <QueueRow key={name} testID={`${id}-${name}`} lead="done" title={p(name)} duration={p(`${name}Length`)} onPress={none}/>)}
    <QueueRow testID={`${id}-chapter15`} lead="playing" title={p('bookChapter')} progress={.38} duration={p('chapterLeft')} onPress={none}/>
    {(['chapter16', 'chapter17', 'chapter18'] as const).map((name, index) => <QueueRow key={name} testID={`${id}-${name}`} lead={16 + index} title={p(name)} duration={p(`${name}Length`)} onPress={none}/>)}
  </>;
  const lyrics = <View style={{padding: theme.space.md, alignItems: 'center', gap: theme.space.sm}}>
    <Icon name="i-lyrics" size={theme.targets.touch - theme.space.xs} color={theme.colors.textTertiary}/>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily, textAlign: 'center'}]}>{p('lyricsNone')}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, textAlign: 'center'}]}>{p('lyricsNoneBody')}</Text>
    <View style={{alignSelf: 'stretch', flexDirection: 'row', alignItems: 'center', gap: theme.space.sm, padding: theme.space.sm, borderRadius: frameSpec.card.radius.phone, backgroundColor: theme.colors.raisedHigh}}>
      <Text style={[theme.type.rowHelp, {flex: 1, color: theme.colors.textSecondary}]}><Text style={{color: theme.colors.text, fontFamily: theme.type.groupLabel.fontFamily}}>{p('lyricsOwner')}</Text>{'\n'}{p('lyricsOwnerBody')}</Text>
      <Button variant="glass" size="xs" onPress={none}>{p('librarySettings')}</Button></View>
  </View>;
  const tabs = book ? {items: [{value: 'chapters', label: f('chapters'), icon: 'i-chapters' as const}, {value: 'queue', label: f('upNext'), icon: 'i-queue' as const}], value: tab, onValueChange: setTab}
    : {items: [{value: 'queue', label: f('upNext'), icon: 'i-queue' as const}, {value: 'lyrics', label: f('lyrics'), icon: 'i-lyrics' as const}], value: tab, onValueChange: setTab};
  const footer = book ? {text: p('chaptersHint')} : {text: p('queueCount'), action: {label: p('saveAsPlaylist'), icon: 'i-list-plus' as const, onPress: none, testID: `${id}-save`}};
  const body = tab === 'lyrics' ? lyrics : book && tab === 'chapters' ? chapterRows : queue;
  // A phone shows the queue as a sheet only while it is open (its Up next button); the wide forms always show the panel; nothing queued leaves the panel out.
  const panel = missing || (phone && variant !== 'queue' && variant !== 'lyrics') ? undefined
    : <AudioPanel testID={`${id}-panel`} tabs={tv ? undefined : tabs} heading={tv ? {title: f('upNext'), detail: p('queueCountTv')} : undefined} footer={tv || tab === 'lyrics' ? undefined : footer}>{body}</AudioPanel>;
  const choices = (labels: readonly string[], chosen: number) => labels.map((label, index) => ({label, selected: index === chosen, onPress: none}));
  const art = missing ? undefined : asset(book ? 'pool/books/the-martian-unabridged.jpg' : 'pool/albums/after-hours.jpg');
  return <Window glow={loading ? undefined : art}><AudioPlayerFrame testID={id} startFocus={`${id}-${f('pause')}`} loading={loading}
    close={control('i-down', f('close'))} more={control('i-more', f('more'))} favourite={control('i-heart', f('favourite'))} titleMore={control('i-more', p('more'))} device={tv ? undefined : device}
    context={phone && !book ? f('playingFrom') : f('playingFromListening')} source={book ? p('audiobooks') : missing ? f('playingSourceField') : f('playingSource')} notice={book ? p('wentBack') : undefined}
    artwork={art} icon={book ? 'i-book' : 'i-music'} title={book ? p('martian') : missing ? f('longSong') : f('songTitle')} badge={book ? p('unabridged') : undefined}
    byline={book ? [p('weir'), p('readBy')] : missing ? [f('choir')] : [f('songArtist'), tv ? `${f('playingSource')}   ${p('songYear')}` : f('playingSource')]}
    chapter={book ? {label: p('bookChapter'), of: phone ? f('chapterOf') : p('bookChapterOf')} : undefined}
    scrubber={<PlayerScrubBar value={book ? .58 : .51} tone={tv ? 'white' : 'accent'} knob={phone} marks={book ? [] : []}/>}
    times={book ? phone ? {position: f('bookElapsed'), middle: f('timeLeft'), end: f('bookRemaining')} : {position: p('bookPosition'), end: p('bookLeft')} : {position: f('audioElapsed'), end: phone ? f('audioRemaining') : p('songLength')}}
    transport={book ? reading : music} next={book || missing ? undefined : {label: f('nextLabel'), title: f('nextTitle')}}
    lyrics={book ? undefined : control('i-lyrics', f('lyrics'), {disabled: missing})} queue={book ? undefined : control('i-queue', f('upNext'), {disabled: missing})} playlist={tv && !book ? control('i-listplus', f('addToPlaylist')) : undefined}
    speed={book ? {label: f('speed'), value: f('speedValue'), help: p('speedHelp'), onPress: none, testID: `${id}-speed`, choices: choices([f('optSpeed05'), f('optSpeed075'), p('speed1'), f('optSpeed125'), f('optSpeed15'), p('speed175'), f('optSpeed2'), p('speed25'), p('speed3')], 3)} : undefined}
    sleep={book ? {label: f('sleep'), value: f('sleepValue'), icon: 'i-moon', help: p('sleepHelp'), active: true, onPress: none, testID: `${id}-sleep`, choices: choices([f('optOff'), p('sleep15'), p('sleep30'), p('sleep45'), p('sleep60'), p('sleep90'), p('sleepEnd')], 2)} : undefined}
    chapters={book ? {label: f('chapters'), value: f('chaptersValue'), icon: 'i-list', onPress: none, testID: `${id}-chapters`} : undefined}
    panel={panel}/></Window>;
}

const whole = {screen: true} as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-player', title: 'next.platform.frame.player', render: () => <Player/>, ...whole},
  ...(['paused', 'buffering', 'scrubbing', 'missing'] as const).map(state => ({id: `frames-player-${state}`, title: 'next.platform.frame.player' as const, render: () => <Player state={state}/>, ...whole})),
  {id: 'frames-player-skip', title: 'next.platform.frame.playerSkip', render: () => <Player state="recap" id="frames-player-skip"/>, ...whole},
  {id: 'frames-player-undo', title: 'next.platform.frame.playerSkip', render: () => <Player state="undo" id="frames-player-undo"/>, ...whole},
  ...(['options', 'subtitles', 'audio', 'quality', 'speed'] as const).map(panel => ({id: `frames-player-${panel}`, title: `next.platform.frame.player${panel[0]!.toUpperCase()}${panel.slice(1)}` as 'next.platform.frame.playerOptions', render: () => <PlayerPanelCase panel={panel}/>, ...whole})),
  {id: 'frames-player-episodes', title: 'next.platform.frame.playerEpisodes', render: PlayerEpisodesCase, ...whole},
  {id: 'frames-player-up-next', title: 'next.platform.frame.playerUpNext', render: () => <PlayerUpNextCase variant="plain"/>, ...whole},
  {id: 'frames-player-up-next-credits', title: 'next.platform.frame.playerUpNextCredits', render: () => <PlayerUpNextCase variant="credits"/>, ...whole},
  {id: 'frames-player-up-next-countdown', title: 'next.platform.frame.playerUpNextCountdown', render: () => <PlayerUpNextCase variant="countdown"/>, ...whole},
  {id: 'frames-player-still-watching', title: 'next.platform.frame.playerStillWatching', render: PlayerStillWatching, ...whole},
  {id: 'frames-player-live', title: 'next.platform.frame.playerLive', render: () => <PlayerLiveCase edge/>, ...whole},
  {id: 'frames-player-live-controls', title: 'next.platform.frame.playerLiveControls', render: () => <PlayerLiveCase/>, ...whole},
  {id: 'frames-player-live-guide', title: 'next.platform.frame.playerLiveGuide', render: () => <PlayerLiveCase guide/>, ...whole},
  {id: 'frames-player-end-card', title: 'next.platform.frame.playerEndCard', render: PlayerEndCase, ...whole},
  {id: 'frames-player-info', title: 'next.platform.frame.playerInfo', render: PlayerInfoCase, ...whole},
  ...([['frames-player-dock', 'video'], ['frames-player-dock-music', 'music'], ['frames-player-casting', 'playing'], ['frames-player-casting-connecting', 'connecting']] as const).map(([id, kind]) =>
    ({id, title: 'next.platform.frame.playerDock' as const, platforms: ['phone', 'tablet'] as const, render: () => <DockCase kind={kind} artwork={dockArt}/>, ...whole})),
  {id: 'frames-player-dock-tv', title: 'next.platform.frame.playerDock', platforms: ['tv'], render: () => <DockCase kind="tv-music" artwork={dockArt}/>, ...whole},
  {id: 'frames-audio', title: 'next.platform.frame.audio', render: () => <Audio variant="music"/>, ...whole},
  {id: 'frames-audio-lyrics', title: 'next.platform.frame.audioLyrics', render: () => <Audio variant="lyrics"/>, platforms: ['phone', 'tablet', 'web'], ...whole},
  {id: 'frames-audio-queue', title: 'next.platform.frame.audioQueue', render: () => <Audio variant="queue"/>, platforms: ['phone'], ...whole},
  {id: 'frames-audio-missing', title: 'next.platform.frame.audioMissing', render: () => <Audio variant="missing"/>, ...whole},
  {id: 'frames-audio-book', title: 'next.platform.frame.audioBook', render: () => <Audio variant="book"/>, ...whole},
  {id: 'frames-audio-loading', title: 'next.platform.frame.audioLoading', render: () => <Audio variant="loading"/>, ...whole},
];
export default galleries;
