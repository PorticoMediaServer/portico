import {useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from 'react';
import {defaultI18n} from '@i18n';
import type {DashboardBindings, DashboardSession, DashboardSessions} from './bindings';
import {Button} from '../../kit/controls';
import {SessionCard} from './SessionCard';
import {fmtMbps} from './format';
import {COMPACT_SESSION_HEIGHT, SESSION_PREVIEW_COUNT, sessionWindow} from './session-window';
import {useLayoutMotion} from '../../kit/layout/motion';
import {useLayoutPresence} from '../../kit/layout/presence';
import {useListRemovalFocus} from '../../kit/layout/use-list-removal-focus';
import styles from './dashboard.module.css';

const t = defaultI18n.t;

/** One phone list owns its cards across the busy threshold. A polling update
 * must not replace the host of an open session action or its captured focus. */
export function BusySessions({sessions, totals, bindings, refresh, empty}: {
  sessions: readonly DashboardSession[]; totals?: DashboardSessions['totals']; bindings: DashboardBindings;
  refresh: (session: DashboardSession, removedId?: string) => void; empty: ReactNode;
}) {
  const [expanded, setExpanded] = useState(false), [viewport, setViewport] = useState({top: 0, height: 660});
  const [compact, setCompact] = useState(sessions.length > SESSION_PREVIEW_COUNT);
  const [focused, setFocused] = useState<string>(), [overlays, setOverlays] = useState<readonly string[]>([]);
  const root = useRef<HTMLDivElement>(null), toggle = useRef<HTMLButtonElement>(null), nextFocus = useRef<string | undefined>(undefined);
  const overlay = useCallback((id: string, open: boolean) => setOverlays(current => open
    ? current.includes(id) ? current : [...current, id] : current.includes(id) ? current.filter(value => value !== id) : current), []);
  useLayoutEffect(() => {
    if (expanded || focused || overlays.length) return;
    const next = sessions.length > SESSION_PREVIEW_COUNT;
    if (next === compact) return;
    // The disappearing Show all control hands focus to this same list.
    if (!next && toggle.current === toggle.current?.ownerDocument.activeElement) root.current?.focus({preventScroll: true});
    setCompact(next);
  }, [sessions.length, compact, expanded, focused, overlays.length]);
  useEffect(() => {
    const element = root.current;
    if (!overlays.length && focused && element && !element.contains(element.ownerDocument.activeElement)) setFocused(undefined);
  }, [focused, overlays.length]);
  useEffect(() => {
    const element = root.current;if (!element || !compact || !expanded) return;
    const measure = () => setViewport({top: element.scrollTop, height: element.clientHeight});
    const observer = new ResizeObserver(measure);observer.observe(element);measure();return () => observer.disconnect();
  }, [compact, expanded]);
  const indices = useMemo(() => {
    const pins = sessions.flatMap((session, index) => session.id === focused || overlays.includes(session.id) ? [index] : []);
    if (compact && expanded) return sessionWindow(sessions.length, viewport.top, viewport.height, pins);
    const count = compact ? Math.min(SESSION_PREVIEW_COUNT, sessions.length) : sessions.length;
    return [...new Set([...Array.from({length: count}, (_, index) => index), ...pins])].sort((a, b) => a - b);
  }, [sessions, compact, expanded, viewport, focused, overlays]);
  const paint = useMemo(() => indices.map(index => sessions[index]), [sessions, indices]);
  const motion = useLayoutMotion(), sessionKey = useCallback((session: DashboardSession) => session.id, []);
  const focus = useListRemovalFocus(bindings);
  // Keep the ordinary phone list's removal paint and focus handoff. A virtual
  // window supplies only mounted rows and never retains offscreen paint.
  const presence = useLayoutPresence(paint, sessionKey, motion.reduced, compact ? 0 : 180, compact ? 0 : 320,
    {...focus, maxRetained: compact ? 0 : undefined, admitNew: !compact});
  const attach = useCallback((element: HTMLDivElement | null) => {
    focus.root.current = element;(presence.root as {current: HTMLDivElement | null}).current = element;
  }, [focus.root, presence.root]);
  useLayoutEffect(() => {
    if (nextFocus.current) {
      const slot = [...(root.current?.querySelectorAll<HTMLElement>('[data-session-id]') ?? [])].find(row => row.dataset.sessionId === nextFocus.current);
      if (slot) {slot.querySelector<HTMLElement>('[data-session-primary]')?.focus({preventScroll: true});nextFocus.current = undefined;}
      else if (!sessions.some(session => session.id === nextFocus.current)) {nextFocus.current = undefined;setFocused(undefined);}
    } else if (focused && !sessions.some(session => session.id === focused)) setFocused(undefined);
  }, [sessions, indices, focused, presence.present]);
  const collapse = () => {setExpanded(false);setViewport({top: 0, height: 660});setFocused(undefined);toggle.current?.focus({preventScroll: true});};
  return <div className={styles.busySessions}>
    {compact && totals && <div className={styles.busyTotals}>
      <div><small>{t('next.dashboard.now')}</small><b>{defaultI18n.number(totals.streams)}</b><span>{t('next.dashboard.paused')}: {defaultI18n.number(totals.paused)}</span></div>
      <div><small>{t('next.kit.converting')}</small><b>{defaultI18n.number(totals.converting)}</b><span>{t('next.dashboard.percent', {value: totals.streams ? Math.round(totals.converting / totals.streams * 100) : 0})}</span></div>
      <div><small>{t('next.dashboard.bandwidth')}</small><b>{fmtMbps(totals.bandwidthKbps / 1000)}</b></div>
    </div>}
    {compact && expanded && <div className={styles.busyHeading}><b>{t('next.dashboard.streams', {count: sessions.length})}</b><Button size="small" variant="text" onClick={collapse}>{t('next.dashboard.fewer')}</Button></div>}
    <div ref={root} className={compact ? styles.busyViewport : undefined} data-expanded={compact && expanded || undefined} tabIndex={-1} role="list" aria-label={t('next.dashboard.now')}
      onScroll={event => {if (expanded) setViewport({top: event.currentTarget.scrollTop, height: event.currentTarget.clientHeight});}}
      onFocusCapture={event => {const id = (event.target as HTMLElement).closest<HTMLElement>('[data-session-id]')?.dataset.sessionId;if (id) setFocused(id);}}
      onBlurCapture={event => {if (!overlays.length && !(event.relatedTarget instanceof Node && event.currentTarget.contains(event.relatedTarget))) setFocused(undefined);}}
      onKeyDown={event => {
        if (!compact || !expanded || event.altKey || event.ctrlKey || event.metaKey || !(event.target as HTMLElement).hasAttribute('data-session-primary')) return;
        if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return;
        const index = sessions.findIndex(session => session.id === focused);
        const next = event.key === 'Home' ? 0 : event.key === 'End' ? sessions.length - 1 : Math.max(0, Math.min(sessions.length - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
        event.preventDefault();nextFocus.current = sessions[next]?.id;setFocused(sessions[next]?.id);
        const element = root.current;if (!element) return;
        const top = next * COMPACT_SESSION_HEIGHT;
        if (top < element.scrollTop) element.scrollTop = top;
        else if (top + COMPACT_SESSION_HEIGHT > element.scrollTop + element.clientHeight) element.scrollTop = top + COMPACT_SESSION_HEIGHT - element.clientHeight;
        setViewport({top: element.scrollTop, height: element.clientHeight});
      }}>
      <div ref={attach} tabIndex={-1} className={compact ? styles.busyExtent : styles.phoneSessionStack} style={compact && expanded ? {height: sessions.length * COMPACT_SESSION_HEIGHT} : undefined}>
        {presence.present.map(session => {const index = sessions.findIndex(value => value.id === session.id), leaving = presence.leaving.has(session.id);
          return <div key={session.id} role="listitem" aria-posinset={index < 0 ? undefined : index + 1} aria-setsize={sessions.length}
          data-layout-item={session.id} data-session-id={session.id} data-leaving={leaving || undefined} inert={leaving} aria-hidden={leaving || undefined}
          className={compact ? undefined : styles.sessionSlot} style={compact && expanded ? {position: 'absolute', top: Math.max(0, index) * COMPACT_SESSION_HEIGHT, left: 0, right: 0} : undefined}>
          <SessionCard session={session} bindings={bindings} layout={compact ? 'compact' : 'phone'} retained={leaving} onOverlayChange={overlay} refresh={id => refresh(session, id)}/>
        </div>;})}
      </div>
    </div>
    {!presence.present.length && empty}
    {compact && <Button ref={toggle} variant="glass" className={styles.busyMore} aria-expanded={expanded} onClick={() => {if (expanded) collapse();else setExpanded(true);}}>
      {expanded ? t('next.dashboard.fewer') : t('next.dashboard.showAll', {count: sessions.length})}
    </Button>}
  </div>;
}
