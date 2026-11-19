import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {ArtworkGlow, Artwork} from '../../../kit/artwork';
import {Menu, Dialog, Banner} from '../../../kit/overlays';
import {router} from '../../../router';
import {ListeningNotice, SignalLine, SignalPath} from '../../../kit/media';
import {dispatchWatch, watchSeek, watchSeekBounds, type WatchIntent} from '../model';
import {ListeningPanels,ListeningTabs} from '../listening/ListeningPanels';
import type {ListeningHost} from '../listening/use-listening-owner';
import {Button} from '../../../kit/controls';
import {AudioPlayerFrame, type AudioControl} from '../../../kit/frames';
import {PlayerVolume} from '../../../kit/player';
import {StateView} from '../../../kit/states';
import {watchMusicSignal} from './signal';
import {readWatchMusicAudioFacts} from './audio-facts';
import {fenceKey} from '../../../shell/playback-model';
import {WatchEqualizerPanel, hostAdmit, surfaceAdmit, type WatchHostFacts, type WatchReceipt, type WatchSurfaceReceipt} from './EqualizerPanel';
import type {WatchScreenProps} from '../types';
import type {WatchPlaybackContext} from '../play-on-binding';
import type {V1QueuePolicyIntent} from '@core/playback-v1/v1-queue-player.ts';
import {ListeningSeek} from '../listening/ListeningSeek';
import {musicQueueNext, musicQueueOrigin} from './queue-facts';
import {useMusicFavorite} from './use-music-favorite';
import styles from './now-playing.module.css';
import type {MusicExpansion} from './expansion-owner';

const t = defaultI18n.t;
const noSubscribe = () => () => {};
const noEqualizer = () => null;

/** The drawn expanded Now playing host for music (listening-notices frames 2 and 3).
 * It is the same authority-local runtime and the same retained media owner; this view
 * only presents facts, and the actionable DockNotice stays with the dock. */
