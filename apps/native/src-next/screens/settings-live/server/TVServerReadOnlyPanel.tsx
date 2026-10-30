import React, {useEffect, useMemo, useState} from 'react';
import {Text, View} from 'react-native';
import {routePath} from '../../../../../../packages/contracts/src/routes';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {settingsBinding, readSettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {parseSettingsConfiguration} from '../../../../../../packages/client-core/src/settings-configuration';
import {parseMetadataSettings} from '../../../../../../packages/client-core/src/metadata-settings';
import {parseSignInSettings} from '../../../../../../packages/client-core/src/account-move';
import {signInLabelKey} from '../../../../../../packages/client-core/src/sign-in';
import {listApiKeys, listWebhooks} from '../../../../../../packages/client-core/src/api-access';
import {fetchPlayHistory} from '../../../../../../packages/client-core/src/play-history';
import {captureReportContext} from '../../../../../../packages/client-core/src/report-context';
import {RemoteSourceService} from '../../../../../../packages/client-core/src/remote-sources';
import {sourceStateText} from '../../../../../../packages/client-core/src/server-admin/storage-words';
import {liveSourceState} from '../../../../../../packages/client-core/src/server-admin/live-words';
import {parseUpdateReport} from '../../../../../../packages/client-core/src/administration';
import {Button, Focusable} from '../../../kit/controls';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeScreenLifetime} from '../lifetime';
import type {NativeSettingsLivePorts} from '../ports';
import {parseLiveSources} from '../live-owner-model';
import {createTVServerReader} from './tv-server-reader';
import type {TVServerPage} from './TVServerPageMenu';
import {tvServerCredentialCount, tvServerSettingsFacts, type TVServerFact} from './tv-server-values';
import {TVDiskUsage} from './TVDiskUsage';

type Page = Exclude<TVServerPage, 'dashboard' | 'libraries' | 'people' | 'remote'>;
type Snapshot = Readonly<{facts: readonly TVServerFact[]; nextCursor?: string}>;

/** All controls here are reads through the same admitted primary-profile lifetime.
 * Only the small display projection survives the read: no secret/document reaches JSX. */
