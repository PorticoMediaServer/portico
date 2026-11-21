import {OwnerGeneralSettings} from '../../../../../../packages/client-core/src/server-admin/general-settings';
import {OwnerStreamingSettings} from '../../../../../../packages/client-core/src/server-admin/streaming-settings';
import {OwnerRemoteSettings} from '../../../../../../packages/client-core/src/server-admin/remote-settings';
import {OwnerApiKeysSource} from '../../../../../../packages/client-core/src/server-admin/owner-api-keys';
import {readOwnerSources} from '../../../../../../packages/client-core/src/server-admin/live-sources';
import {readLiveDefaultsDocument} from '../../../../../../packages/client-core/src/server-admin/live-defaults';
import {readOwnerDvrSettings, matchingDvrPreset, DVR_PADDING_PRESETS, DVR_KEEP_PRESETS, DVR_SPACE_PRESETS} from '../../../../../../packages/client-core/src/server-admin/owner-dvr-settings';
import {readNoticesKeep, readServerStorage, storageFacts} from '../../../../../../packages/client-core/src/server-admin/server-storage';
import {readNamedSettings} from '../../../../../../packages/client-core/src/server-admin/server-forms';
import {readServerUpdates} from '../../../../../../packages/client-core/src/server-admin/server-updates';
import {readOwnerSignInSettings, readOwnerMetadataSettings} from '../../../../../../packages/client-core/src/owner-requests';
import {parseOwnerSignInSettings} from '../../../../../../packages/client-core/src/account-move';
import {parseMetadataSettings} from '../../../../../../packages/client-core/src/metadata-settings';
import {signInLabelKey} from '../../../../../../packages/client-core/src/sign-in';
import {listWebhooks} from '../../../../../../packages/client-core/src/api-access';
import {readServerPlayHistory} from '../../../../../../packages/client-core/src/admin/server-play-history';
import type {ReportContext} from '../../../../../../packages/client-core/src/report-context';
import {listRemoteSources} from '../../../../../../packages/client-core/src/remote-storage';
import {liveSourceState} from '../../../../../../packages/client-core/src/server-admin/live-words';
import {settingsSpec, settingsRowIndex} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {settingsBinding, readSettingsBinding} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import type {SettingsDocumentAPI} from '../../../../../../packages/client-core/src/settings-document';
import type {HttpLocalApi} from '../../../../../../packages/client-core/src';
import {sourceStateText} from '../../../../../../packages/client-core/src/server-admin/storage-words';
import {liveDefaultsView} from '../live-owner-model';
import {liveStabilityValue} from '../live-defaults-presentation';
import type {TVServerPage} from './tv-server-pages';
import {tvServerCredentialCount, tvServerSettingsFacts, type TVServerFact} from './tv-server-values';

export type TVReadOnlyPage = Exclude<TVServerPage, 'dashboard' | 'libraries' | 'people'>;
export type TVPageSnapshot = Readonly<{facts: readonly TVServerFact[]; nextCursor?: string}>;
type Api = SettingsDocumentAPI & Pick<HttpLocalApi, 'requestRaw'>;

