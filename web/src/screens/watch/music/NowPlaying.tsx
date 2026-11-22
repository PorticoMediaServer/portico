import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {ArtworkGlow} from '../../../kit/artwork';
import {ArtPlaceholder, AudioPlayerFrame} from '../../../kit/frames';
import {AudioPanelSurface, PlayerVolume} from '../../../kit/player';
import {Menu, Dialog, Banner} from '../../../kit/overlays';
import {router} from '../../../router';
import {ListeningNotice, SeekLine, SignalLine, SignalPath} from '../../../kit/media';
import {dispatchWatch, watchClock, watchSeek, watchSeekBounds, type WatchIntent} from '../model';
import {ListeningPanels,ListeningTabs} from '../listening/ListeningPanels';
import type {ListeningHost} from '../listening/use-listening-owner';
import {Button} from '../../../kit/controls';
import {StateView} from '../../../kit/states';
import {watchMusicSignal} from './signal';
import {readWatchMusicAudioFacts} from './audio-facts';
import {fenceKey} from '../../../shell/playback-model';
import {WatchEqualizerPanel, hostAdmit, surfaceAdmit, type WatchHostFacts, type WatchReceipt, type WatchSurfaceReceipt} from './EqualizerPanel';
import type {WatchScreenProps} from '../types';
import type {WatchPlaybackContext} from '../play-on-binding';
import type {V1QueuePolicyIntent} from '@core/playback-v1/v1-queue-player.ts';
import {musicQueueNext, musicQueueOrigin, musicQueueSong} from './queue-facts';
import {useMusicFavorite} from './use-music-favorite';
import styles from './now-playing.module.css';
import type {MusicExpansion} from './expansion-owner';

const t = defaultI18n.t;
const noSubscribe = () => () => {};
const noEqualizer = () => null;

/** The drawn expanded Now playing host for music (listening-notices frames 2 and 3).
 * It is the same authority-local runtime and the same retained media owner; this view
 * only presents facts, and the actionable DockNotice stays with the dock. */
