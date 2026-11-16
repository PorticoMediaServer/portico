import React, {useEffect, useState} from 'react';
import {Text, View} from 'react-native';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import {Button, Focusable} from '../../../kit/controls';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import {createTVServerReader} from './tv-server-reader';
import {readTVServerPage, type TVReadOnlyPage, type TVPageSnapshot} from './tv-server-page-data';

/** A TV page retains display facts only. Settings readers have a GET-only transport. */
export function TVServerReadOnlyPanel({page, ports, runtime, lifetime}: {page: TVReadOnlyPage; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime}) {
  const theme = useTheme(), t = runtime.text.t;
  const [snapshot, setSnapshot] = useState<TVPageSnapshot>(), [error, setError] = useState<unknown>();
  const [revision, setRevision] = useState(0), [cursors, setCursors] = useState<readonly string[]>(['']);
  const cursor = cursors.at(-1) ?? '';
  // Each mounted page owns a fixed report timestamp.
  const [report] = useState(() => captureReportContext(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'));
  useEffect(() => {
    const abort = new AbortController(), api = createTVServerReader(ports.context, lifetime);
    const current = () => !abort.signal.aborted && lifetime.current();
    setSnapshot(undefined);setError(undefined);
    const load = async () => {
      const value = await readTVServerPage(page, api, current, report, cursor, lifetime.authority.serverId, t, runtime.text.locale ?? 'en-US', abort.signal);
      if (value.nextCursor && (value.nextCursor === cursor || cursors.includes(value.nextCursor))) throw new Error('invalid_play_history_continuation');
      return value;
    };
    void load().then(value => {if (current()) setSnapshot(value);}, failure => {if (current()) setError(failure);});
    return () => abort.abort();
  }, [page, ports, lifetime, runtime.text, t, revision, cursor, cursors, report]);
  if (error !== undefined) return <StateView testID={`tv-server-${page}-error`} {...runtime.error(error, () => setRevision(value => value + 1))}/>;
  if (!snapshot) return <StateView title={t('status.loading')}/>;
  return <View>
    {snapshot.facts.map(fact => <Focusable key={fact.id} focusId={`tv-server:${page}:${fact.id}`} accessibilityRole="text" accessibilityLabel={[fact.label, fact.value, fact.detail].filter(Boolean).join('. ')} focusKind="row"
      style={{padding: 22, marginBottom: 8, borderRadius: 16, backgroundColor: theme.colors.raised, gap: 8}}>
      <Text style={[shellType(24, 600), {color: theme.colors.text}]}>{fact.label}</Text>
      <Text style={[shellType(22), {color: theme.colors.textSecondary}]}>{fact.value}</Text>
      {fact.detail && <Text style={[shellType(20), {color: theme.colors.textTertiary}]}>{fact.detail}</Text>}
    </Focusable>)}
    {!snapshot.facts.length && <StateView title={t(page === 'history' ? 'next.settingsSpec.server.history.groups.0.rows.0.control.empty' : page === 'remote-sources' ? 'next.remoteSources.empty' : page === 'live' ? 'web.live.empty.noSourcesTitle' : 'error.unavailable')}/>}
    {cursors.length > 1 && <Button focusId={`tv-server:${page}:previous`} variant="glass" onPress={() => setCursors(value => value.slice(0, -1))}>{t('web.pager.previous')}</Button>}
    {snapshot.nextCursor && <Button focusId={`tv-server:${page}:next`} variant="glass" onPress={() => setCursors(value => [...value, snapshot.nextCursor!])}>{t('web.pager.next')}</Button>}
  </View>;
}
