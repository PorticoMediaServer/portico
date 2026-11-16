import React, {useCallback, useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {OwnerRemoteSettings} from '../../../../../../packages/client-core/src/server-admin/remote-settings';
import {withGuardedRaw} from '../../../../../../packages/client-core/src/home-selection/list-destinations';
import {settingsSpec, type SettingsRow as SpecRow} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import {presentError} from '../../../../../../packages/client-core/src/presentation/errors';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import {Button, Select, Switch, TextField} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays/BottomSheet';
import {SettingsRow} from '../../../kit/shell';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeSettingsLivePorts} from '../ports';
import {screenScopeEnded} from '../lifetime';
import {useSettingsRequestScope} from '../use-settings-request-scope';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeSettingControl} from './SettingControl';
import {settingsPageHeading, settingsPageInset} from './page-layout';

type Translate = typeof defaultI18n.t;
type Group = Readonly<{title?: MessageId; rows: readonly SpecRow[]}>;
type OwnAddress = {enabled: boolean; domain: string; certificateFile: string; keyFile: string};
const when = (value: string) => defaultI18n.dateTime(value);
const K = 'next.settingsSpec.server.remote.groups.2.rows.0.children.0.children.0';
const B = 'next.settingsSpec.server.remote.groups.2.rows.1.children.0';
const C = 'next.settingsSpec.server.remote.groups.1.rows.0';
/** The rebuilt server's own facts drawn on this page; every other row is one OwnerRemoteSettings stores. */
const FACTS = new Set(['remote.refused', 'remote.blocked', 'remote.thisDevice', 'remote.listen', 'remote.addresses']);
const OWN_ADDRESS_CHILDREN = new Set(['remote.externalAddress', 'remote.certFile', 'remote.certKey', 'remote.certStatus', 'remote.identityCode']);
const LIST_ROWS = new Set(['remote.homeNets', 'remote.customAddresses', 'remote.trustedProxies']);

/** Settings › Server › Remote access on the phone and tablet, on the rebuilt server: the remote-access and network
 * documents, the server's status (outside check, certificate, router, refused and blocked addresses) and the network
 * interface, all through OwnerRemoteSettings, the reader web's Remote access uses. A draft is saved row by row with
 * the revision each document was read at; a change that would lock the owner out asks first. */
