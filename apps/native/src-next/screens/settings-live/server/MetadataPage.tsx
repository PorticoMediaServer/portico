import React, {useCallback, useEffect, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {artworkLimitText, openSubtitlesSummary, type MetadataSettings} from '../../../../../../packages/client-core/src/metadata-settings';
import {settingsSpec, settingsRowIndex, type SettingsRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button} from '../../../kit/controls/Button';
import {Select} from '../../../kit/controls/Select';
import {Switch} from '../../../kit/controls/Switch';
import {TextField} from '../../../kit/controls/TextField';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {LibraryGroup, LibraryRow, SectionTitle} from '../owner-libraries/controls';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeMetadataController, type MetadataPatch} from './metadata-controller';
import {settingsPageHeading, settingsPageInset} from './page-layout';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean; onLibraries: () => void};
type CredentialKey = 'tmdbReadToken' | 'tvdbProjectKey' | 'openSubtitlesApiKey' | 'openSubtitlesUsername' | 'openSubtitlesPassword' | 'acoustidKey';
const credentialKeys: Readonly<Record<string, CredentialKey>> = {'metadata.tmdb': 'tmdbReadToken', 'metadata.tvdb': 'tvdbProjectKey', 'metadata.osKey': 'openSubtitlesApiKey', 'metadata.osUser': 'openSubtitlesUsername', 'metadata.osPass': 'openSubtitlesPassword', 'metadata.acoustid': 'acoustidKey'};
const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'metadata');

/** The Metadata settings producer supplies both values and write-only credential status. */
export function NativeServerMetadata(props: Props) {
  const scope = useSettingsRequestScope(props.ports.context);
  const current = useCallback(() => {
    const {account} = props.ports.context;
    return scope.lifetime.current() && account.canManage && account.account.role === 'owner' && !account.account.disabled && account.account.primaryProfileId === scope.authority.profileId;
  }, [scope, props.ports]);
  const [owned, setOwned] = useState<{controller: NativeMetadataController; scope: typeof scope}>();
  useEffect(() => {
    const controller = new NativeMetadataController(scope.api, current); setOwned({controller, scope}); void controller.load();
    return () => controller.dispose();
  }, [scope, current]);
  return owned && owned.scope === scope ? <MetadataPage {...props} controller={owned.controller} current={current}/>
    : <StateView title={props.runtime.text.t('next.ownerLibraries.loading')}/>;
}