export function WatchMusicNowPlaying({binding, snapshot, expanded, onCollapse, phone, narrow = false, motion, isCurrent,listening,playOn,context}: Pick<WatchScreenProps, 'binding' | 'snapshot' | 'expanded' | 'onCollapse'> & {phone: boolean; narrow?: boolean; motion?: MusicExpansion; isCurrent?: () => boolean;listening?:ListeningHost;playOn?:(anchor?:HTMLButtonElement)=>void;context?:WatchPlaybackContext}) {
  const presentation = snapshot.presentation;
  const environment = binding.environment;
  const equalizer = binding.runtime.equalizerPort;
  const settings = useSyncExternalStore(equalizer?.subscribe ?? noSubscribe, equalizer?.getSnapshot ?? noEqualizer, noEqualizer);
  useEffect(() => { if (!equalizer) return; return equalizer.start(); }, [equalizer]);
  const [signalOpen, setSignalOpen] = useState(false);
  const [eqOpen, setEqOpen] = useState(false), [moreOpen, setMoreOpen] = useState(false);
  const signalAnchor = useRef<HTMLButtonElement>(null), moreAnchor = useRef<HTMLButtonElement>(null);
  const signal = watchMusicSignal(snapshot, settings, readWatchMusicAudioFacts(binding.runtime, snapshot));
  const identity = presentation ? playbackIdentity(presentation.subject, 'full', environment) : undefined;
  const artworkSource = presentation ? binding.artwork.foreground(presentation.posterUrl ?? presentation.backdropUrl, 560, 1, {group: 'playback', priority: 'high'}) : undefined;
  const open = expanded && presentation?.kind === 'music' && !!identity && (!isCurrent || isCurrent()) && (!motion || !!motion.scope);
  const favorite = useMusicFavorite(context, binding.runtime, snapshot, open, binding.reportError, isCurrent);
  useLayoutEffect(() => {if (!open) {setSignalOpen(false); setEqOpen(false); setMoreOpen(false);}}, [open]);
  // Esc minimises, as it does for video and audiobooks (shortcuts list); open panels and fields keep their own Esc.
  const collapse = useRef(onCollapse); collapse.current = onCollapse;
  useEffect(() => {
    if (!open || signalOpen || eqOpen || moreOpen || typeof document === 'undefined') return;
    const key = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || (event.target as Element | null)?.closest?.('[role="dialog"],[role="menu"],input,textarea,select,[contenteditable="true"]')) return;
      event.preventDefault(); collapse.current?.();
    };
    document.addEventListener('keydown', key);
    return () => document.removeEventListener('keydown', key);
  }, [open, signalOpen, eqOpen, moreOpen]);
  // Immutable rendered facts. A receipt is minted fresh for each layout setup/replay.
  const [hostReceipt, setHostReceipt] = useState<WatchReceipt | null>(null);
  const publishedHost = useRef<WatchReceipt | null>(null);
  const [moreReceipt, setMoreReceipt] = useState<WatchSurfaceReceipt | null>(null), publishedMore = useRef<WatchSurfaceReceipt | null>(null);
  const [eqReceipt, setEqReceipt] = useState<WatchSurfaceReceipt | null>(null), publishedEq = useRef<WatchSurfaceReceipt | null>(null);
  const fenceAt = snapshot?.fence ? fenceKey(snapshot.fence) : '', presentationAt = snapshot?.presentation?.fence ? fenceKey(snapshot.presentation.fence) : '';
  const presentationKind = snapshot?.presentation?.kind, authority = snapshot?.authority, playbackRun = snapshot?.playback.playbackRun;
  const scope = motion?.scope,listenOwner=listening?.owner,listenState=listening?.state;
  useLayoutEffect(()=>{if(eqOpen&&listenOwner&&listenState)listenOwner.cover(listenState);},[eqOpen,listenOwner,listenState]);
  const facts = useMemo<WatchHostFacts | null>(() => !open || !equalizer || presentationKind !== 'music' || !fenceAt || !presentationAt || fenceAt !== presentationAt
    ? null : {runtime: binding.runtime, port: equalizer, authority, fenceAt, presentationAt, run: playbackRun, ...(isCurrent ? {isCurrent} : {}), ...(scope ? {scope} : {})},
    [open, equalizer, presentationKind, fenceAt, presentationAt, binding.runtime, authority, playbackRun, isCurrent, scope]);
  useLayoutEffect(() => {
    if (!facts) {
      const old = publishedHost.current; publishedHost.current = null; if (old) old.live = false;
      setHostReceipt(null); setMoreReceipt(null); setEqReceipt(null); setEqOpen(false); setMoreOpen(false); return;
    }
    const receipt: WatchReceipt = {facts, live: true};
    publishedHost.current = receipt;
    setHostReceipt(receipt); setMoreReceipt(null); setEqReceipt(null); setEqOpen(false); setMoreOpen(false);
    return () => {receipt.live = false; if (publishedHost.current === receipt) publishedHost.current = null;};
  }, [facts]);
  useLayoutEffect(() => {
    if (!moreOpen || !hostReceipt) {const old = publishedMore.current; publishedMore.current = null; if (old) old.live = false; setMoreReceipt(null); return;}
    const receipt: WatchSurfaceReceipt = {host: hostReceipt, live: true};
    publishedMore.current = receipt; setMoreReceipt(receipt);
    return () => {receipt.live = false; if (publishedMore.current === receipt) publishedMore.current = null;};
  }, [moreOpen, hostReceipt]);
  useLayoutEffect(() => {
    if (!eqOpen || !hostReceipt) {const old = publishedEq.current; publishedEq.current = null; if (old) old.live = false; setEqReceipt(null); return;}
    const receipt: WatchSurfaceReceipt = {host: hostReceipt, live: true};
    publishedEq.current = receipt; setEqReceipt(receipt);
    return () => {receipt.live = false; if (publishedEq.current === receipt) publishedEq.current = null;};
  }, [eqOpen, hostReceipt,phone]);
  // A surface retires synchronously before its state changes, so a saved callback cannot act in
  // the same tick as the close/selection that replaced it.
  const retireMore = (receipt: WatchSurfaceReceipt | null) => {
    if (!receipt || publishedMore.current !== receipt) return false;
    receipt.live = false; publishedMore.current = null; setMoreReceipt(null); return true;
  };
  const closeEq = (receipt: WatchSurfaceReceipt | null) => {
    if (!surfaceAdmit(receipt,publishedEq,publishedHost,hostReceipt,facts)) return;
    receipt!.live = false; publishedEq.current = null; setEqReceipt(null); setEqOpen(false);
    if(listenOwner&&listenState)listenOwner.show(listenState,'queue');
  };
  const present = motion ? motion.present : open;
  if (!present || !presentation || !identity || isCurrent && !isCurrent()) return null;
  const send = (intent: WatchIntent) => { if (!open || motion?.scope && !motion.scope.current()) return;void dispatchWatch(binding.runtime, snapshot, intent).catch(binding.reportError); };
  const bounds = watchSeekBounds(snapshot), at = presentation.positionSeconds, duration = presentation.durationSeconds;
  const can = (kind: string) => presentation.controls.includes(kind as never);
  const canSeek = can('seek') && !!bounds;
  const from = musicQueueOrigin(snapshot), nextTitle = musicQueueNext(snapshot);
  const code = snapshot.playback.listeningNotice;
  const notice = code ? {code, sequence: snapshot.sequence} : undefined;
  const listeningNotice = <ListeningNotice notice={notice} paused={snapshot.playback.intent === 'paused'}
    stopped={snapshot.disposed || snapshot.playback.phase === 'idle' || snapshot.playback.phase === 'ended'}
    errorShown={snapshot.playback.phase === 'error' || !!snapshot.playback.error || !!snapshot.problem}
    expanded layout={phone ? 'phone' : 'desktop'} translate={t}/>;
  const queue = snapshot.queueView?.queue, list = listenState?.queue;
  const canPolicy = !!listenOwner?.queueOffered() && !!list?.current && !!queue && list.queueId === queue.id && list.revision === queue.revision;
  const policyBusy = !canPolicy || listenState?.policyBusy || list?.change === 'sending';
  const policy = (intent: V1QueuePolicyIntent) => {
    if (!open || !listenOwner || !listenState || !list || policyBusy || motion?.scope && !motion.scope.current()) return;
    void listenOwner.policy(listenState, list, intent, false).catch(error => {if (listenOwner.canReport(listenState)) binding.reportError(error);});
  };
  const favoriteAction = (retry = false) => {
    if (!open || !favorite || motion?.scope && !motion.scope.current()) return;
    void (retry ? favorite.owner.retry(favorite.state) : favorite.owner.toggle(favorite.state)).catch(error => {if (favorite.owner.current()) binding.reportError(error);});
  };
  const seek = (seconds: number) => { const value = watchSeek(snapshot, seconds); if (value !== null) send({kind: 'seek', seconds: value}); };
  const eqPanel=eqOpen&&equalizer&&facts&&hostReceipt&&eqReceipt?<WatchEqualizerPanel receipt={eqReceipt} rendered={eqReceipt} published={publishedEq}
    host={facts} hostReceipt={hostReceipt} publishedHost={publishedHost}
    runtime={binding.runtime} port={equalizer} {...(isCurrent?{isCurrent}:{})} {...(scope?{scope}:{})}
    close={()=>closeEq(eqReceipt)} openSettings={()=>{if(!open)return;void router.navigate({to:'/settings',search:()=>({scope:'account' as const,section:'music'})});}}/>:null;
  const panel=eqOpen?eqPanel:listening?<><ListeningTabs host={listening}/><ListeningPanels host={listening} snapshot={snapshot} reportError={binding.reportError} artwork={binding.artwork} policyInTransport/></>:<StateView title={t('player.queueUnavailable')}/>;
  const glowSource = binding.artwork.ambient(presentation.posterUrl ?? presentation.backdropUrl);
  // The audio frame's own forms: one column under 600 wide, the column beside the panel from 600 (R7). The host hands
  // that rule as `narrow`; the foot's Lyrics and Up next, the next line and the sheet follow it, not the video's 1024.
  const control = (icon: AudioControl['icon'], label: string, onPress: (button: HTMLButtonElement) => void, extra: Partial<AudioControl> = {}): AudioControl => ({icon, label, onPress, ...extra});
  const openMore = () => {
    if (moreOpen) {
      if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts) || !retireMore(moreReceipt)) return;
      setMoreOpen(false); return;
    }
    if (hostAdmit(hostReceipt, publishedHost, facts)) setMoreOpen(true);
  };
  const favouriteBanner = favorite?.failed ? <Banner action={{label: t('next.states.tryAgain'), onAction: () => favoriteAction(true)}}>{t('next.states.pageFailedBody')}</Banner> : null;
  const repeat = queue?.repeat ?? 'off';
  // The frame (AudioPlayerFrame) places every region; this screen fills them with its own parts and keeps its behaviour.
  return <section ref={motion?.scope ? motion.frame : undefined} className={`portico-cover ${styles.frame}`} data-cover={glowSource ? 'audio' : undefined} data-music-motion-owned={!!motion?.scope || undefined} inert={!open || undefined} aria-hidden={!open || undefined} aria-label={t('next.music.nowPlaying')} tabIndex={-1} data-watch-music data-phone={phone || undefined}>
    <ArtworkGlow source={glowSource} phone={phone}/>
    <AudioPlayerFrame
      close={control('i-down', t('player.minimize'), () => onCollapse())}
      context={from ? t('web.player.playingFromLabel') : undefined} source={from ?? undefined}
      device={playOn && listenOwner && listenState ? control('i-cast', t('device.thisDevice'), button => {if (open) listenOwner.external(listenState, snapshot, () => playOn(button));}) : undefined}
      more={equalizer ? control('i-more', t('player.more'), button => {moreAnchor.current = button; openMore();}, {active: moreOpen}) : undefined}
      notice={narrow ? <>{favouriteBanner}{listeningNotice}</> : listeningNotice}
      artwork={artworkSource} icon="i-music" title={identity.title} byline={identity.subtitle.map(part => part.text)}
      cover={<div ref={motion?.scope ? motion.cover : undefined} className={styles.cover}>{!motion?.scope && <Artwork source={artworkSource} alt={identity.title} />}</div>}
      favourite={favorite?.offered ? control('i-heart', t(favorite.selected ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), () => favoriteAction(), {active: favorite.selected, disabled: favorite.busy || favorite.failed}) : undefined}
      signal={signal ? <div className={styles.signal}><SignalLine model={signal} translate={t} layout={phone ? 'phone' : 'desktop'}
        ref={signalAnchor} aria-haspopup="dialog" aria-expanded={signalOpen} onClick={() => setSignalOpen(true)} />
        <SignalPath model={signal} translate={t} locale={environment.locale} layout={phone ? 'phone' : 'desktop'}
          open={open && signalOpen} onOpenChange={setSignalOpen} anchorRef={signalAnchor} /></div> : undefined}
      scrubber={<ListeningSeek className={styles.scrub} position={at} duration={duration} bounds={bounds} disabled={!canSeek} phone={phone} onSeek={seek}/>}
      transport={[
        // Radio has no shuffle: it keeps its place, disabled (the frame's rule).
        control('i-shuffle', t(queue?.shuffled ? 'player.opt.shuffleOn' : 'player.opt.shuffleOff'), () => policy({action: 'shuffle', shuffled: !queue?.shuffled}), {active: queue?.shuffled === true, disabled: policyBusy || !!queue?.radio}),
        control('i-prev', t('next.dock.previous'), () => send({kind: 'previous'}), {disabled: !can('previous')}),
        control(presentation.playing ? 'i-pause' : 'i-play', presentation.playing ? t('next.dock.pause') : t('next.dock.play'), () => send({kind: presentation.playing ? 'pause' : 'play'}), {disabled: !can(presentation.playing ? 'pause' : 'play')}),
        control('i-next', t('next.dock.next'), () => send({kind: 'next'}), {disabled: !can('next')}),
        control('i-repeat', t(repeat === 'one' ? 'player.opt.repeatOne' : repeat === 'all' ? 'player.opt.repeatAll' : 'player.opt.repeatOff'),
          () => policy({action: 'repeat', repeat: repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'}), {active: !!queue && repeat !== 'off', disabled: policyBusy, mark: repeat === 'one' ? '1' : undefined}),
      ]}
      volume={!narrow && can('volume') ? <PlayerVolume value={presentation.volume} muted={presentation.muted} label={t('player.volume')} muteLabel={t(presentation.muted ? 'player.unmute' : 'player.mute')}
        onChange={volume => send({kind: 'volume', volume})} onMute={() => send({kind: 'mute'})} /> : undefined}
      next={narrow && nextTitle ? {label: t('player.next'), title: nextTitle} : undefined}
      lyrics={narrow && listening ? control('i-lyrics', t('player.lyrics'), button => listenOwner?.show(listenState!, 'lyrics', button), {disabled: !listenOwner?.lyricsOffered()}) : undefined}
      queue={narrow && listening ? control('i-queue', t('next.music.upNext'), button => listenOwner?.show(listenState!, 'queue', button), {disabled: !listenOwner?.queueOffered()}) : undefined}
      panel={narrow ? undefined : <>{favouriteBanner}{panel}{!eqOpen && listenOwner && listenState?.panel === 'lyrics' && <Button size="small" onClick={() => listenOwner.closePanel(listenState)}>{t('action.close')}</Button>}</>} />
    {equalizer ? <Menu open={moreOpen && open} onOpenChange={value => {
      if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts)) return;
      if (!value) retireMore(moreReceipt);
      setMoreOpen(value);
    }} title={t('player.more')} anchorRef={moreAnchor} returnFocusRef={moreAnchor} presentation="anchored" width={280}
      items={[{id: 'equalizer', label: t('next.media.eq.title'), description: t(settings?.enabled === true ? 'next.settings.on' : 'next.settings.off'),
        onSelect: () => {if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts) || !retireMore(moreReceipt)) return; setMoreOpen(false);if(listenOwner&&listenState)listenOwner.cover(listenState);if(!hostAdmit(hostReceipt,publishedHost,facts))return;setEqOpen(true);}}]} /> : null}
    {narrow&&<Dialog open={open&&(eqOpen||listenState?.panelVisible===true)} onOpenChange={value=>{if(value)return;if(eqOpen)closeEq(eqReceipt);else if(listenOwner&&listenState)listenOwner.closePanel(listenState);}}
      title={eqOpen?t('next.media.eq.title'):listenState?.panel==='lyrics'?t('player.lyrics'):t('next.music.upNext')} returnFocusRef={eqOpen?moreAnchor:listenState?.focus??undefined}>{panel}</Dialog>}
  </section>;
}