export function WatchMusicNowPlaying({binding, snapshot, expanded, onCollapse, phone, motion, isCurrent,listening,playOn,context}: Pick<WatchScreenProps, 'binding' | 'snapshot' | 'expanded' | 'onCollapse'> & {phone: boolean; motion?: MusicExpansion; isCurrent?: () => boolean;listening?:ListeningHost;playOn?:(anchor?:HTMLButtonElement)=>void;context?:WatchPlaybackContext}) {
  const presentation = snapshot.presentation;
  const environment = binding.environment;
  const equalizer = binding.runtime.equalizerPort;
  const settings = useSyncExternalStore(equalizer?.subscribe ?? noSubscribe, equalizer?.getSnapshot ?? noEqualizer, noEqualizer);
  useEffect(() => { if (!equalizer) return; return equalizer.start(); }, [equalizer]);
  const [signalOpen, setSignalOpen] = useState(false);
  const [eqOpen, setEqOpen] = useState(false), [moreOpen, setMoreOpen] = useState(false), [songOpen, setSongOpen] = useState(false);
  const songAnchor = useRef<HTMLButtonElement>(null);
  const signalAnchor = useRef<HTMLButtonElement>(null), moreAnchor = useRef<HTMLButtonElement>(null);
  const signal = watchMusicSignal(snapshot, settings, readWatchMusicAudioFacts(binding.runtime, snapshot));
  const identity = presentation ? playbackIdentity(presentation.subject, 'full', environment) : undefined;
  const artworkSource = presentation ? binding.artwork.foreground(presentation.posterUrl ?? presentation.backdropUrl, 560, 1, {group: 'playback', priority: 'high'}) : undefined;
  const open = expanded && presentation?.kind === 'music' && !!identity && (!isCurrent || isCurrent()) && (!motion || !!motion.scope);
  const favorite = useMusicFavorite(context, binding.runtime, snapshot, open, binding.reportError, isCurrent);
  useLayoutEffect(() => {if (!open) {setSignalOpen(false); setEqOpen(false); setMoreOpen(false); setSongOpen(false);}}, [open]);
  // Esc minimises, as it does for video and audiobooks (shortcuts list); open panels and fields keep their own Esc.
  const collapse = useRef(onCollapse); collapse.current = onCollapse;
  useEffect(() => {
    if (!open || signalOpen || eqOpen || moreOpen || songOpen || typeof document === 'undefined') return;
    const key = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || (event.target as Element | null)?.closest?.('[role="dialog"],[role="menu"],input,textarea,select,[contenteditable="true"]')) return;
      event.preventDefault(); collapse.current?.();
    };
    document.addEventListener('keydown', key);
    return () => document.removeEventListener('keydown', key);
  }, [open, signalOpen, eqOpen, moreOpen, songOpen]);
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
  const from = musicQueueOrigin(snapshot), nextTitle = phone ? musicQueueNext(snapshot) : undefined;
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
  const openMore = (button: HTMLButtonElement) => {
    moreAnchor.current = button;
    if (moreOpen) {
      if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts) || !retireMore(moreReceipt)) return;
      setMoreOpen(false); return;
    }
    if (hostAdmit(hostReceipt, publishedHost, facts)) setMoreOpen(true);
  };
  // desktop-listen "now": the ⋯ beside the song's title is the song's own menu: Start radio from it, and its album's and
  // artist's pages (the dock and the album page offer the same). Each runs only while this same song is still playing.
  const song = open ? musicQueueSong(snapshot) : undefined, songKey = song ? song.libraryId + ':' + song.itemId : '';
  const stillPlaying = () => !!song && musicQueueSong(binding.runtime.getSnapshot())?.itemId === song.itemId && (!isCurrent || isCurrent());
  const goTo = (view: 'album' | 'artist', entityId: string) => {
    setSongOpen(false);
    if (!song || !stillPlaying()) return;
    onCollapse();
    void router.navigate({to: '/library/$libraryId', params: {libraryId: song.libraryId}, search: {view, entityId}}).catch(binding.reportError);
  };
  const songItems = song ? [
    {id: 'radio', label: t('music.startRadio'), onSelect: () => {
      setSongOpen(false);
      if (!stillPlaying()) return;
      void binding.runtime.playFromQueue({container: {kind: 'radio', id: song.itemId}}, {signal: new AbortController().signal, isCurrent: () => !isCurrent || isCurrent()}).catch(binding.reportError);
    }},
    ...(song.albumId && song.album ? [{id: 'album', label: t('music.goToArtist', {name: song.album}), onSelect: () => goTo('album', song.albumId!)}] : []),
    ...(song.artistId && song.artist ? [{id: 'artist', label: t('music.goToArtist', {name: song.artist}), onSelect: () => goTo('artist', song.artistId!)}] : []),
  ] : [];
  const playOnHere = playOn && listenOwner && listenState ? (button: HTMLButtonElement) => {if (open) listenOwner.external(listenState, snapshot, () => playOn(button));} : undefined;
  const repeat = queue?.repeat ?? 'off';
  const scrub = bounds ? Math.min(bounds.end, Math.max(bounds.start, at)) : at;
  // frame 8 (desktop-listen "now", phone-app 14): the kit's AudioPlayerFrame places every region. The screen keeps its own
  // cover view (the box the dock's artwork morphs into), the More menu and the equaliser, the signal path, and the phone's sheet.
  return <section ref={motion?.scope ? motion.frame : undefined} className={`portico-cover ${styles.frame}`} data-cover={glowSource ? 'audio' : undefined} data-music-motion-owned={!!motion?.scope || undefined} inert={!open || undefined} aria-hidden={!open || undefined} aria-label={t('next.music.nowPlaying')} tabIndex={-1} data-watch-music data-phone={phone || undefined}>
    <ArtworkGlow source={glowSource} phone={phone}/>
    <AudioPlayerFrame
      close={{icon: 'i-down', label: t('player.minimize'), onPress: () => onCollapse()}}
      context={from ? t('web.player.playingFromLabel') : undefined} source={from}
      device={playOnHere ? {icon: 'i-cast', label: t('device.thisDevice'), onPress: playOnHere} : undefined}
      more={{icon: 'i-more', label: t('player.more'), disabled: !equalizer, active: moreOpen, onPress: openMore}}
      // More holds the equaliser only; where this browser has none, the frame keeps More's place, disabled (frame 8's fixed region).
      artwork={motion?.scope ? undefined : artworkSource} icon="i-music"
      // The morph paints the dock's artwork into this box; a song with no picture keeps the placeholder in it (rule 12).
      cover={motion?.scope ? <div ref={motion.cover} className={styles.cover}>{!artworkSource && <ArtPlaceholder icon="i-music" />}</div> : undefined}
      title={identity.title} byline={identity.subtitle.map(part => part.text)}
      favourite={favorite?.offered ? {icon: favorite.selected ? 'i-heart-f' : 'i-heart', label: t(favorite.selected ? 'entry.removeFromFavorites' : 'entry.addToFavorites'), active: favorite.selected,
        disabled: favorite.busy || favorite.failed, onPress: () => favoriteAction()} : undefined}
      titleMore={songItems.length ? {icon: 'i-more', label: t('player.more'), active: songOpen, onPress: button => {songAnchor.current = button; setSongOpen(value => !value);}} : undefined}
      signal={favorite?.failed || signal ? <>
        {favorite?.failed && <Banner action={{label: t('next.states.tryAgain'), onAction: () => favoriteAction(true)}}>{t('next.states.pageFailedBody')}</Banner>}
        {signal && <><SignalLine model={signal} translate={t} layout={phone ? 'phone' : 'desktop'} ref={signalAnchor} aria-haspopup="dialog" aria-expanded={signalOpen} onClick={() => setSignalOpen(true)} />
          <SignalPath model={signal} translate={t} locale={environment.locale} layout={phone ? 'phone' : 'desktop'} open={open && signalOpen} onOpenChange={setSignalOpen} anchorRef={signalAnchor} /></>}
      </> : undefined}
      scrubber={<SeekLine variant="player" value={duration > 0 ? scrub / duration : 0}
        input={{min: bounds?.start ?? 0, max: bounds?.end ?? Math.max(duration, 1), step: .1, value: scrub, disabled: !canSeek, 'aria-label': t('player.position'),
          'aria-valuetext': t('player.clockOf', {position: watchClock(scrub), duration: watchClock(duration)}),
          onChange: event => {const value = event.currentTarget.valueAsNumber; if (canSeek && Number.isFinite(value)) seek(value);}}} />}
      times={{position: watchClock(scrub), end: phone ? `−${watchClock(Math.max(0, duration - scrub))}` : watchClock(duration)}}
      transport={[
        {icon: 'i-shuffle', label: t(queue?.shuffled ? 'player.opt.shuffleOn' : 'player.opt.shuffleOff'), active: queue?.shuffled === true, disabled: policyBusy || !!queue?.radio, onPress: () => policy({action: 'shuffle', shuffled: !queue?.shuffled})},
        {icon: 'i-prev', label: t('next.dock.previous'), disabled: !can('previous'), onPress: () => send({kind: 'previous'})},
        {icon: presentation.playing ? 'i-pause' : 'i-play', label: presentation.playing ? t('next.dock.pause') : t('next.dock.play'), disabled: !can(presentation.playing ? 'pause' : 'play'), onPress: () => send({kind: presentation.playing ? 'pause' : 'play'})},
        {icon: 'i-next', label: t('next.dock.next'), disabled: !can('next'), onPress: () => send({kind: 'next'})},
        // Repeat one is the repeat arrow with a 1 inside it, as a skip arrow carries its seconds.
        {icon: 'i-repeat', label: t(repeat === 'one' ? 'player.opt.repeatOne' : repeat === 'all' ? 'player.opt.repeatAll' : 'player.opt.repeatOff'), active: repeat !== 'off', ...(repeat === 'one' ? {skip: 1} : {}), disabled: policyBusy,
          onPress: () => policy({action: 'repeat', repeat: repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'})},
      ]}
      volume={!phone && can('volume') ? <PlayerVolume value={presentation.volume} muted={presentation.muted} label={t('player.volume')} muteLabel={t(presentation.muted ? 'player.unmute' : 'player.mute')}
        onChange={volume => send({kind: 'volume', volume})} onMute={() => send({kind: 'mute'})} /> : undefined}
      next={phone && nextTitle ? {label: t('player.next'), title: nextTitle} : undefined}
      lyrics={phone && listenOwner?.lyricsOffered() ? {icon: 'i-lyrics', label: t('player.lyrics'), onPress: button => listenOwner.show(listenState!, 'lyrics', button)} : undefined}
      queue={phone && listenOwner?.queueOffered() ? {icon: 'i-queue', label: t('next.music.upNext'), onPress: button => listenOwner.show(listenState!, 'queue', button)} : undefined}
      panel={phone ? undefined : <AudioPanelSurface label={eqOpen ? t('next.media.eq.title') : t('next.music.upNext')}><div className={styles.side}>{panel}
        {!eqOpen && listenOwner && listenState?.panel === 'lyrics' && <Button size="small" onClick={() => listenOwner.closePanel(listenState)}>{t('action.close')}</Button>}</div></AudioPanelSurface>} />
    {/* The notice of what the player did (listening-notices frames 2 and 3): over the top bar from 600, under the player on a phone. */}
    <div className={phone ? styles.notice : styles.topNotice}>{listeningNotice}</div>
    {songItems.length > 0 && <Menu key={songKey} open={songOpen && open} onOpenChange={setSongOpen} title={identity.title} anchorRef={songAnchor} returnFocusRef={songAnchor} presentation="anchored" width={280} items={songItems}/>}
    {equalizer ? <Menu open={moreOpen && open} onOpenChange={value => {
      if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts)) return;
      if (!value) retireMore(moreReceipt);
      setMoreOpen(value);
    }} title={t('player.more')} anchorRef={moreAnchor} returnFocusRef={moreAnchor} presentation="anchored" width={280}
      items={[{id: 'equalizer', label: t('next.media.eq.title'), description: t(settings?.enabled === true ? 'next.settings.on' : 'next.settings.off'),
        onSelect: () => {if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts) || !retireMore(moreReceipt)) return; setMoreOpen(false);if(listenOwner&&listenState)listenOwner.cover(listenState);if(!hostAdmit(hostReceipt,publishedHost,facts))return;setEqOpen(true);}}]} /> : null}
    {phone&&<Dialog open={open&&(eqOpen||listenState?.panelVisible===true)} onOpenChange={value=>{if(value)return;if(eqOpen)closeEq(eqReceipt);else if(listenOwner&&listenState)listenOwner.closePanel(listenState);}}
      title={eqOpen?t('next.media.eq.title'):listenState?.panel==='lyrics'?t('player.lyrics'):t('next.music.upNext')} returnFocusRef={eqOpen?moreAnchor:listenState?.focus??undefined}>{panel}</Dialog>}
  </section>;
}