function MetadataPage({ports, runtime, compactHeader, onLibraries, controller, current}: Props & {controller: NativeMetadataController; current: () => boolean}) {
  const theme = useTheme(), t = runtime.text.t as typeof defaultI18n.t;
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot), settings = state.data;
  const [editing, setEditing] = useState<{row: SettingsRow; original: MetadataSettings; key: CredentialKey}>(), [text, setText] = useState('');
  const [confirmRefresh, setConfirmRefresh] = useState(false);
  const disabled = state.busy || state.needsReload;
  const save = (patch: MetadataPatch) => {if (settings && current()) void controller.save(settings, patch);};
  const row = (id: string) => settingsRowIndex.get(id)!.row;
  const groupTitle = (index: number) => {const title = section?.groups?.[index]?.title; return title ? t(title) : '';};
  const options = (definition: SettingsRow, value: string) => {
    const offered = (definition.control.options ?? []).map(option => ({value: String(option.value), label: t(option.label), description: option.hint ? t(option.hint) : undefined}));
    if (!offered.some(option => option.value === value)) offered.push({value, label: value, description: undefined});
    return offered;
  };
  const toggle = (id: string, value: boolean | undefined, patch: (value: boolean) => MetadataPatch, first = false) => {
    if (value === undefined) return null;
    const definition = row(id);
    return <LibraryRow key={id} first={first} label={t(definition.label)} help={definition.help ? t(definition.help) : undefined}
      trailing={<Switch testID={id} accessibilityLabel={t(definition.label)} checked={value} disabled={disabled} onCheckedChange={next => save(patch(next))}/>}/>;
  };
  const select = (id: string, value: string | undefined, patch: (value: string) => MetadataPatch, first = false, help?: string) => {
    if (value === undefined) return null;
    const definition = row(id);
    return <LibraryRow key={id} first={first} label={t(definition.label)} help={help ?? (definition.help ? t(definition.help) : undefined)}
      trailing={<Select testID={id} accessibilityLabel={t(definition.label)} value={value} disabled={disabled} options={options(definition, value)} onValueChange={next => {
        if (definition.control.options?.some(option => String(option.value) === next)) save(patch(next));
      }}/>} />;
  };
  const credential = (id: string, configured: boolean, first = false) => {
    const definition = row(id), key = credentialKeys[id]!;
    const status = key === 'openSubtitlesUsername' ? settings?.openSubtitlesUsername || 'No account set' : configured ? 'Your own value is saved' : key === 'tmdbReadToken' || key === 'tvdbProjectKey' ? t('server.metadata.keyOurs')
      : key === 'acoustidKey' && settings?.advanced?.acoustidKeySource === 'environment' ? t('server.metadata.acoustidServer') : 'Not set up';
    return <LibraryRow key={id} first={first} label={t(definition.label)} help={definition.help ? t(definition.help) : undefined} value={status}
      trailing={<Button variant="text" size="xs" disabled={disabled} onPress={() => {if (settings && current()) {setEditing({row: definition, original: settings, key}); setText(key === 'openSubtitlesUsername' ? settings.openSubtitlesUsername : '');}}}>{t('web.metadataLookup.change')}</Button>}/>;
  };
  if (!settings) return state.error !== undefined ? <StateView {...runtime.error(state.error, () => {void controller.load();}, 'server-console')}/>
    : <StateView title={t('next.ownerLibraries.loading')}/>;
  const subtitles = openSubtitlesSummary(settings.openSubtitles, {...defaultI18n, t});
  const advanced = settings.advanced;
  const advancedTitle = (index: number) => {const title = section?.advanced?.groups?.[index]?.title; return title ? t(title) : '';};
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
      <ServerHeader serverName={ports.context.serverName} viewerRole="owner" audience="owner" variant="phone" style={{marginBottom: 12}}/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.metadata')}</Text>}
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {void controller.load();}, 'server-console')}/>}
      <SectionTitle>{groupTitle(0)}</SectionTitle><LibraryGroup>
        {toggle('metadata.online', settings.onlineLookups, onlineLookups => ({onlineLookups}), true)}
        {toggle('metadata.adult', settings.includeAdult, includeAdult => ({includeAdult}), settings.onlineLookups === undefined)}
        <LibraryRow label={t(row('metadata.perLibrary').label)} help={row('metadata.perLibrary').help ? t(row('metadata.perLibrary').help!) : undefined} onPress={onLibraries}/>
      </LibraryGroup>
      {(settings.defaultLanguage !== undefined || settings.defaultRegion !== undefined) && <><SectionTitle>{groupTitle(1)}</SectionTitle><LibraryGroup>
        {select('metadata.language', settings.defaultLanguage, defaultLanguage => ({defaultLanguage}), true)}
        {select('metadata.region', settings.defaultRegion, defaultRegion => ({defaultRegion}), settings.defaultLanguage === undefined)}
      </LibraryGroup></>}
      <SectionTitle>{groupTitle(2)}</SectionTitle><LibraryGroup>
        {credential('metadata.tmdb', settings.tmdbCredentialConfigured, true)}
        {credential('metadata.tvdb', settings.tvdbCredentialConfigured)}
        <LibraryRow label={t(row('metadata.noKey').label)} help={row('metadata.noKey').help ? t(row('metadata.noKey').help!) : undefined} value={row('metadata.noKey').control.text ? t(row('metadata.noKey').control.text!) : undefined}/>
      </LibraryGroup>
      {(settings.artworkPreference !== undefined || settings.artworkLanguage !== undefined) && <><SectionTitle>{groupTitle(3)}</SectionTitle><LibraryGroup>
        {select('metadata.artworkPrefer', settings.artworkPreference, value => ({artworkPreference: value as MetadataSettings['artworkPreference']}), true)}
        {select('metadata.artworkLanguage', settings.artworkLanguage, value => ({artworkLanguage: value as MetadataSettings['artworkLanguage']}), settings.artworkPreference === undefined)}
      </LibraryGroup></>}
      <SectionTitle>{groupTitle(4)}</SectionTitle><LibraryGroup>
        {credential('metadata.osKey', settings.openSubtitlesKeyConfigured, true)}
        {credential('metadata.osUser', !!settings.openSubtitlesUsername)}
        {credential('metadata.osPass', settings.openSubtitlesPasswordConfigured)}
        <LibraryRow label={t(row('metadata.osStatus').label)} help={subtitles.downloads} value={subtitles.text}/>
        <LibraryRow label={t(row('metadata.osTest').label)} trailing={<Button variant="glass" size="xs" disabled={disabled} onPress={() => {if (current()) void controller.checkOpenSubtitles(settings);}}>{t(row('metadata.osTest').control.label!)}</Button>}/>
      </LibraryGroup>
      <NativeAdvancedSection testID="server-metadata-advanced" summary={`${artworkLimitText(settings.localArtworkLimitBytes)} GB · ${settings.artworkMaxEdge} px`}>
        {advanced && <><SectionTitle>{advancedTitle(0)}</SectionTitle><LibraryGroup>
          {select('metadata.refresh', advanced.refreshPolicy, value => ({refreshPolicy: value as typeof advanced.refreshPolicy}), true)}
          <LibraryRow label={t(row('metadata.refreshAll').label)} help={advanced.refreshAll.running ? t('server.metadata.refreshAllRunning') : row('metadata.refreshAll').help ? t(row('metadata.refreshAll').help!) : undefined}
            trailing={<Button testID="metadata.refreshAll" variant="glass" size="xs" disabled={disabled} onPress={() => {if (current()) setConfirmRefresh(true);}}>{t(row('metadata.refreshAll').control.label!)}</Button>}/>
        </LibraryGroup>
        <SectionTitle>{advancedTitle(1)}</SectionTitle><LibraryGroup>
          {select('metadata.fallbackLanguage', advanced.fallbackLanguage, value => ({fallbackLanguage: value as typeof advanced.fallbackLanguage}), true)}
          {select('metadata.animeTitles', advanced.animeTitles, value => ({animeTitles: value as typeof advanced.animeTitles}))}
        </LibraryGroup>
        <SectionTitle>{advancedTitle(2)}</SectionTitle></>}
        <LibraryGroup>
          {select('metadata.artworkLimit', artworkLimitText(settings.localArtworkLimitBytes), value => ({localArtworkLimitBytes: Number(value) * 2 ** 30}), true,
            t('server.metadata.artworkUsedHelp', {used: artworkLimitText(settings.localArtworkUsedBytes)}))}
          {select('metadata.artworkSize', settings.artworkMaxEdge === 1280 ? 'small' : settings.artworkMaxEdge === 2560 ? 'large' : 'standard', value => ({artworkMaxEdge: value === 'small' ? 1280 : value === 'large' ? 2560 : 1920}))}
          {toggle('metadata.castPhotos', advanced?.castPhotos, castPhotos => ({castPhotos}))}
        </LibraryGroup>
        {advanced && <><SectionTitle>{advancedTitle(3)}</SectionTitle><LibraryGroup>
          {credential('metadata.acoustid', advanced.acoustidKeyConfigured, true)}
        </LibraryGroup></>}
      </NativeAdvancedSection>
    </ScrollView>
    <BottomSheet open={!!editing} onOpenChange={open => {if (!open && !state.busy) {setEditing(undefined); setText('');}}} title={editing ? t(editing.row.label) : ''}
      footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" disabled={state.busy} onPress={() => {setEditing(undefined); setText('');}}>{t('action.cancel')}</Button>
        <Button disabled={disabled || !editing} loading={state.busy} onPress={() => {
          if (!editing || !current()) return;
          const captured = editing; void controller.save(captured.original, {[captured.key]: text}).then(saved => {if (saved && current()) {setEditing(undefined); setText('');}});
        }}>{t('action.save')}</Button></View>}>
      <TextField testID="metadata-credential-value" label={editing ? t(editing.row.label) : ''} value={text} onChangeText={setText} editable={!disabled} secureTextEntry={editing?.row.control.secret}
        autoCapitalize="none" autoCorrect={false} autoComplete="off" placeholder={editing?.row.control.placeholder ? t(editing.row.control.placeholder) : undefined}
        description={editing?.row.help ? t(editing.row.help) : undefined}/>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: 8}]}>{t('server.metadata.clearValueHelp')}</Text>
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {setEditing(undefined); setText(''); void controller.load();}, 'server-console')}/>}
    </BottomSheet>
    <BottomSheet open={confirmRefresh} onOpenChange={open => {if (!open && !state.busy) setConfirmRefresh(false);}} title={t(row('metadata.refreshAll').label)}
      footer={<View style={{flexDirection: 'row', gap: 10}}><Button variant="glass" disabled={state.busy} onPress={() => setConfirmRefresh(false)}>{t('action.cancel')}</Button>
        <Button testID="metadata.refreshAll.confirm" disabled={disabled} loading={state.busy} onPress={() => {
          if (!settings || !current()) return;
          void controller.refreshAll(settings).then(done => {if (done && current()) setConfirmRefresh(false);});
        }}>{t(row('metadata.refreshAll').control.label!)}</Button></View>}>
      <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{t(row('metadata.refreshAll').control.confirm!)}</Text>
    </BottomSheet>
  </View>;
}