export function NativeRemoteAccessPage({ports, runtime, compactHeader = false}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean}) {
  const theme = useTheme(), t = runtime.text.t as Translate, context = ports.context;
  const {api, lifetime} = useSettingsRequestScope(context);
  const source = useMemo(() => new OwnerRemoteSettings(withGuardedRaw(api, context.api, () => {if (!lifetime.current()) throw screenScopeEnded();}), lifetime.current), [api, context, lifetime]);
  useEffect(() => () => source.dispose(), [source]);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), status = state.status;
  const [loaded, setLoaded] = useState(false), [loadError, setLoadError] = useState<unknown>(), [revision, setRevision] = useState(0);
  const [drafts, setDrafts] = useState<ReadonlyMap<string, unknown>>(new Map()), [saving, setSaving] = useState(false), [saveError, setSaveError] = useState<unknown>();
  const [own, setOwn] = useState<OwnAddress>(), [advanced, setAdvanced] = useState(false);
  const reread = useCallback(async () => {await source.readConfiguration(); if (lifetime.current()) {setLoaded(true); setRevision(value => value + 1);}}, [source, lifetime]);
  const load = useCallback(() => {void reread().then(() => setLoadError(undefined), error => {if (lifetime.current()) setLoadError(error);});}, [reread, lifetime]);
  useEffect(load, [load]);
  const section = settingsSpec.find(value => value.scope === 'server' && value.id === 'remote');
  const main: readonly Group[] = section?.groups ?? [], extra: readonly Group[] = section?.advanced?.groups ?? [];
  const value = (id: string) => drafts.has(id) ? drafts.get(id) : source.read(id)?.current;
  const enabled = (row: SpecRow) => {
    const rule = row.enableWhen; if (!rule) return true;
    const current = value(rule.id) as never;
    return (!rule.in || rule.in.includes(current)) && (!rule.notIn || !rule.notIn.includes(current));
  };
  const change = (id: string, next: unknown) => {if (!saving) {setSaveError(undefined); setDrafts(current => new Map(current).set(id, next));}};
  const blocked = saving || state.busy || !!state.confirmation;
  const save = async () => {
    if (saving || !lifetime.current()) return;
    setSaving(true); setSaveError(undefined);
    try {
      for (const row of [...main, ...extra].flatMap(function all(group: Group | SpecRow): SpecRow[] {return 'rows' in group ? group.rows.flatMap(all) : [group, ...(group.children ?? []).flatMap(all)];})) {
        if (!drafts.has(row.id)) continue;
        await source.save(row.id, drafts.get(row.id));
        await reread();
        if (lifetime.current()) setDrafts(current => {const next = new Map(current); next.delete(row.id); return next;});
      }
    } catch (error) {
      // A change that would refuse this device or the owner's own sign-in comes back as a confirmation.
      if (lifetime.current() && !source.getSnapshot().confirmation) setSaveError(error);
      await reread().catch(() => {});
    } finally {if (lifetime.current()) setSaving(false);}
  };
  const confirm = () => {void source.confirm().then(done => {if (done && lifetime.current()) {setDrafts(new Map()); setRevision(v => v + 1);}});};

  const savedOwn = state.interfaces?.document.ownAddress as OwnAddress | undefined, ownDraft = own ?? savedOwn;
  const ownAddress = savedOwn && ownDraft ? <View key="remote.ownCertificate" style={{gap: 10}}>
    <SettingsRow first label={t(`${C}.label`)} control={<Switch testID="remote.ownCertificate" accessibilityLabel={t(`${C}.label`)} checked={ownDraft.enabled} disabled={blocked} onCheckedChange={enabled => setOwn({...ownDraft, enabled})}/>}/>
    {ownDraft.enabled && <>
      <TextField testID="remote.externalAddress" label={t(`${C}.children.0.label`)} description={t('next.remoteOwner.domainHelp')} value={ownDraft.domain} editable={!blocked} autoCapitalize="none" autoCorrect={false} onChangeText={domain => setOwn({...ownDraft, domain})}/>
      <TextField testID="remote.certFile" label={t(`${C}.children.1.label`)} description={t('next.remoteOwner.fileHelp')} value={ownDraft.certificateFile} editable={!blocked} autoCapitalize="none" autoCorrect={false} onChangeText={certificateFile => setOwn({...ownDraft, certificateFile})}/>
      <TextField testID="remote.certKey" label={t(`${C}.children.2.label`)} description={t('next.remoteOwner.fileHelp')} value={ownDraft.keyFile} editable={!blocked} autoCapitalize="none" autoCorrect={false} onChangeText={keyFile => setOwn({...ownDraft, keyFile})}/>
    </>}
    {status && <View style={{gap: 2}}>
      <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('next.remoteStatus.ownCertificate')}: {status.certificate.problem ?? t(status.certificate.state === 'ok' ? 'next.remoteOwner.serving' : 'next.remoteStatus.none')}</Text>
      {status.certificate.names?.map(name => <Text key={name} style={[shellType(13), {color: theme.colors.textSecondary}]}>{name}</Text>)}
      {status.certificate.expiresAt && <Text style={[shellType(13), {color: theme.colors.textSecondary}]}>{t('next.remoteStatus.expires', {when: when(status.certificate.expiresAt)})}</Text>}
      <Text selectable style={[shellType(13), {color: theme.colors.textSecondary}]}>{t(`${C}.children.4.label`)}: {status.identityCode}</Text>
    </View>}
    <View style={{flexDirection: 'row'}}><Button testID="remote-own-address-save" size="compact" variant="glass" loading={state.busy} disabled={blocked || JSON.stringify(ownDraft) === JSON.stringify(savedOwn)}
      onPress={() => {void source.saveOwnAddress(ownDraft).then(done => {if (done && lifetime.current()) setOwn(undefined);});}}>{t('next.remoteStatus.saveOwnAddress')}</Button></View>
  </View> : null;

  const fact = (row: SpecRow): React.ReactNode => {
    if (row.id === 'remote.thisDevice') return status ? <SettingsRow key={row.id} first label={t(row.label)} value={t(status.thisDevice.atHome ? 'next.remoteStatus.deviceHome' : 'next.remoteStatus.deviceAway', {address: status.thisDevice.address})}/> : null;
    if (row.id === 'remote.listen') return status?.listener ? <SettingsRow key={row.id} first label={t(row.label)} value={`${status.listener.address}:${status.listener.port}`}/> : null;
    if (row.id === 'remote.addresses') return status ? <SettingsRow key={row.id} first label={t(row.label)} description={status.addresses.map(address => `${address.address}:${address.port}`).join('\n')}/> : null;
    if (row.id === 'remote.blocked') return <View key={row.id} style={{gap: 8}}>
      <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{t(row.label)}</Text>
      {!state.blocks.length ? <Text style={[shellType(13), {color: theme.colors.textTertiary}]}>{t(`${B}.control.empty`)}</Text>
        : state.blocks.map(block => <SettingsRow key={block.id} first label={block.network} description={t('next.remoteOwner.blockedUntil', {when: when(block.blockedUntil)})}
          control={<Button size="xs" variant="glass" disabled={blocked} onPress={() => {void source.unblock(block);}}>{t(`${B}.control.itemAction.label`)}</Button>}/>)}
    </View>;
    if (row.id === 'remote.refused') {
      const names = status?.refused ?? [];
      return <View key={row.id} style={{gap: 8}}>
        <Text style={[shellType(15, 600), {color: theme.colors.text}]}>{t(row.label)}</Text>
        {!names.length ? <Text style={[shellType(13), {color: theme.colors.textTertiary}]}>{t(`${K}.control.empty`)}</Text>
          : names.map(item => <View key={item.address} style={{gap: 6}}>
            <SettingsRow first label={item.address} description={t(item.reason === 'http' ? 'next.remoteOwner.refusedHttp' : 'next.remoteOwner.refusedAddress')}/>
            <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>
              {item.fixes.includes('allow') && <Button size="xs" variant="glass" disabled={blocked} onPress={() => {void source.allow(item.address);}}>{t(`${K}.control.itemAction.label`)}</Button>}
              {item.fixes.includes('standard') && <Button size="xs" variant="glass" disabled={blocked} onPress={() => change('remote.security', 'standard')}>{t(`${K}.control.standardAction.label`)}</Button>}
            </View>
          </View>)}
      </View>;
    }
    return null;
  };
  const control = (row: SpecRow): React.ReactNode => {
    if (row.id === 'remote.ownCertificate') return ownAddress;
    if (FACTS.has(row.id)) return fact(row);
    if (OWN_ADDRESS_CHILDREN.has(row.id) || !source.has(row.id) || source.read(row.id) === undefined) return null;
    if (row.enableWhen?.hide && !enabled(row)) return null;
    const current = value(row.id), disabled = blocked || !enabled(row);
    if (LIST_ROWS.has(row.id)) return <TextField key={row.id} testID={row.id} label={t(row.label)} description={row.help ? t(row.help) : undefined} multiline editable={!disabled} autoCapitalize="none" autoCorrect={false}
      value={Array.isArray(current) ? current.join('\n') : ''} onChangeText={text => change(row.id, text.split('\n').map(line => line.trim()).filter(Boolean))}/>;
    if (row.id === 'remote.secureConnections' && status?.remoteSecurity.active === 'enhanced')
      return <SettingsRow key={row.id} first label={t(row.label)} value={t('next.remoteOwner.required')}/>;
    if (row.id === 'remote.interface') {
      const interfaces = state.interfaces, options = [{value: 'all', label: t('next.remoteOwner.automatic')}, ...(interfaces?.interfaces ?? []).map(item => ({value: item.name, label: item.name, description: item.addresses.join(', ')}))];
      if (typeof current === 'string' && !options.some(option => option.value === current)) options.push({value: current, label: current});
      return <SettingsRow key={row.id} first label={t(row.label)} description={interfaces?.note ?? (row.help ? t(row.help) : undefined)}
        control={<Select testID={row.id} accessibilityLabel={t(row.label)} value={String(current)} disabled={disabled} options={options} onValueChange={next => change(row.id, next)}/>}/>;
    }
    // Not reachable from outside without the Portico route is not offered by the rebuilt server.
    const options = row.id === 'remote.access' ? row.control.options?.filter(option => option.value !== 'private') : row.control.options;
    const shown = {...row, control: {...row.control, ...(row.control.type === 'segmented' ? {type: 'select' as const} : {}), ...(options ? {options} : {})}};
    return <NativeSettingControl key={row.id} row={shown} value={current} t={t} disabled={disabled} onChange={next => change(row.id, next)}/>;
  };
  const renderRow = (row: SpecRow): React.ReactNode => {
    const self = control(row), children = (row.children ?? []).map(renderRow).filter(Boolean);
    return self || children.length ? <React.Fragment key={row.id}>{self}{children}</React.Fragment> : null;
  };
  const renderGroup = (group: Group, index: number) => {
    const rows = group.rows.map(renderRow).filter(Boolean);
    return rows.length ? <View key={index} style={{gap: 10, marginTop: 22}}>
      {group.title && <Text accessibilityRole="header" style={[shellType(17, 800), {color: theme.colors.text}]}>{t(group.title)}</Text>}
      {rows}
    </View> : null;
  };

  const outside = status?.outside, answers = outside?.addresses ?? [];
  const tone = answers.length ? answers.every(item => item.reachable) ? 'ok' : 'warn' : 'quiet';
  const certificate = status?.managedCertificate;
  const role = context.account.account.role === 'owner' ? 'owner' : 'administrator';
  const notes = [status?.remoteSecurity.message, status?.remoteSecurity.problem, state.interfaces?.document.warning, state.interfaces?.document.ipv6Note, ...(status?.warnings.map(warning => warning.message) ?? [])]
    .filter((line): line is string => typeof line === 'string' && line.length > 0);
  if (loadError !== undefined && !loaded) return <StateView {...runtime.error(loadError, load, 'server-console')}/>;
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}>
      <ServerHeader serverName={context.serverName} viewerRole={role} audience={role === 'administrator' ? 'administrators' : 'owner'} variant="phone" style={{marginBottom: 12}}/>
      {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.remote')}</Text>}
      {!loaded ? <StateView title={t('status.loading')}/> : <>
        <View testID="remote-access-status" style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 16, marginTop: 14, gap: 6}}>
          <View style={{flexDirection: 'row', gap: 12, alignItems: 'center'}}>
            <View style={{width: 9, height: 9, borderRadius: 5, backgroundColor: tone === 'ok' ? theme.colors.healthy : tone === 'warn' ? theme.colors.amber : theme.colors.textTertiary}}/>
            <Text accessibilityRole="text" style={[shellType(15, 700), {color: theme.colors.text, flex: 1}]}>{t(state.checking ? 'settings.server.remote.checking' : !outside?.checkedAt ? 'next.remoteOwner.notChecked' : tone === 'ok' ? 'next.remoteOwner.reachable' : answers.length ? 'next.remoteOwner.unreachable' : 'next.remoteOwner.unknown')}</Text>
          </View>
          {outside?.checkedAt && <Text style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{t('web.servers.checked', {when: when(outside.checkedAt)})}</Text>}
          {answers.map(item => <Text key={`${item.address}:${item.port}`} style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{item.address}:{item.port} · {t(item.reachable ? 'next.remoteOwner.reachable' : 'next.remoteOwner.unreachable')}</Text>)}
          {outside?.problem && <Text style={[shellType(12.5), {color: theme.colors.amber}]}>{outside.problem}</Text>}
          {certificate && <Text style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{t('next.remoteStatus.secure')}: {t(certificate.ready ? 'next.remoteOwner.serving' : 'next.remoteOwner.waiting')}{certificate.name ? ` · ${certificate.name}` : ''}{certificate.problem ? ` · ${certificate.problem}` : ''}</Text>}
          {status && [status.router, status.routerIPv6].filter(router => router && router.state !== 'off').map((router, index) => <Text key={index} style={[shellType(12.5), {color: theme.colors.textSecondary}]}>{t('next.remoteOwner.router')}: {t(router!.state === 'open' ? 'next.remoteOwner.mapped' : 'next.remoteOwner.notMapped')}</Text>)}
          {notes.map((line, index) => <Text key={index} testID="remote-access-warning" style={[shellType(12.5, 400, 17), {color: theme.colors.amber}]}>{line}</Text>)}
          <View style={{flexDirection: 'row', marginTop: 4}}><Button testID="remote-access-check" size="compact" variant="glass" loading={state.checking} disabled={!status || blocked} onPress={() => {void source.check();}}>{t('next.settingsSpec.server.general.groups.1.rows.2.control.label')}</Button></View>
        </View>
        {main.map(renderGroup)}
        {extra.length > 0 && <NativeAdvancedSection testID="remote-access-advanced" open={advanced} onOpenChange={setAdvanced}>{extra.map(renderGroup)}</NativeAdvancedSection>}
        {state.error !== undefined && !state.confirmation && <Text accessibilityRole="alert" style={[shellType(13, 600), {color: theme.colors.record, marginTop: 10}]}>{presentError(state.error, 'server-console', {operation: 'action'}).body}</Text>}
        {drafts.size > 0 && <View style={{flexDirection: 'row', gap: 8, marginTop: 16}}>
          <Button testID="remote-access-discard" variant="glass" disabled={saving} onPress={() => {setDrafts(new Map()); setSaveError(undefined);}}>{t('action.discard')}</Button>
          <Button testID="remote-access-save" loading={saving} disabled={!!state.confirmation} onPress={() => {void save();}}>{t('action.save')}</Button>
        </View>}
        {saveError !== undefined && <StateView variant="inline" {...runtime.error(saveError, () => {void save();}, 'server-console')}/>}
      </>}
    </ScrollView>
    <BottomSheet open={!!state.confirmation} onOpenChange={open => {if (!open && !state.busy) source.cancelConfirmation();}} testID="remote-access-confirm"
      title={t('next.remoteOwner.confirm')} description={state.confirmation?.message}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={state.busy} onPress={source.cancelConfirmation}>{t('action.cancel')}</Button>
        <Button variant="danger" loading={state.busy} onPress={confirm}>{t('next.remoteOwner.saveAnyway')}</Button></View>}><View/></BottomSheet>
  </View>;
}
