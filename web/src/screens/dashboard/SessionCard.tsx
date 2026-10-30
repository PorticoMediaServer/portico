import {useSessionMove} from './use-session-move';
import {useEffect, useId, useRef, useState} from 'react';
import {defaultI18n} from '@i18n';
import type {DashboardSession as AdminSession} from './bindings';
import {Button} from '../../kit/controls';
import {Dialog, Menu} from '../../kit/overlays';
import {Icon} from '../../kit/Icon';
import {Artwork} from '../../kit/artwork/Artwork';
import {accountAvatarStyle} from '../../kit/settings/account-avatar';
import type {DashboardBindings} from './bindings';
import {fmtDur, formatStreamRate, formatTrackRoute} from './format';
import {clock, sessionTitle, sessionStopTitle, sessionTracks, type Track} from './model';
import styles from './dashboard.module.css';
const t = defaultI18n.t;
/** The drawn phone card's route chip: the one route a stream is taking, never an inferred total. */
function phoneRoute(tracks: readonly Track[]): Track | undefined {
  return tracks.find(track => track.kind === 'video') ?? tracks[0];
}
export function SessionCard({session: s, bindings: b, refresh, layout = 'desktop', retained = false, onOverlayChange}: {session: AdminSession; bindings: DashboardBindings; refresh: (removedId?: string) => void; layout?: 'desktop' | 'phone' | 'compact'; retained?: boolean; onOverlayChange?: (id: string, open: boolean) => void}) {
  const [menu, setMenu] = useState(false), [stop, setStop] = useState(false), [message, setMessage] = useState(''), [busy, setBusy] = useState(false);
  const messageId = useId(), cancel = useRef<HTMLButtonElement>(null), opener = useRef<HTMLButtonElement>(null), item = s.item, square = s.kind === 'audio' || item?.kind === 'audiobook_file';
  const art = s.itemId ? b.artwork(s.itemId, square ? 'square' : 'poster', item?.art.find(a => a.role === (square ? 'square' : 'poster'))?.revision) : undefined;
  const backdrop = s.itemId ? b.artwork(s.itemId, 'backdrop', item?.art.find(a => a.role === 'backdrop')?.revision) : undefined;
  const act = async (action: () => Promise<void>, depart = false) => {if (!b.isCurrent() || busy) return; setBusy(true); try {await action(); if (b.isCurrent()) {setStop(false); refresh(depart ? s.id : undefined);}} catch (error) {if (b.isCurrent()) b.effects.reportError(error);} finally {if (b.isCurrent()) setBusy(false);}};
  const state = s.state === 'playing' ? t('next.dashboard.playing') : s.state === 'paused' ? t('next.dashboard.paused') : s.state === 'buffering' ? t('next.dashboard.buffering') : s.state === 'preparing' ? t('next.dashboard.preparing') : undefined;
  const title = sessionTitle(s), stopTitle = sessionStopTitle(s);
  const runtime = s.durationMs ? fmtDur(s.durationMs / 3_600_000) : undefined;
  const move = useSessionMove(b, s, title, menu, retained, () => refresh());
  const overlayOpen = menu || stop || !!move.dialog || busy;
  useEffect(() => {onOverlayChange?.(s.id, overlayOpen);return () => onOverlayChange?.(s.id, false);}, [s.id, overlayOpen, onOverlayChange]);
  const episode = item?.showTitle && s.title !== item.showTitle;
  const tracks = sessionTracks(s), current = s.currentBitrateKbps;
  const route = phoneRoute(tracks), routeLabel = route && formatTrackRoute(route), paused = s.state === 'paused';
  const factClass = layout === 'phone' ? styles.phoneFact : styles.sessionFact;
  const facts = [
    (s.location === 'local' || s.location === 'remote') && <span className={factClass} key="location">{t(s.location === 'local' ? 'next.kit.session.local' : 'next.kit.session.remote')}</span>,
    routeLabel && <span className={factClass} key="route" data-route={route!.route}>{routeLabel}</span>,
    current != null && <span className={factClass} key="rate">{formatStreamRate(current)}</span>,
  ].filter(Boolean);
  const subtitle = episode ? <p>{item!.seasonNumber !== undefined && item!.episodeNumber !== undefined && <><span>{t('title.episodeCode', {season: item!.seasonNumber, episode: item!.episodeNumber})}</span><i className={styles.dotSep} aria-hidden="true" /></>}<span>{s.title}</span></p>
            : s.live ? ((s.channel?.name || s.programme) ? <p>{s.channel?.name && <span>{s.channel.name}</span>}{s.channel?.name && s.programme && <i className={styles.dotSep} aria-hidden="true" />}{s.programme && <span>{t('next.dashboard.format.ends', {time: defaultI18n.time(Date.parse(s.programme.endsAt))})}</span>}</p> : null)
            : square ? ((item?.artist || item?.author || item?.album) ? <p>{(item?.artist || item?.author) && <span>{item.artist || item.author}</span>}{(item?.artist || item?.author) && (item?.album || (runtime && item?.kind === 'audiobook_file')) && <i className={styles.dotSep} aria-hidden="true" />}{(item?.album || (runtime && item?.kind === 'audiobook_file')) && <span>{item?.album || runtime}</span>}</p> : null)
            : item?.year ? <p><span>{item.year}</span></p> : null;
  const menuItems = [
    ...(move.canMove ? [{id: 'move', icon: 'i-clock' as const, label: t('web.nowPlaying.moveTo'), onSelect: move.open}] : []),
    ...(['paused', 'playing'].includes(s.state) ? [{id: 'transport', icon: s.state === 'paused' ? 'i-play' as const : 'i-pause' as const, label: s.state === 'paused' ? t('next.dashboard.resume') : t('next.dashboard.pause'), onSelect: () => {void act(() => b.effects.transport(s.id, s.state === 'paused' ? 'resume' : 'pause'));}}] : []),
    ...(b.role === 'owner' && stopTitle ? [{id: 'stop', icon: 'i-stop' as const, label: t('next.dashboard.stop'), destructive: true, onSelect: () => {setMessage(''); setStop(true);}}] : []),
  ];
  const sessionMenu = <><Menu open={menu} onOpenChange={setMenu} title={t('next.dashboard.sessionMenu', {user: s.user})} trigger={<Button ref={opener} data-session-menu icon="i-more" iconOnly size="xs" disabled={busy} aria-label={t('next.dashboard.sessionMenu', {user: s.user})} />} items={menuItems} />{move.dialog}</>;
  const stopDialog = <Dialog open={stop} onOpenChange={value => {if (!busy) setStop(value);}} title={t('next.dashboard.stopTitle', {title: stopTitle})} description={s.user && s.device ? t('next.dashboard.stopDetail', {user: s.user, device: s.device}) : undefined} returnFocusRef={opener} initialFocusRef={cancel} width={560} showCloseButton={false} className={styles.stopDialog} bodyClassName={styles.stopBody} footerClassName={styles.stopFooter}
    footer={<><Button ref={cancel} size="small" disabled={busy} onClick={() => setStop(false)}>{t('next.dashboard.cancel')}</Button><Button size="small" className={styles.stopButton} loading={busy} onClick={() => {void act(() => b.effects.stop(s.id, message.trim() || undefined), true);}}>{t('next.dashboard.stopPlayback')}</Button></>}>
    <div className={styles.viewerMessage}><label htmlFor={messageId}>{t('next.dashboard.message')}</label><textarea id={messageId} value={message} maxLength={500} placeholder={t('next.dashboard.messagePlaceholder')} aria-describedby={`${messageId}-help`} disabled={busy} onChange={event => setMessage(event.target.value)} /><small id={`${messageId}-help`}><span>{t('next.dashboard.messageHelp')}</span><span aria-hidden="true">{defaultI18n.number(message.length)}</span></small></div>
  </Dialog>;
  if (layout === 'compact') {
    const converted = tracks.some(track => track.route === 'converted');
    const note = s.state === 'buffering' ? t('next.dashboard.buffering') : s.state === 'paused' ? t('next.dashboard.paused') : converted ? t('next.dashboard.converted') : undefined;
    return <article className={styles.compactSession} data-state={s.state} aria-label={[title, s.user, s.device].filter(Boolean).join(', ')}>
      <span className={styles.compactAvatar} style={accountAvatarStyle(s.user)} aria-hidden="true">{s.user.slice(0, 1).toUpperCase()}</span>
      <button type="button" className={styles.compactOpen} data-session-primary disabled={retained || busy} onClick={() => {if (b.isCurrent()) setMenu(true);}}>
        <span className={styles.compactWho}><b>{s.user}</b>{s.profile && s.profile !== s.user && <span>{s.profile}</span>}<span>{s.device}</span></span>
        <span className={styles.compactWhat}><span>{title}{episode && item?.seasonNumber !== undefined && item?.episodeNumber !== undefined ? ` · ${t('title.episodeCode', {season: item.seasonNumber, episode: item.episodeNumber})}` : item?.year ? ` · ${item.year}` : ''}</span>
          {note && <small data-kind={s.state === 'buffering' ? 'problem' : s.state === 'paused' ? 'paused' : 'converted'}>{note}</small>}</span>
      </button>
      {!retained && sessionMenu}
      {!s.live && !!s.durationMs && <div className={styles.compactProgress} aria-hidden="true"><i style={{width: `${Math.min(100, Math.max(0, s.positionMs / s.durationMs * 100))}%`}}/></div>}
      {!retained && stopDialog}
    </article>;
  }
  if (layout === 'phone') {
    return <article className={styles.phoneSession} data-state={s.state}
      aria-label={[title, s.user, s.profile && s.profile !== s.user ? s.profile : undefined, s.device].filter(Boolean).join(', ')}>
      <div className={styles.phoneSessionRow}>
        <div className={styles.phoneArt} data-kind={s.live ? 'live' : square ? 'square' : 'video'}>
          {s.live ? <><Icon name="i-live" size={22} /><span>{s.channel?.name}</span></>
            : square ? <>{backdrop && <Artwork source={backdrop} alt="" width="100%" height="100%" />}<span className={styles.phoneSquare}>{art ? <Artwork source={art} alt="" width="100%" height="100%" /> : <Icon name="i-music" size={22} />}</span></>
            : (backdrop || art) ? <Artwork source={backdrop ?? art!} alt="" width="100%" height="100%" /> : <Icon name="i-play" size={26} />}
        </div>
        <div className={styles.phoneSessionText}>
          {title && <h3>{title}</h3>}
          {subtitle}
          <div className={styles.phoneWho}><span className={styles.phoneAvatar} style={accountAvatarStyle(s.user)} aria-hidden="true">{s.user.slice(0, 1).toUpperCase()}</span><b>{s.user}</b>{s.profile && s.profile !== s.user && <span>{s.profile}</span>}<span>{s.device}</span></div>
        </div>
        {paused && <span className={styles.phoneState}><Icon name="i-pause" size={12} />{t('next.dashboard.paused')}</span>}
      </div>
      {!!facts.length && <div className={styles.phoneFactRow}>{facts}</div>}
      <div className={styles.phoneSessionActions}>
        <span className={styles.phonePosition}>{s.live ? (s.startedAt ? t('next.dashboard.started', {time: defaultI18n.time(Date.parse(s.startedAt))}) : undefined)
          : s.durationMs ? t('next.kit.session.position', {position: t('next.kit.session.positionOf', {position: clock(s.positionMs), duration: clock(s.durationMs)})}) : clock(s.positionMs)}</span>
        {b.role === 'owner' && stopTitle && <Button size="xs" variant="glass" icon="i-stop" className={styles.phoneStop} onClick={() => {setMessage(''); setStop(true);}}>{t('next.dashboard.stop')}</Button>}
        {!retained && sessionMenu}
      </div>
      {!s.live && !!s.durationMs && <div className={styles.phoneProgress} aria-hidden="true"><i style={{width: `${Math.min(100, Math.max(0, s.positionMs / s.durationMs * 100))}%`}} /></div>}
      {!retained && stopDialog}
    </article>;
  }
  const who = s.profile && s.profile !== s.user ? s.profile : undefined;
  return <article className={styles.session} data-state={s.state} aria-label={title}>
    <div className={styles.sessionArt} data-kind={s.live ? 'live' : square ? 'square' : 'video'}>
      {s.live ? <><Icon name="i-live" size={22} /><span>{s.channel?.name}</span></>
        : square ? <>{backdrop && <Artwork source={backdrop} alt="" width="100%" height="100%" />}<span className={styles.sessionSquare}>{art ? <Artwork source={art} alt="" width="100%" height="100%" /> : <Icon name="i-music" size={26} />}</span></>
        : (backdrop || art) ? <Artwork source={backdrop ?? art!} alt="" width="100%" height="100%" /> : <Icon name="i-play" size={28} />}
    </div>
    <div className={styles.sessionText}>
      {title && <h4>{title}</h4>}
      {subtitle}
      <div className={styles.sessionWho}><span className={styles.sessionAvatar} style={accountAvatarStyle(s.user)} aria-hidden="true">{s.user.slice(0, 1).toUpperCase()}</span><b>{s.user}</b>{who && <span>{who}</span>}<span>{s.device}</span></div>
      {!!facts.length && <div className={styles.sessionFacts}>{facts}</div>}
    </div>
    {state && s.state !== 'playing' && <span className={styles.sessionState}><Icon name={paused ? 'i-pause' : s.state === 'buffering' ? 'i-refresh' : 'i-play'} size={12} />{state}</span>}
    {!retained && <div className={styles.sessionHover}>
      {b.role === 'owner' && stopTitle && <Button size="xs" variant="glass" icon="i-stop" onClick={() => {setMessage(''); setStop(true);}}>{t('next.dashboard.stop')}</Button>}
      {sessionMenu}
    </div>}
    {!s.live && !!s.durationMs && <div className={styles.sessionBar} aria-hidden="true"><i style={{width: `${Math.min(100, Math.max(0, s.positionMs / s.durationMs * 100))}%`}} /></div>}
    {!retained && stopDialog}
  </article>;
}