export function TVServerReadOnlyPanel({page, ports, runtime, lifetime, statusOnly = false}: {page: Page; ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; lifetime: NativeScreenLifetime;
  /** The owner edits this page's settings below; only its status facts are drawn here. */
  statusOnly?: boolean}) {
  const theme = useTheme(), t = runtime.text.t;
  const [snapshot, setSnapshot] = useState<Snapshot>(), [error, setError] = useState<unknown>();
  const [revision, setRevision] = useState(0), [cursors, setCursors] = useState<readonly string[]>(['']);
  const cursor = cursors.at(-1) ?? '';
  // A new page owns a fresh fixed report timestamp; the dependency marks that view boundary.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const report = useMemo(() => captureReportContext(Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'), [page]);
  useEffect(() => {
    const abort = new AbortController(), api = createTVServerReader(ports.context, lifetime);
    const current = () => !abort.signal.aborted && lifetime.current();
    const read = (path: string) => api.request<unknown>(path, 'GET', undefined, abort.signal);
    const section = settingsSpec.find(value => value.scope === 'server' && value.id === page);
    const label = (id: string) => {
      const row = section?.groups?.flatMap(group => group.rows).find(value => value.id === id);
      if (!row) throw new Error('native_tv_server_row_missing');
      return t(row.label);
    };
    setSnapshot(undefined); setError(undefined);
    const load = async (): Promise<Snapshot> => {
      if (page === 'signin') {
        const settings = parseSignInSettings(await read(routePath('get_sign_in_settings')));
        return {facts: [
          {id: 'signin.method', label: label(settings.signIn === 'local' ? 'signin.methodLocal' : 'signin.method'), value: t(signInLabelKey(settings.signIn))},
          {id: 'signin.owner', label: label(settings.signIn === 'local' ? 'signin.ownerLocal' : 'signin.owner'), value: settings.owner.username},
        ]};
      }
      if (page === 'integrations') {
        const keys = await tvServerCredentialCount(cursor => listApiKeys(api, cursor, abort.signal), current);
        const webhooks = await listWebhooks(api, abort.signal);
        return {facts: [
          {id: 'integrations.apiKeys', label: label('integrations.apiKeys'), value: defaultI18n.number(keys)},
          {id: 'integrations.webhooks', label: label('integrations.webhooks'), value: defaultI18n.number(webhooks.items.length)},
        ]};
      }
      if (page === 'history') {
        const history = await fetchPlayHistory(api, {...report, period: 'all'}, cursor, abort.signal);
        if (history.nextCursor && (history.nextCursor === cursor || cursors.includes(history.nextCursor))) throw new Error('invalid_play_history_continuation');
        return {facts: history.entries.map(entry => ({id: entry.id, label: entry.title,
          value: [entry.user, entry.profile !== entry.user ? entry.profile : undefined, entry.device].filter(Boolean).join('  '),
          detail: new Intl.DateTimeFormat(runtime.text.locale, {dateStyle: 'medium', timeStyle: 'short', timeZone: report.timeZone}).format(new Date(entry.startedAt)),
        })), nextCursor: history.nextCursor};
      }
      if (page === 'remote-sources') {
        const service = new RemoteSourceService(api, {serverId: lifetime.authority.serverId, viewerId: lifetime.authority.profileId}, async () => {throw new Error('native_tv_server_read_only');}, current);
        try {
          await service.revalidate();
          return {facts: service.getSnapshot().sources.map(source => ({id: source.id, label: source.name, value: sourceStateText(t, defaultI18n.has, source)}))};
        } finally {service.dispose();}
      }
      if (page === 'live') {
        const list = parseLiveSources(await read('/v1/admin/live-sources'), lifetime.authority.serverId);
        return {facts: list.sources.map(source => ({id: source.id, label: source.name, value: t(liveSourceState(source, list.refresh.find(value => value.sourceId === source.id)).id)}))};
      }
      if (!section) throw new Error('native_tv_server_page_missing');
      if (statusOnly) {
        if (page !== 'general') return {facts: []};
        const updates = parseUpdateReport(await read('/v1/admin/updates'));
        return {facts: [{id: 'general.version', label: label('general.version'), value: updates.current.version}]};
      }
      const configuration = parseSettingsConfiguration(await read(routePath('get_settings_configuration')));
      const metadata = page === 'metadata' ? parseMetadataSettings(await read(routePath('get_metadata_settings'))) : undefined;
      const extra: Readonly<Record<string, unknown>> = metadata ? {
        'metadata.online': metadata.onlineLookups, 'metadata.adult': metadata.includeAdult,
        'metadata.language': metadata.defaultLanguage, 'metadata.region': metadata.defaultRegion,
        'metadata.artworkPrefer': metadata.artworkPreference, 'metadata.artworkLanguage': metadata.artworkLanguage,
      } : {};
      const facts = [...tvServerSettingsFacts(section, id => {
        if (Object.hasOwn(extra, id)) return extra[id];
        const binding = settingsBinding(id);
        return binding ? readSettingsBinding(binding, {configuration, forms: metadata ? {metadata} : undefined}, id)?.effective
          : configuration.fields.find(field => field.id === id)?.effective;
      }, t, value => defaultI18n.number(value))];
      if (page === 'general') {
        const updates = parseUpdateReport(await read('/v1/admin/updates'));
        facts.splice(1, 0, {id: 'general.version', label: label('general.version'), value: updates.current.version});
      }
      return {facts};
    };
    void load().then(value => {if (current()) setSnapshot(value);}, failure => {if (current()) setError(failure);});
    return () => abort.abort();
  }, [page, ports, lifetime, runtime.text, t, revision, cursor, cursors, report, statusOnly]);
  if (error !== undefined) return <StateView testID={`tv-server-${page}-error`} {...runtime.error(error, () => setRevision(value => value + 1))}/>;
  if (!snapshot) return <StateView title={t('status.loading')}/>;
  return <View style={statusOnly && snapshot.facts.length ? {marginBottom: 30} : undefined}>
    {snapshot.facts.map(fact => <Focusable key={fact.id} focusId={`tv-server:${page}:${fact.id}`} accessibilityRole="text" accessibilityLabel={[fact.label, fact.value, fact.detail].filter(Boolean).join('. ')} focusKind="row"
      style={{padding: 22, marginBottom: 8, borderRadius: 16, backgroundColor: theme.colors.raised, gap: 8}}>
      <Text style={[shellType(24, 600), {color: theme.colors.text}]}>{fact.label}</Text>
      <Text style={[shellType(22), {color: theme.colors.textSecondary}]}>{fact.value}</Text>
      {fact.detail && <Text style={[shellType(20), {color: theme.colors.textTertiary}]}>{fact.detail}</Text>}
    </Focusable>)}
    {!snapshot.facts.length && !statusOnly && <StateView title={t(page === 'history' ? 'next.settingsSpec.server.history.groups.0.rows.0.control.empty' : page === 'remote-sources' ? 'next.remoteSources.empty' : page === 'live' ? 'web.live.empty.noSourcesTitle' : 'error.unavailable')}/>}
    {cursors.length > 1 && <Button focusId={`tv-server:${page}:previous`} variant="glass" onPress={() => setCursors(value => value.slice(0, -1))}>{t('web.pager.previous')}</Button>}
    {snapshot.nextCursor && <Button focusId={`tv-server:${page}:next`} variant="glass" onPress={() => setCursors(value => [...value, snapshot.nextCursor!])}>{t('web.pager.next')}</Button>}
    {page === 'storage' && <TVDiskUsage context={ports.context} runtime={runtime}/>}
  </View>;
}
