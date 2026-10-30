import {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import type {PlayHistoryEntry, PlayHistoryPeriod} from '@core/play-history.ts';
import {SettingsGroup, SettingsTable, type SettingsTableRow, type TableQuery} from '../../kit/settings';
import type {DashboardBindings, DashboardFilters} from './bindings';
import {useDashboardAccounts, useDashboardAuthority, useDashboardRead} from './use-read';
import {mediaKinds} from './model';
import {formatHistoryDuration, formatHistoryStarted} from './format';
import {mediaLabel, ReadFeedback, t} from './shared';
import styles from './dashboard.module.css';
export function HistoryScreen({bindings: b, embedded = false}: {bindings: DashboardBindings; embedded?: boolean}) {
  const current = useDashboardAuthority(b);
  if (!current || !b.isMainProfile() || b.role !== 'owner') return null;
  return <div className={styles.dashboard}>{!embedded && <h2 className={styles.pageTitle}>{t('next.dashboard.history')}</h2>}<HistoryTable key={JSON.stringify([b.scope.serverId, b.scope.viewerId])} bindings={b} /></div>;
}
function HistoryTable({bindings: b}: {bindings: DashboardBindings}) {
  const [query, setQuery] = useState<TableQuery>({search: '', filters: {media: 'all', person: 'all', range: 'all'}, sort: {by: 'played', direction: 'desc'}});
  const [settledQuery, settle] = useState(query);
  type Results = {context: DashboardBindings['context']; query: TableQuery; cursor: string; rows: readonly PlayHistoryEntry[]; total?: number};
  const [results, setResults] = useState<Results>({context: b.context, query: settledQuery, cursor: '', rows: []});
  // A new context or settled query must never reuse a previous continuation,
  // count or row. Keep the controls mounted so typing and filters survive.
  const {cursor, rows, total} = results.context === b.context && results.query === settledQuery ? results : {cursor: '', rows: [], total: undefined};
  const setCursor = useCallback((cursor: string) => setResults(previous => ({...(previous.context === b.context && previous.query === settledQuery ? previous : {context: b.context, query: settledQuery, rows: []}), cursor})), [b.context, settledQuery]);
  const seen = useRef<unknown>(undefined);
  useEffect(() => {if (query === settledQuery) return; const timer = setTimeout(() => settle(query), query.search === settledQuery.search ? 0 : 250); return () => clearTimeout(timer);}, [query, settledQuery]);
  const filters: DashboardFilters = useMemo(() => ({period: settledQuery.filters.range as PlayHistoryPeriod, ...(settledQuery.filters.media === 'all' ? {} : {media: settledQuery.filters.media}), ...(settledQuery.filters.person === 'all' ? {} : {accountId: settledQuery.filters.person}), search: settledQuery.search, sort: ({played: 'started', for: 'watched', where: 'location'} as Record<string,string>)[settledQuery.sort.by] ?? settledQuery.sort.by, direction: settledQuery.sort.direction}), [settledQuery]);
  const load = useCallback((signal: AbortSignal) => b.read.plays(filters, cursor, signal), [b, filters, cursor]);
  const state = useDashboardRead(b, load, true, cursor ? 0 : 30000);
  useEffect(() => {
    const page = state.value; if (!page || page === seen.current) return; seen.current = page;
    setResults(previous => {
      const old = previous.context === b.context && previous.query === settledQuery ? previous : {rows: [], total: undefined};
      return {context: b.context, query: settledQuery, cursor, rows: cursor ? [...old.rows, ...page.entries.filter(entry => !old.rows.some(previous => previous.id === entry.id))] : page.entries, total: page.total ?? old.total};
    });
  }, [state.value, cursor, b.context, settledQuery]);
  const people = useDashboardAccounts(b);
  const failed = state.failed || people.failed;
  useEffect(() => {const reset = () => setCursor(''); const offs = ['session.updated', 'playback.history'].map(type => b.events?.on(type, reset)); offs.push(b.events?.onResync?.(reset)); return () => offs.forEach(off => off?.());}, [b, setCursor]);
  const tableRows = useMemo(() => rows.map((entry): SettingsTableRow => {
    const artwork = entry.itemId ? b.artwork(entry.itemId, entry.kind === 'song' || entry.kind === 'audiobook_file' ? 'square' : 'poster') : undefined;
    return {id: entry.id, label: entry.title, cells: {
      title: {title: (entry.kind === 'episode' || entry.kind === 'audiobook_file') && entry.parentTitle ? entry.parentTitle : entry.title, meta: entry.kind === 'episode' ? [entry.episode !== undefined && (entry.season === undefined ? t('title.episodeOnly', {episode: entry.episode}) : t('title.episodeCode', {season: entry.season, episode: entry.episode})), entry.title].filter(Boolean).join(' ') : entry.kind === 'audiobook_file' ? undefined : entry.parentTitle, artwork},
      person: {title: entry.user, avatar: entry.user.slice(0, 1).toUpperCase(), avatarKey: entry.user, meta: entry.profile !== entry.user ? entry.profile : undefined},
      // The platform wire identifier is not an app display name. Actual app waits for the checked producer.
      player: {title: '', meta: entry.device},
      played: {title: formatHistoryStarted(entry.startedAt, b.context.timeZone, b.context.until)},
      for: {title: entry.completed ? t('next.dashboard.finished') : entry.watchedMs !== undefined ? formatHistoryDuration(entry.watchedMs / 1000) : '', ...(entry.durationSeconds > 0 ? {meta: entry.completed ? formatHistoryDuration(entry.durationSeconds) : t('next.dashboard.ofDuration', {duration: formatHistoryDuration(entry.durationSeconds, true)})} : entry.kind === 'channel' ? {meta: t('next.dashboard.live')} : {})},
      ...(entry.location === 'local' || entry.location === 'remote' ? {where: {title: t(entry.location === 'local' ? 'next.dashboard.home' : 'next.dashboard.away'), tone: 'quiet' as const}} : {}),
    }};
  }), [rows, b]);
  return <SettingsGroup title={t('next.dashboard.playsHeading')} plain><ReadFeedback openHome={b.effects.openHome} state={failed ? {loading: false, failed: true, refresh: () => {state.refresh(); people.refresh();}} : state} />{!failed && <SettingsTable label={t('next.dashboard.history')} searchLabel={t('next.dashboard.search')} rows={tableRows}
    columns={[
      {id: 'title', label: t('next.dashboard.titleColumn'), kind: 'main', sortable: true, width: '27%'},
      {id: 'person', label: t('next.dashboard.person'), kind: 'main', sortable: true, width: '15%'},
      {id: 'player', label: t('next.dashboard.player'), kind: 'text', sortable: true, width: '18%'},
      {id: 'played', label: t('next.dashboard.played'), kind: 'text', sortable: true, width: '17%'},
      {id: 'for', label: t('next.dashboard.playedFor'), kind: 'text', sortable: true, width: '12%'},
      {id: 'where', label: t('next.dashboard.where'), kind: 'badge', sortable: true, width: '11%'},
    ]} query={query} onQueryChange={setQuery} filters={[
      {id: 'media', label: t('next.dashboard.media'), options: ['all', ...mediaKinds].map(kind => ({value: kind, label: mediaLabel(kind)}))},
      {id: 'person', label: t('next.dashboard.person'), options: [{value: 'all', label: t('next.dashboard.everyone')}, ...people.items.map(p => ({value: p.accountId, label: p.username}))]},
      {id: 'range', label: t('next.dashboard.range'), options: ['today', '7d', '30d', 'all'].map(value => ({value, label: t(`next.dashboard.${value}` as 'next.dashboard.all')}))},
    ]} height={640} countLabel={total === undefined ? undefined : t('next.dashboard.plays', {count: total})} matchedCount={total}
    emptyLabel={t('next.dashboard.noMatch')} loading={state.loading} hasMore={rows.length > 0 && !!state.value?.nextCursor} onLoadMore={() => setCursor(state.value!.nextCursor)} />}
  </SettingsGroup>;
}