/** Only the display projection leaves these page readers: no key, secret or raw configuration. */
export async function readTVServerPage(page: TVReadOnlyPage, api: Api, current: () => boolean, report: ReportContext, cursor: string, serverId: string, t: typeof defaultI18n.t, locale: string, signal: AbortSignal): Promise<TVPageSnapshot> {
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === page)!;
  const label = (id: string) => t(settingsRowIndex.get(id)!.row.label);
  const facts = (read: (id: string) => unknown) => tvServerSettingsFacts(section, read, t, value => defaultI18n.number(value));
  if (page === 'signin') {
    const settings = parseOwnerSignInSettings(await readOwnerSignInSettings(api, signal));
    return {facts: [{id: 'signin.method', label: label(settings.signIn === 'local' ? 'signin.methodLocal' : 'signin.method'), value: t(signInLabelKey(settings.signIn))},
      {id: 'signin.owner', label: label(settings.signIn === 'local' ? 'signin.ownerLocal' : 'signin.owner'), value: settings.owner.username}]};
  }
  if (page === 'integrations') {
    let failure: unknown;
    const keys = new OwnerApiKeysSource(api, current, error => {failure = error;});
    signal.addEventListener('abort', keys.dispose, {once: true});
    try {
      const count = await tvServerCredentialCount(async next => {
        await keys.load(next); const state = keys.getSnapshot();
        if (failure) throw failure;
        if (state.phase !== 'ready' || !state.page) throw new Error('native_tv_server_keys_unavailable');
        return state.page;
      }, current);
      const webhooks = await listWebhooks(api, signal);
      return {facts: [{id: 'integrations.apiKeys', label: label('integrations.apiKeys'), value: defaultI18n.number(count)},
        {id: 'integrations.webhooks', label: label('integrations.webhooks'), value: defaultI18n.number(webhooks.items.length)}]};
    } finally {signal.removeEventListener('abort', keys.dispose);keys.dispose();}
  }
  if (page === 'history') {
    const history = await readServerPlayHistory(api, {period: 'all'}, cursor, signal);
    return {facts: history.entries.map(entry => ({id: entry.id, label: entry.title,
      value: [entry.user, entry.profile !== entry.user ? entry.profile : undefined, entry.device].filter(Boolean).join('  '),
      detail: new Intl.DateTimeFormat(locale, {dateStyle: 'medium', timeStyle: 'short', timeZone: report.timeZone}).format(new Date(entry.startedAt))})), nextCursor: history.nextCursor};
  }
  if (page === 'remote-sources') {
    const sources = await listRemoteSources(api, signal);
    return {facts: sources.map(source => ({id: source.id, label: source.name, value: sourceStateText(t, defaultI18n.has, source)}))};
  }
  if (page === 'live') {
    const [sources, live, dvr] = await Promise.all([readOwnerSources(api, signal), readLiveDefaultsDocument(api, signal), readOwnerDvrSettings(api, signal)]);
    const values: Record<string, unknown> = {'live.stability': liveStabilityValue(liveDefaultsView(live).settings), 'live.guideDays': live.value.guideDays,
      'live.rewind': String(live.value.rewindMinutes), 'live.logos': live.value.importImages, 'live.discovery': live.value.discoveryEnabled,
      'live.buffer': live.value.bufferSeconds, 'live.retry': live.value.retrySeconds,
      'live.rec.extra': matchingDvrPreset(DVR_PADDING_PRESETS, dvr.value), 'live.rec.keep': matchingDvrPreset(DVR_KEEP_PRESETS, dvr.value),
      'live.rec.space': matchingDvrPreset(DVR_SPACE_PRESETS, dvr.value), 'live.rec.conflict': dvr.value.stopViewers ? 'stop' : 'keep',
      'live.rec.early': dvr.value.beforeMs / 60000, 'live.rec.late': dvr.value.afterMs / 60000, 'live.rec.days': dvr.value.keepDays,
      'live.rec.episodes': dvr.value.keepNewest, 'live.rec.floor': dvr.value.floorBytes / 1024 ** 3, 'live.rec.cap': dvr.value.capBytes / 1024 ** 3};
    return {facts: [...sources.sources.map(source => ({id: source.id, label: source.name, value: t(liveSourceState({state: source.paused ? 'paused' : source.state}, undefined).id)})), ...facts(id => values[id])]};
  }
  if (page === 'remote') {
    const source = new OwnerRemoteSettings(api, current);
    try {
      const configuration = await source.readConfiguration(signal), status = source.getSnapshot().status;
      const rows = [...facts(id => configuration.fields.find(field => field.id === id)?.effective)];
      if (status) {
        const outside = status.outside, answers = outside?.addresses ?? [];
        rows.unshift({id: 'remote.thisDevice', label: label('remote.thisDevice'), value: t(status.thisDevice.atHome ? 'next.remoteStatus.deviceHome' : 'next.remoteStatus.deviceAway', {address: status.thisDevice.address})},
          {id: 'remote.outside', label: t('settings.server.remote'), value: t(!outside?.checkedAt ? 'next.remoteOwner.notChecked' : answers.length && answers.every(answer => answer.reachable) ? 'next.remoteOwner.reachable' : answers.length ? 'next.remoteOwner.unreachable' : 'next.remoteOwner.unknown')});
        if (status.managedCertificate) rows.push({id: 'remote.secureStatus', label: label('remote.secureStatus'), value: t(status.managedCertificate.ready ? 'next.remoteOwner.serving' : 'next.remoteOwner.waiting')});
      }
      return {facts: rows};
    } finally {source.dispose();}
  }
  if (page === 'metadata') {
    const value = parseMetadataSettings(await readOwnerMetadataSettings(api, signal));
    const values: Record<string, unknown> = {'metadata.online': value.onlineLookups, 'metadata.adult': value.includeAdult,
      'metadata.language': value.defaultLanguage, 'metadata.region': value.defaultRegion, 'metadata.artworkPrefer': value.artworkPreference,
      'metadata.artworkLanguage': value.artworkLanguage, 'metadata.refresh': value.advanced?.refreshPolicy,
      'metadata.fallbackLanguage': value.advanced?.fallbackLanguage, 'metadata.animeTitles': value.advanced?.animeTitles,
      'metadata.artworkLimit': value.localArtworkLimitBytes === undefined ? undefined : String(value.localArtworkLimitBytes / 2 ** 30),
      'metadata.artworkSize': value.artworkMaxEdge === undefined ? undefined : value.artworkMaxEdge === 1280 ? 'small' : value.artworkMaxEdge === 2560 ? 'large' : 'standard'};
    return {facts: facts(id => values[id])};
  }
  if (page === 'segments') {
    const forms = {segments: (await readNamedSettings(api, 'segments', signal)).value};
    return {facts: facts(id => {const binding = settingsBinding(id);return binding ? readSettingsBinding(binding, {forms}, id)?.effective : undefined;})};
  }
  if (page === 'storage') {
    const [notices, storage] = await Promise.all([readNoticesKeep(api, signal), readServerStorage(api, signal)]);
    const usage = storageFacts(storage), rows = [...facts(id => id === 'storage.keepNotifications' ? String(notices.days) : undefined)];
    if (usage.freeBytes !== undefined) rows.push({id: 'storage.free', label: t('server.dashboard.freeSpace'), value: defaultI18n.bytes(usage.freeBytes)});
    rows.push({id: 'storage.own', label: t('server.dashboard.porticoFiles'), value: defaultI18n.bytes((storage.folders ?? []).reduce((sum, folder) => sum + folder.bytes, 0))});
    if (usage.databaseBytes !== undefined) rows.push({id: 'storage.database', label: t('server.space.database'), value: defaultI18n.bytes(usage.databaseBytes)});
    return {facts: rows};
  }
  const configuration = page === 'general' ? await new OwnerGeneralSettings(api).readConfiguration(signal) : await new OwnerStreamingSettings(api).readConfiguration(signal);
  const rows = [...facts(id => configuration.fields.find(field => field.id === id)?.effective)];
  if (page === 'general') {
    const updates = await readServerUpdates(api, signal);
    rows.splice(1, 0, {id: 'general.version', label: label('general.version'), value: updates.version}, {id: 'general.serverId', label: label('general.serverId'), value: serverId});
  }
  return {facts: rows};
}
