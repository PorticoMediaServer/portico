import {useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {defaultI18n} from '@i18n';
import {playbackIdentity} from '@core/presentation/kit-model/index.ts';
import {AmbientArt, Artwork} from '../../../kit/artwork';
import {Icon} from '../../../kit/Icon';
import {Slider} from '../../../kit/controls';
import {Menu, Dialog, Banner} from '../../../kit/overlays';
import {router} from '../../../router';
import {ListeningNotice, SignalLine, SignalPath} from '../../../kit/media';
import {dispatchWatch, watchSeek, watchSeekBounds, type WatchIntent} from '../model';
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
import {ListeningSeek} from '../listening/ListeningSeek';
import {musicQueueOrigin} from './queue-facts';
import {useMusicFavorite} from './use-music-favorite';
import ambientStyles from '../listening/listening-ambient.module.css';
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
  const from = musicQueueOrigin(snapshot);
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
  return <section ref={motion?.scope ? motion.frame : undefined} className={styles.frame} data-music-motion-owned={!!motion?.scope || undefined} inert={!open || undefined} aria-hidden={!open || undefined} aria-label={t('next.music.nowPlaying')} tabIndex={-1} data-watch-music data-phone={phone || undefined}>
    <AmbientArt source={binding.artwork.ambient(presentation.posterUrl ?? presentation.backdropUrl)} className={`${ambientStyles.ambient}${phone ? ` ${ambientStyles.phone}` : ''}`}/>
    <header className={styles.top}>
      <button type="button" className={styles.round} aria-label={t('player.minimize')} onClick={onCollapse}><Icon name="i-down" size={22} /></button>
      {from && <span className={styles.from}>{t('next.music.playingFrom', {source: from})}</span>}
      {!phone && listeningNotice}
      <span className={styles.spacer} />
      {playOn && listenOwner && listenState && <button type="button" className={styles.glass} aria-label={t('next.dock.playOn')}
        onClick={event => {if (open) listenOwner.external(listenState, snapshot, () => playOn(event.currentTarget));}}><Icon name="i-cast" size={20}/></button>}
      {equalizer ? <button ref={moreAnchor} type="button" className={styles.glass} aria-label={t('player.more')} aria-haspopup="menu" aria-expanded={moreOpen}
        onClick={() => {
        if (moreOpen) {
          if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts) || !retireMore(moreReceipt)) return;
          setMoreOpen(false); return;
        }
        if (hostAdmit(hostReceipt, publishedHost, facts)) setMoreOpen(true);
      }}><Icon name="i-more" size={18} /></button> : null}
      {equalizer ? <Menu open={moreOpen && open} onOpenChange={value => {
        if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts)) return;
        if (!value) retireMore(moreReceipt);
        setMoreOpen(value);
      }} title={t('player.more')} anchorRef={moreAnchor} returnFocusRef={moreAnchor} presentation="anchored" width={280}
        items={[{id: 'equalizer', label: t('next.media.eq.title'), description: t(settings?.enabled === true ? 'next.settings.on' : 'next.settings.off'),
          onSelect: () => {if (!surfaceAdmit(moreReceipt, publishedMore, publishedHost, hostReceipt, facts) || !retireMore(moreReceipt)) return; setMoreOpen(false);if(listenOwner&&listenState)listenOwner.cover(listenState);if(!hostAdmit(hostReceipt,publishedHost,facts))return;setEqOpen(true);}}]} /> : null}
    </header>
    <div className={styles.main}>
      <div ref={motion?.scope ? motion.cover : undefined} className={styles.cover}>{!motion?.scope && <Artwork source={artworkSource} alt={identity.title} />}</div>
      <div className={styles.info}>
        <div className={`${styles.identity} ${styles.fade1}`} data-music-content-group="0">
          <div><h1>{identity.title}</h1>{!!identity.subtitle.length && <p>{identity.subtitle.map(part => <span key={part.id}>{part.text}</span>)}</p>}</div>
          {favorite?.offered && <button type="button" className={styles.ctl} aria-label={t(favorite.selected ? 'entry.removeFromFavorites' : 'entry.addToFavorites')}
            aria-pressed={favorite.selected} disabled={favorite.busy || favorite.failed} onClick={() => favoriteAction()}><Icon name="i-heart" size={22}/></button>}
        </div>
        {favorite?.failed && <Banner action={{label: t('next.states.tryAgain'), onAction: () => favoriteAction(true)}}>{t('next.states.pageFailedBody')}</Banner>}
        {signal && <div className={styles.signal}><SignalLine model={signal} translate={t} layout={phone ? 'phone' : 'desktop'}
          ref={signalAnchor} aria-haspopup="dialog" aria-expanded={signalOpen} onClick={() => setSignalOpen(true)} />
          <SignalPath model={signal} translate={t} locale={environment.locale} layout={phone ? 'phone' : 'desktop'}
            open={open && signalOpen} onOpenChange={setSignalOpen} anchorRef={signalAnchor} /></div>}
        <div className={styles.fade2} data-music-content-group="1">
          <ListeningSeek className={styles.scrub} position={at} duration={duration} bounds={bounds} disabled={!canSeek} phone={phone} onSeek={seek}/>
        </div>
        <div className={`${styles.controls} ${styles.fade2}`} data-music-content-group="1">
          <button type="button" className={styles.ctl} aria-label={t(queue?.shuffled ? 'player.opt.shuffleOn' : 'player.opt.shuffleOff')}
            aria-pressed={queue?.shuffled === true} disabled={policyBusy} onClick={() => policy({action: 'shuffle', shuffled: !queue?.shuffled})}><Icon name="i-shuffle" size={22}/></button>
          <button type="button" className={styles.ctl} aria-label={t('next.dock.previous')} disabled={!can('previous')} onClick={() => send({kind: 'previous'})}><Icon name="i-prev" size={24} /></button>
          <button type="button" className={styles.big} data-playback-primary aria-label={presentation.playing ? t('next.dock.pause') : t('next.dock.play')} disabled={!can(presentation.playing ? 'pause' : 'play')}
            onClick={() => send({kind: presentation.playing ? 'pause' : 'play'})}><Icon name={presentation.playing ? 'i-pause' : 'i-play'} size={28} /></button>
          <button type="button" className={styles.ctl} aria-label={t('next.dock.next')} disabled={!can('next')} onClick={() => send({kind: 'next'})}><Icon name="i-next" size={24} /></button>
          <button type="button" className={styles.ctl} aria-label={t(queue?.repeat === 'one' ? 'player.opt.repeatOne' : queue?.repeat === 'all' ? 'player.opt.repeatAll' : 'player.opt.repeatOff')}
            aria-pressed={!!queue && queue.repeat !== 'off'} disabled={policyBusy} onClick={() => policy({action: 'repeat', repeat: queue?.repeat === 'off' ? 'all' : queue?.repeat === 'all' ? 'one' : 'off'})}>
            <Icon name="i-repeat" size={22}/>{queue?.repeat === 'one' && <span className={styles.repeatOne} aria-hidden="true">1</span>}</button>
        </div>
        {phone && <div className={styles.notice}>{listeningNotice}</div>}
        {!phone && can('volume') && <div className={`${styles.volume} ${styles.fade2}`} data-music-content-group="1"><button type="button" className={styles.ctl} aria-label={t(presentation.muted ? 'player.unmute' : 'player.mute')} onClick={() => send({kind: 'mute'})}><Icon name="i-vol" size={20} /></button>
          <Slider min={0} max={1} step={.01} value={presentation.muted ? 0 : presentation.volume} aria-label={t('player.volume')} onValueChange={volume => send({kind: 'volume', volume})} /></div>}
      </div>
    </div>
    {!phone&&<aside className={styles.side} aria-label={eqOpen?t('next.media.eq.title'):t('next.music.upNext')}>
      <div className={styles.fade3} data-music-content-group="2">{panel}</div>
      {!eqOpen&&listenOwner&&listenState?.panel==='lyrics'&&<Button size="small" onClick={()=>listenOwner.closePanel(listenState)}>{t('action.close')}</Button>}
    </aside>}
    {phone&&listening&&<div className={styles.phoneTools}>
      {listenOwner?.queueOffered()&&<Button size="small" onClick={event=>listenOwner.show(listenState!,'queue',event.currentTarget)}>{t('next.music.upNext')}</Button>}
      {listenOwner?.lyricsOffered()&&<Button size="small" onClick={event=>listenOwner.show(listenState!,'lyrics',event.currentTarget)}>{t('player.lyrics')}</Button>}
    </div>}
    {phone&&<Dialog open={open&&(eqOpen||listenState?.panelVisible===true)} onOpenChange={value=>{if(value)return;if(eqOpen)closeEq(eqReceipt);else if(listenOwner&&listenState)listenOwner.closePanel(listenState);}}
      title={eqOpen?t('next.media.eq.title'):listenState?.panel==='lyrics'?t('player.lyrics'):t('next.music.upNext')} returnFocusRef={eqOpen?moreAnchor:listenState?.focus??undefined}>{panel}</Dialog>}

  </section>;
}
