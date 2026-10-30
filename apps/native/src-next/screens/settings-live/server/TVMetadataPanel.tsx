import React, {useCallback, useEffect, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import {artworkLimitText, openSubtitlesSummary, type MetadataSettings} from '../../../../../../packages/client-core/src/metadata-settings';
import {settingsRowIndex, settingsSpec, type SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls';
import {SettingsRow as Row} from '../../../kit/shell';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {NativeMetadataController, type MetadataPatch} from './metadata-controller';
import {TVChoiceRow, TVEntryRow, TVSwitchRow} from './TVSettingRows';

type Translate = typeof defaultI18n.t;
const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'metadata');
const row = (id: string): SettingsRow => settingsRowIndex.get(id)!.row;

/** Settings › Server › Metadata on TV through the phone's own metadata controller: every
 * change is one revisioned save, and a key or password is only ever replaced, never read back. */
export function TVMetadataPanel({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const scope = useSettingsRequestScope(ports.context);
  const current = useCallback(() => {
    const {account} = ports.context;
    return scope.lifetime.current() && account.canManage && account.account.role === 'owner' && !account.account.disabled && account.account.primaryProfileId === scope.authority.profileId;
  }, [scope, ports]);
  const [owned, setOwned] = useState<{controller: NativeMetadataController; scope: typeof scope}>();
  useEffect(() => {
    const controller = new NativeMetadataController(scope.api, current); setOwned({controller, scope}); void controller.load();
    return () => controller.dispose();
  }, [scope, current]);
  return owned && owned.scope === scope ? <Panel controller={owned.controller} current={current} runtime={runtime}/> : <StateView title={runtime.text.t('status.loading')}/>;
}

function Panel({controller, current, runtime}: {controller: NativeMetadataController; current: () => boolean; runtime: NativeShellRuntime}) {
  const theme = useTheme(), t = runtime.text.t as Translate;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), settings = state.data;
  const [advanced, setAdvanced] = useState(false);
  if (!settings) return state.error !== undefined ? <StateView {...runtime.error(state.error, () => {void controller.load();})}/> : <StateView title={t('status.loading')}/>;
  const blocked = state.busy || state.needsReload;
  const save = (patch: MetadataPatch) => {if (!blocked && current()) void controller.save(settings, patch);};
  const copy = (id: string, help?: string) => ({testID: id, label: t(row(id).label), description: help ?? (row(id).help ? t(row(id).help!) : undefined)});
  const toggle = (id: string, value: boolean | undefined, patch: (value: boolean) => MetadataPatch) =>
    value === undefined ? null : <TVSwitchRow key={id} {...copy(id)} checked={value} onChange={next => save(patch(next))}/>;
  const select = (id: string, value: string | undefined, patch: (value: string) => MetadataPatch, help?: string) => {
    if (value === undefined) return null;
    const options = (row(id).control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined}));
    if (!options.some(option => option.value === value)) options.push({value, label: value, description: undefined});
    return <TVChoiceRow key={id} {...copy(id, help)} value={value} options={options} onChange={next => {if (row(id).control.options?.some(option => String(option.value) === next)) save(patch(next));}}/>;
  };
  // Keys and passwords are written and never read back. A username is shown, unless it is an
  // email address, which a shared screen never draws.
  const secret = (id: string, key: 'tmdbReadToken' | 'tvdbProjectKey' | 'openSubtitlesApiKey' | 'openSubtitlesPassword' | 'acoustidKey', configured: boolean, unset?: string) =>
    <TVEntryRow key={id} {...copy(id)} kind="secret" configured={configured} unset={unset} t={t} onSave={value => {if (typeof value === 'string' && value) save({[key]: value});}}/>;
  const username = settings.openSubtitlesUsername;
  const group = (title: number, rows: React.ReactNode[]) => {
    const shown = rows.filter(Boolean);
    if (!shown.length) return null;
    const heading = section?.groups?.[title]?.title;
    return <View key={title} style={{marginBottom: 30}}>
      {heading && <Text style={[shellType(20, 700), {color: theme.colors.textTertiary, marginLeft: 24, marginBottom: 10}]}>{t(heading)}</Text>}
      <View style={{backgroundColor: theme.colors.raised, borderRadius: 18}}>{shown}</View>
    </View>;
  };
  const subtitles = openSubtitlesSummary(settings.openSubtitles, {...defaultI18n, t});
  const extra = settings.advanced;
  return <View testID="tv-server-metadata-settings">
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {void controller.load();})}/>}
    {group(0, [
      toggle('metadata.online', settings.onlineLookups, onlineLookups => ({onlineLookups})),
      toggle('metadata.adult', settings.includeAdult, includeAdult => ({includeAdult})),
    ])}
    {group(1, [
      select('metadata.language', settings.defaultLanguage, defaultLanguage => ({defaultLanguage})),
      select('metadata.region', settings.defaultRegion, defaultRegion => ({defaultRegion})),
    ])}
    {group(2, [
      secret('metadata.tmdb', 'tmdbReadToken', settings.tmdbCredentialConfigured, t('server.metadata.keyOurs')),
      secret('metadata.tvdb', 'tvdbProjectKey', settings.tvdbCredentialConfigured, t('server.metadata.keyOurs')),
    ])}
    {group(3, [
      select('metadata.artworkPrefer', settings.artworkPreference, value => ({artworkPreference: value as MetadataSettings['artworkPreference']})),
      select('metadata.artworkLanguage', settings.artworkLanguage, value => ({artworkLanguage: value as MetadataSettings['artworkLanguage']})),
    ])}
    {group(4, [
      secret('metadata.osKey', 'openSubtitlesApiKey', settings.openSubtitlesKeyConfigured),
      username.includes('@')
        ? <TVEntryRow key="metadata.osUser" {...copy('metadata.osUser')} kind="secret" configured t={t} onSave={value => {if (typeof value === 'string' && value) save({openSubtitlesUsername: value});}}/>
        : <TVEntryRow key="metadata.osUser" {...copy('metadata.osUser')} kind="text" value={username} t={t} onSave={value => {if (typeof value === 'string') save({openSubtitlesUsername: value});}}/>,
      secret('metadata.osPass', 'openSubtitlesPassword', settings.openSubtitlesPasswordConfigured),
      <Row key="metadata.osStatus" testID="metadata.osStatus" readable label={t(row('metadata.osStatus').label)} description={subtitles.downloads} value={subtitles.text}/>,
      <Row key="metadata.osTest" label={t(row('metadata.osTest').label)} control={<Button testID="metadata.osTest" variant="glass" size="small" disabled={blocked}
        onPress={() => {if (current()) void controller.checkOpenSubtitles(settings);}}>{t(row('metadata.osTest').control.label!)}</Button>}/>,
    ])}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 20, marginBottom: 24}}>
      <Button testID="tv-server-metadata-advanced" variant="glass" size="small" trailingIcon={advanced ? 'i-up' : 'i-down'} onPress={() => setAdvanced(value => !value)}>{t('settings.section.advanced')}</Button>
    </View>
    {advanced && <View style={{backgroundColor: theme.colors.raised, borderRadius: 18, marginBottom: 30}}>
      {select('metadata.artworkLimit', artworkLimitText(settings.localArtworkLimitBytes), value => ({localArtworkLimitBytes: Number(value) * 2 ** 30}),
        t('server.metadata.artworkUsedHelp', {used: artworkLimitText(settings.localArtworkUsedBytes)}))}
      {select('metadata.artworkSize', settings.artworkMaxEdge === 1280 ? 'small' : settings.artworkMaxEdge === 2560 ? 'large' : 'standard', value => ({artworkMaxEdge: value === 'small' ? 1280 : value === 'large' ? 2560 : 1920}))}
      {/* Refresh all is an action, and actions stay off TV (Q37). */}
      {extra && select('metadata.refresh', extra.refreshPolicy, value => ({refreshPolicy: value as typeof extra.refreshPolicy}))}
      {extra && select('metadata.fallbackLanguage', extra.fallbackLanguage, value => ({fallbackLanguage: value as typeof extra.fallbackLanguage}))}
      {extra && select('metadata.animeTitles', extra.animeTitles, value => ({animeTitles: value as typeof extra.animeTitles}))}
      {toggle('metadata.castPhotos', extra?.castPhotos, castPhotos => ({castPhotos}))}
      {extra && secret('metadata.acoustid', 'acoustidKey', extra.acoustidKeyConfigured, extra.acoustidKeySource === 'environment' ? t('server.metadata.acoustidServer') : undefined)}
    </View>}
  </View>;
}
