import {useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import {ChannelMark} from '../../kit/artwork';
import {Button} from '../../kit/controls';
import {PanelPresence, useLeavingPresence} from '../../kit/overlays';
import {Icon, type IconName} from '../../kit/Icon';
import {liveRailDragHeld, liveRailDragSeek, liveBehindLabel, liveNextLine, liveRail, liveRecordLabel, liveSeekSeconds, liveTimeRange, liveRailValueText} from './live-chrome-model';
import type {LiveChromeDrag, LiveChromePort} from './live-chrome-types';
import styles from './live-chrome.module.css';

const t = defaultI18n.t;
const time = (ms: number) => new Intl.DateTimeFormat(defaultI18n.region.formatLocale, {hour: 'numeric', minute: '2-digit', timeZone: defaultI18n.region.timeZone ?? undefined}).format(ms);

function LiveControl({icon, label, onClick, main, small, disabled}: {icon: IconName; label: string; onClick: () => void; main?: boolean; small?: string; disabled?: boolean}) {
  return <button type="button" className={styles.control} data-main={main || undefined} data-channel={small !== undefined || undefined}
    aria-label={label} title={label} disabled={disabled} onClick={onClick}>
    <Icon name={icon} size={main ? 28 : small !== undefined ? 22 : 24}/>{small !== undefined && <small>{small}</small>}
  </button>;
}

export type LiveChromeProps = Readonly<{
  port: LiveChromePort;
  playing: boolean;
  onCollapse: () => void;
  onStop: () => void;
  onPlayPause: () => void;
  onSkip: (seconds: number) => void;
  onSeek: (seconds: number) => void;
  onMore: (anchor: HTMLButtonElement) => void;
  canPlayPause?: boolean;
  canSeek?: boolean;
  /** The unchanged generic right cluster: volume, subtitles/audio, quality, PiP and full screen. */
  right: ReactNode;
  onMiniGuideOpenChange?: (open: boolean) => void;
  /** A held seek drag keeps the controls up (motion.html 2.6). */
  onDragChange?: (dragging: boolean) => void;
  initialMiniGuideOpen?: boolean;
  reportError: (error: unknown) => void;
}>;

/** The drawn Live player chrome (desktop-live.html#live-player): top channel/programme lockup,
 * programme-span seek rail, live edge and Go to live, channel surf, Guide mini guide and Record.
 * Presentation only: every fact is a genuine channel/guide/session fact supplied by the port,
 * and every action rechecks that port's exact channel fence before it runs. */
export function LiveChrome({port, playing, onCollapse, onStop, onPlayPause, onSkip, onSeek, onMore, right, onMiniGuideOpenChange, onDragChange, initialMiniGuideOpen = false, canPlayPause = true, canSeek = true, reportError}: LiveChromeProps) {
  const [miniGuide, setMiniGuide] = useState(initialMiniGuideOpen), [recordingBusy, setRecordingBusy] = useState(false), [scrub, setScrub] = useState<number>();
  // A-05: the mini guide stays mounted while it leaves.
  const guidePresence = useLeavingPresence(miniGuide ? true : null);
  const guide = useRef<HTMLElement>(null), guideTrigger = useRef<HTMLButtonElement>(null), rail = useRef<HTMLDivElement>(null), drag = useRef<LiveChromeDrag | undefined>(undefined), scrubRef = useRef<number | undefined>(undefined);
  const window = port.window, channel = port.channel, programme = port.programme;
  const displayWindow = scrub === undefined || !liveRailDragHeld(drag.current, port.fence) ? window : {...window, positionMs: scrub};
  const railFacts = liveRail(displayWindow), behindSeconds = Math.max(0, (window.liveEdgeMs - displayWindow.positionMs) / 1000);
  const times = useRef<HTMLDivElement>(null), goLive = useRef<HTMLButtonElement>(null), [goLiveRoom, setGoLiveRoom] = useState<{start: number; end: number; docked: boolean}>();
  // The Go live pill rides the live edge between the position and "Ends" labels and never covers
  // either; where they leave no room for it (phone width, long labels) it sits after the bar
  // instead, as phone-player.html draws it.
  useLayoutEffect(() => {
    const row = times.current, pill = goLive.current;
    if (!row || !pill || typeof ResizeObserver === 'undefined') return;
    const measure = () => {
      const [position, ends] = [row.firstElementChild, row.lastElementChild].map(node => (node as HTMLElement | null)?.offsetWidth ?? 0);
      const half = pill.offsetWidth / 2, start = position + 12 + half, end = ends + 12 + half;
      const next = {start, end, docked: start + end > row.clientWidth};
      setGoLiveRoom(prior => prior && prior.start === next.start && prior.end === next.end && prior.docked === next.docked ? prior : next);
    };
    measure();
    const observer = new ResizeObserver(measure);
    for (const node of [row, pill, row.firstElementChild, row.lastElementChild]) if (node) observer.observe(node);
    return () => observer.disconnect();
  }, [window.atLive]);
  const behind = liveBehindLabel(behindSeconds), next = liveNextLine(port), range = programme ? liveTimeRange(programme.startMs, programme.endMs, time) : undefined;
  const recordLabel = liveRecordLabel(programme);
  const openGuide = (value: boolean) => {
    // Return focus before removing the focused row. Do not steal focus from a
    // destination opened outside this mini-guide (for example the full Guide).
    if (!value && guide.current?.contains(document.activeElement)) guideTrigger.current?.focus();
    setMiniGuide(value); onMiniGuideOpenChange?.(value);
  };
  useEffect(() => { if (!miniGuide) return; const rows = guide.current?.querySelectorAll<HTMLButtonElement>('[data-mini-row]'); if (rows?.length) (rows[Math.min(2, rows.length - 1)] ?? rows[0])?.focus(); }, [miniGuide]);
  const run = (work: () => void) => { if (port.isCurrent()) work(); };
  const act = (work: () => Promise<void>) => {
    if (!port.isCurrent()) return;
    try { void work().catch(reportError); } catch (error) { reportError(error); }
  };
  const record = () => {
    if (!port.isCurrent() || recordingBusy) return;
    setRecordingBusy(true);
    void port.record().then(() => {}, reportError).finally(() => setRecordingBusy(false));
  };
  const seekAt = (clientX: number) => {
    const element = rail.current;if (!element || !port.isCurrent()) return;
    const rect = element.getBoundingClientRect();if (!(rect.width > 0)) return;
    const from = window.programmeStartMs ?? window.seekStartMs, to = window.programmeEndMs ?? window.seekEndMs, span = to - from;if (!(span > 0)) return;
    const at = from + Math.min(1, Math.max(0, (clientX - rect.left) / rect.width)) * span;
    const value = Math.min(window.liveEdgeMs, Math.max(window.seekStartMs, at));
    scrubRef.current = value;setScrub(value);
  };
  const endDrag = () => {const held = !!drag.current;drag.current = undefined;scrubRef.current = undefined;setScrub(undefined);if (held) onDragChange?.(false);};
  const railKeys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!port.isCurrent()) return;
    const bounds = [window.seekStartMs, window.liveEdgeMs];
    const step = (seconds: number) => Math.min(bounds[1], Math.max(bounds[0], window.positionMs + seconds * 1000));
    const amount = event.key === 'ArrowLeft' || event.key === 'ArrowDown' ? -10 : event.key === 'ArrowRight' || event.key === 'ArrowUp' ? 10
      : event.key === 'PageDown' ? -60 : event.key === 'PageUp' ? 60 : undefined;
    const target = event.key === 'Home' ? bounds[0] : event.key === 'End' ? bounds[1] : amount === undefined ? undefined : step(amount);
    if (target !== undefined) {event.preventDefault();onSeek(liveSeekSeconds(window, target));}
  };
  const guideKeys = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') {event.preventDefault();openGuide(false);return;}
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
    const rows = [...(guide.current?.querySelectorAll<HTMLButtonElement>('[data-mini-row]') ?? [])];
    const index = rows.indexOf(document.activeElement as HTMLButtonElement);
    if (index < 0) return;
    event.preventDefault();rows[Math.max(0, Math.min(rows.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)))]?.focus();
  };
  return <div className={styles.live} data-live-chrome>
    <div className={styles.shade} aria-hidden="true"/>
    <header className={styles.top}>
      <LiveControl icon="i-down" label={t('player.minimize')} onClick={onCollapse}/>
      {channel.mark && <ChannelMark className={styles.mark} mark={{sourceId: channel.sourceId, id: channel.id, ...channel.mark}} size={48}/>}
      <div className={styles.identity}>
        <h2>{programme?.title ?? channel.name}</h2>
        <p><strong>{channel.name}</strong>{channel.number && <><i className={styles.gap}/>{channel.number}</>}<span className={styles.liveTag}><i/>{t('guide.live')}</span></p>
        {(range || next) && <p>{range}{range && next && <i className={styles.gap}/>}{next}</p>}
      </div>
      <div className={styles.tools}>
        {channel.recordAvailable && programme && recordLabel && <Button className={styles.record} variant="glass" size="small" icon="i-rec" disabled={recordingBusy} onClick={record}><span className={styles.recordText}>{recordLabel}</span></Button>}
        <button type="button" className={styles.control} aria-label={t('player.more')} title={t('player.more')} onClick={event => onMore(event.currentTarget)}><Icon name="i-more" size={24}/></button>
        <LiveControl icon="i-close" label={t('player.close')} onClick={onStop}/>
      </div>
    </header>
    {guidePresence.shown && <PanelPresence leaving={guidePresence.leaving}><aside ref={guide} className={styles.miniGuide} aria-label={t('player.live.guide')} onKeyDown={guideKeys}>
      <div className={styles.miniHead}><b>{t('player.live.guide')}</b><span>{t('next.live.miniGuideHint')}</span></div>
      {port.miniGuide.map(item => {
        const current = item.channel.id === channel.id && item.channel.sourceId === channel.sourceId;
        const facts = current ? programme : item.programme, from = facts?.startMs ?? window.programmeStartMs, to = facts?.endMs ?? window.programmeEndMs;
        const span = from !== undefined && to !== undefined ? to - from : 0, progress = span > 0 ? Math.min(100, Math.max(0, (window.liveEdgeMs - from!) / span * 100)) : 0;
        return <button type="button" className={styles.miniRow} data-mini-row data-current={current || undefined} key={`${item.channel.sourceId}:${item.channel.id}`}
          onClick={() => act(async () => {await port.tuneTo({sourceId: item.channel.sourceId, channelId: item.channel.id, generation: item.channel.generation, kind: item.channel.kind});openGuide(false);})}>
          {item.channel.mark && <ChannelMark className={styles.miniMark} mark={{sourceId: item.channel.sourceId, id: item.channel.id, ...item.channel.mark}} size={34}/>}
          <span className={styles.miniCopy}>
            <span className={styles.miniChannel}>{item.channel.name}<i className={styles.gap}/>{item.channel.number}{current && <em className={styles.watching}>{t('next.live.watching')}</em>}</span>
            <b>{facts?.title ?? t('player.live.noProgramme')}</b>
            <span className={styles.miniProgress}><span className={styles.miniTrack} aria-hidden="true"><i style={{width: `${progress.toFixed(2)}%`}}/></span>
              {item.nextStartMs !== undefined && <span>{t('next.live.nextAt', {time: time(item.nextStartMs)})}</span>}</span>
          </span>
        </button>;
      })}
      <button type="button" className={styles.miniAll} onClick={() => {run(port.openFullGuide);openGuide(false);}}>
        {t('next.live.openFullGuide')}<Icon name="i-right" size={16}/>
      </button>
    </aside></PanelPresence>}
    <div className={styles.bottom}>
      <div className={styles.seek}>
        <div ref={rail} className={styles.rail} role="slider" aria-label={t('player.position')}
          aria-valuemin={Math.round(liveSeekSeconds(window, window.seekStartMs))} aria-valuemax={Math.round(liveSeekSeconds(window, window.liveEdgeMs))} aria-valuenow={Math.round(liveSeekSeconds(window, displayWindow.positionMs))}
          aria-disabled={!canSeek || undefined} tabIndex={canSeek ? 0 : -1} aria-valuetext={liveRailValueText(displayWindow, programme, time)} onKeyDown={railKeys}
          onPointerDown={event => {if (!canSeek || !port.isCurrent()) return;drag.current = {fence: port.fence, window};onDragChange?.(true);event.currentTarget.setPointerCapture(event.pointerId);seekAt(event.clientX);}}
          onPointerMove={event => {if (!drag.current) return;if (!liveRailDragHeld(drag.current, port.fence)) {endDrag();return;}seekAt(event.clientX);}}
          onPointerUp={event => {const captured = drag.current;const value = scrubRef.current;endDrag();if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);const seconds = liveRailDragSeek(captured, port.fence, value);if (seconds !== undefined && port.isCurrent()) onSeek(seconds);}}
          onPointerCancel={endDrag}>
          <i className={styles.gone} style={{width: `${railFacts.gone}%`}}/>
          <i className={styles.buffer} style={{left: `${railFacts.gone}%`, width: `${railFacts.buffer}%`}}/>
          <i className={styles.played} style={{left: `${railFacts.gone}%`, width: `${railFacts.played}%`}}/>
          <i className={styles.future} style={{left: `${railFacts.edge}%`}}/>
          <span className={styles.edge} style={{left: `${railFacts.edge}%`}} title={t('guide.live')}/>
          <span className={styles.knob} style={{left: `${railFacts.knob}%`}}/>
        </div>
        {!window.atLive && <button ref={goLive} type="button" className={styles.goLive} data-docked={goLiveRoom?.docked || undefined}
          style={goLiveRoom && !goLiveRoom.docked ? {left: `clamp(${goLiveRoom.start}px, ${railFacts.edge}%, calc(100% - ${goLiveRoom.end}px))`} : undefined}
          aria-label={t('player.live.goLive')} onClick={() => act(port.goLive)}><i/>{t('player.live.goLive')}</button>}
      </div>
      <div ref={times} className={styles.times}>
        <span><b>{time(displayWindow.positionMs)}</b>{behind && <><i className={styles.gap}/><em>{behind}</em></>}</span>
        <span>{programme && t('guide.ends', {time: time(programme.endMs)})}</span>
      </div>
      <div className={styles.controls}>
        <div className={styles.left}><Button ref={guideTrigger} className={styles.guideButton} variant="glass" size="small" icon="i-list" aria-expanded={miniGuide}
          onClick={() => {run(() => openGuide(!miniGuide));}}>{t('player.live.guide')}</Button></div>
        <div className={styles.center}>
          <LiveControl icon="i-chdown" label={port.down ? t('next.live.channelDown', {channel: port.down.channel.name, number: port.down.channel.number}) : t('web.player.channelDown')}
            small={port.down?.channel.number} disabled={!port.down} onClick={() => act(() => port.tune(-1))}/>
          <LiveControl icon="i-back" label={t('next.dock.back', {seconds: port.skipBackSeconds})} small={String(port.skipBackSeconds)} disabled={!canSeek} onClick={() => run(() => onSkip(-port.skipBackSeconds))}/>
          <LiveControl main icon={playing ? 'i-pause' : 'i-play'} label={t(playing ? 'next.dock.pause' : 'next.dock.play')} disabled={!canPlayPause} onClick={() => run(onPlayPause)}/>
          <LiveControl icon="i-fwd" label={t('next.dock.forward', {seconds: port.skipForwardSeconds})} small={String(port.skipForwardSeconds)} disabled={!canSeek} onClick={() => run(() => onSkip(port.skipForwardSeconds))}/>
          <LiveControl icon="i-chup" label={port.up ? t('next.live.channelUp', {channel: port.up.channel.name, number: port.up.channel.number}) : t('web.player.channelUp')}
            small={port.up?.channel.number} disabled={!port.up} onClick={() => act(() => port.tune(1))}/>
        </div>
        <div className={styles.right}>{right}</div>
      </div>
    </div>
  </div>;
}
