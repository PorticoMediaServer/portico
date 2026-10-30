import React, {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {FlatList, ScrollView, Text, View} from 'react-native';
import type {SettingsPeopleQuery, SettingsPersonRow} from '../../../../../../packages/client-core/src/settings-people';
import type {ServerInvitation} from '../../../../../../packages/client-core/src/direct-account';
import type {AccessDevice, AccessMember, MemberDeletionPreview, MemberLimits} from '../../../../../../packages/client-core/src/server-administration';
import {roleLabel as accessRoleLabel} from '../../../../../../packages/client-core/src/server-admin/people-words';
import {peopleStreamLimits, readSettingsBinding, settingsBinding, settingsBindingPatch, applySettingsBindingPatch} from '../../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec} from '../../../../../../packages/client-core/src/presentation/settings-spec';
import type {RuntimeSettings} from '../../../../../../packages/client-core/src/console';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Focusable, Select, Switch, Tabs, TextField} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';
import {ServerHeader} from '../../../kit/shell/ServerIdentity';
import {shellType} from '../../../kit/shell/visual';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import type {NativeSettingsLivePorts} from '../ports';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {NativeAdvancedSection} from './AdvancedSection';
import {NativeSettingControl} from './SettingControl';
import {useStorageForms, useStorageSet, useFormState, type StorageForms} from './storage/forms';
import {NativePeopleSource, type InvitationOpening} from './people-source';
import {InvitationReadySheet} from './InvitationReadySheet';
import {PeopleMemberProfiles} from './PeopleMemberProfiles';
import {settingsPageHeading, settingsPageInset} from './page-layout';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime; compactHeader?: boolean};
type Confirmation = Readonly<{person: AccessMember; action: 'disable' | 'enable' | 'role' | 'devices'; role?: 'admin' | 'member'; device?: AccessDevice} | {person: AccessMember; action: 'remove'; preview: MemberDeletionPreview} | {invitation: ServerInvitation; action: 'revoke'}>;
const IDS = ['runtime'] as const;
// List contracts expose strings; the shared parser admits these three roles.
const roleLabel = (t: NativeShellRuntime['text']['t'], role: string) => accessRoleLabel(t, role === 'owner' || role === 'admin' ? role : 'member');
// Invitation list rows carry the machine state; show the same labels as the status filter.
const invitationState = (t: NativeShellRuntime['text']['t'], state: string) => state === 'pending' ? t('web.access.trustWaiting') : state === 'accepted' ? t('web.access.trustAccepted') : state === 'expired' ? t('api.credential.state.expired') : state === 'revoked' ? t('api.credential.state.revoked') : state;

export function NativeServerPeople(props: Props) {
  const [source, setSource] = useState<NativePeopleSource>();
  const {context, requestId} = props.ports, reportError = props.runtime.reportError;
  useLayoutEffect(() => {const next = new NativePeopleSource(context, requestId, reportError); setSource(next); void next.load(); return next.dispose;}, [context, requestId, reportError]);
  return source?.context === context && source.current() ? <People key={context.authority().accountId} {...props} source={source}/> : null;
}

function People({ports, runtime, compactHeader, source}: Props & {source: NativePeopleSource}) {
  const theme = useTheme(), t = runtime.text.t, state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [search, setSearch] = useState(''), [cursors, setCursors] = useState<readonly string[]>(['']);
  const [confirmation, setConfirmation] = useState<Confirmation>(), [typedName, setTypedName] = useState('');
  const view = useMemo(() => ({active: true, source, confirmation: undefined as Confirmation | undefined}), [source]);
  useLayoutEffect(() => {view.active = true; return () => {view.active = false; view.confirmation = undefined;};}, [view]);
  const current = () => view.active && source.current();
  const choose = (patch: Partial<SettingsPeopleQuery>) => {if (!current() || state.busy) return; setCursors(['']); source.select(state.tab, {...state.query, ...patch});};
  const ask = (value: Confirmation) => {if (!current() || source.getSnapshot().busy) return; view.confirmation = value; setConfirmation(value); setTypedName('');};
  const close = () => {if (source.getSnapshot().busy || view.confirmation !== confirmation) return; view.confirmation = undefined; setConfirmation(undefined); setTypedName('');};
  const held = confirmation && view.confirmation === confirmation && current() ? confirmation : undefined;
  const confirm = async () => {
    if (!held || view.confirmation !== held || !current() || source.getSnapshot().busy) return;
    if (held.action === 'remove' && typedName !== held.preview.username) return;
    view.confirmation = undefined; setConfirmation(undefined); setTypedName('');
    if (held.action === 'remove') await source.remove(held.person, held.preview);
    else if (held.action === 'revoke') await source.revoke(held.invitation);
    else if (held.action === 'devices') await source.signOutDevices(held.person, held.device);
    else if (held.action === 'role' && held.role) await source.role(held.person, held.role);
    else if (held.action === 'enable' || held.action === 'disable') await source.enabled(held.person, held.action === 'enable');
  };
  const loading = state.loading || state.busy;
  const page = state.tab === 'people' ? state.people : state.invitations;
  const rows: readonly (SettingsPersonRow | ServerInvitation)[] = state.tab === 'people' ? state.people?.items ?? [] : state.invitations?.items ?? [];
  const header = <View style={{gap: 14, marginBottom: 12}}>
    <ServerHeader serverName={ports.context.serverName} viewerRole={source.ownerControls() ? 'owner' : 'administrator'} audience={source.ownerControls() ? 'owner' : 'administrators'} variant="phone"/>
    {!compactHeader && <Text accessibilityRole="header" style={[settingsPageHeading(theme), {color: theme.colors.text}]}>{t('settings.server.people')}</Text>}
    <Tabs value={state.tab} onValueChange={tab => {if (current() && !loading && (tab === 'people' || tab === 'invitations')) {setCursors(['']); source.select(tab);}}} items={[{value: 'people', label: t('settings.server.people')}, {value: 'invitations', label: t('settings.server.invitations')}]}/>
    <TextField testID="people-search" accessibilityLabel={t('web.shell.search')} value={search} maxLength={160} returnKeyType="search" editable={!state.busy} onChangeText={setSearch} onSubmitEditing={() => choose({search: search.trim()})}/>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 8}}>
      <Button size="compact" variant="glass" onPress={() => choose({search: search.trim()})} disabled={loading}>{t('web.shell.search')}</Button>
      {state.tab === 'people' ? <>
        <Select accessibilityLabel={t('web.access.roleLabel')} value={state.query.role} disabled={loading} options={[{value: 'all', label: t('web.people.allRoles')}, ...(['owner', 'admin', 'member'] as const).map(role => ({value: role, label: roleLabel(t, role)}))]} onValueChange={role => {if (['all', 'owner', 'admin', 'member'].includes(role)) choose({role: role as SettingsPeopleQuery['role']});}}/>
        <Select accessibilityLabel={t('web.general.status')} value={state.query.status} disabled={loading} options={[{value: 'all', label: t('web.people.allStatuses')}, {value: 'now', label: t('web.people.playingNow')}, {value: 'month', label: t('next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.2.label')}, {value: 'idle', label: t('web.people.inactive')}, {value: 'off', label: t('web.people.disabledBadge')}]} onValueChange={status => {if (['all', 'now', 'month', 'idle', 'off'].includes(status)) choose({status: status as SettingsPeopleQuery['status']});}}/>
      </> : <Select accessibilityLabel={t('web.access.invitationStatus')} value={state.invitationStatus} disabled={loading} options={[{value: '', label: t('web.access.allInvitations')}, {value: 'pending', label: t('web.access.trustWaiting')}, {value: 'accepted', label: t('web.access.trustAccepted')}, {value: 'expired', label: t('api.credential.state.expired')}, {value: 'revoked', label: t('api.credential.state.revoked')}]} onValueChange={status => {if (current() && ['', 'pending', 'accepted', 'expired', 'revoked'].includes(status)) {setCursors(['']); source.select('invitations', state.query, status as typeof state.invitationStatus);}}}/>}
      {state.tab === 'people' && <>
        <Select testID="people-sort" accessibilityLabel={t('web.filters.sortBy')} value={state.query.sort} disabled={loading}
          options={(['person', 'role', 'access', 'profiles', 'active'] as const).map((value, index) => ({value, label: t((['next.settingsSpec.server.people.groups.0.rows.0.control.columns.0.label', 'next.settingsSpec.server.people.groups.0.rows.0.control.columns.1.label', 'next.settingsSpec.server.people.groups.0.rows.0.control.columns.2.label', 'next.settingsSpec.server.people.groups.0.rows.0.control.columns.3.label', 'next.settingsSpec.server.people.groups.0.rows.0.control.columns.4.label'] as const)[index]!)}))}
          onValueChange={sort => {if (['person', 'role', 'access', 'profiles', 'active'].includes(sort)) choose({sort: sort as SettingsPeopleQuery['sort']});}}/>
        <Select testID="people-direction" accessibilityLabel={t('builder.preset.order')} value={state.query.direction} disabled={loading}
          options={(['asc', 'desc'] as const).map(value => ({value, label: t('web.filters.direction', {kind: 'other', dir: value})}))}
          onValueChange={direction => {if (direction === 'asc' || direction === 'desc') choose({direction});}}/>
      </>}
      {source.ownerControls() && <Button testID="people-invite" variant="glass" size="compact" disabled={loading} onPress={() => {if (current()) source.beginInvitation();}}>{t('web.access.inviteSomeone')}</Button>}
      <Button variant="text" size="compact" icon="i-refresh" disabled={loading} onPress={() => {if (current()) void source.load(state.cursor);}}>{t('action.refresh')}</Button>
    </View>
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {if (current()) void source.load(state.cursor);}, 'server-console')}/>}
    {page && <Text style={[shellType(14, 400, 20), {color: theme.colors.textTertiary}]}>{defaultI18n.number(page.matchedCount)} {t(state.tab === 'people' ? 'next.settingsSpec.server.people.groups.0.rows.0.control.noun.1' : 'next.settingsSpec.server.people.groups.1.rows.0.control.noun.1')}</Text>}
    {loading && <Text style={[shellType(14, 400, 20), {color: theme.colors.textTertiary}]}>{t('next.settings.loading')}</Text>}
  </View>;
  return <View testID="server-people" style={{flex: 1}}>
    <FlatList data={rows} keyExtractor={row => 'accountId' in row && 'username' in row ? row.accountId : row.id} contentContainerStyle={{...settingsPageInset(theme, compactHeader)}}
      ListHeaderComponent={header} ListEmptyComponent={!loading && page ? <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('web.people.noResults')}</Text> : null}
      renderItem={({item}) => 'username' in item ? <Focusable testID={`people-person-${item.accountId}`} accessibilityRole="button" accessibilityLabel={item.username} disabled={loading} onPress={() => {if (current()) void source.open(item);}} style={{paddingVertical: 14, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft, gap: 4}}>
        <View style={{flexDirection: 'row', justifyContent: 'space-between', gap: 12}}><Text style={[shellType(16, 700), {color: theme.colors.text}]}>{item.username}</Text><Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{roleLabel(t, item.role)}</Text></View>
        {item.email && <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{item.email}</Text>}
        <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{item.allowedLibraries.length ? item.libraryNames.join(' · ') : t('server.people.allLibraries')} · {item.effectiveMaxStreams === null ? t('settings.server.unlimited') : t('server.people.streams', {count: item.effectiveMaxStreams})}</Text>
        <Text style={[shellType(14, 400, 20), {color: theme.colors.textTertiary}]}>{item.profileNames.filter(name => item.profileNames.length > 1 || name !== item.username).join(' · ')}</Text>
        <Text style={[shellType(13, 400, 18), {color: theme.colors.textTertiary}]}>{item.liveState === 'watching' ? t('next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.1.label') : item.liveState === 'listening' ? t('next.settings.listeningNow') : item.lastActiveAt ? defaultI18n.date(item.lastActiveAt) : '—'}</Text>
      </Focusable> : <View style={{paddingVertical: 14, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft, gap: 5}}>
        <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{item.notes || roleLabel(t, item.role)}</Text>
        <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{roleLabel(t, item.role)} · {invitationState(t, item.state)} · {defaultI18n.date(item.expiresAt)}</Text>
        {source.ownerControls() && item.state === 'pending' && <Button variant="glass" size="compact" disabled={loading} onPress={() => {if (current()) source.beginInvitation(item);}}>{t('next.selection.edit')}</Button>}
        {source.ownerControls() && item.state === 'pending' && <Button size="compact" variant="danger" disabled={loading} onPress={() => ask({action: 'revoke', invitation: item})}>{t('web.access.revoke')}</Button>}
      </View>}
      ListFooterComponent={<View style={{gap: 16, marginTop: 12}}><View style={{flexDirection: 'row', gap: 8}}>
        {cursors.length > 1 && <Button variant="glass" disabled={loading} onPress={() => {if (!current() || loading) return; const next = cursors.slice(0, -1); setCursors(next); void source.load(next[next.length - 1]);}}>{t('action.back')}</Button>}
        {!!page?.nextCursor && <Button variant="glass" disabled={loading} onPress={() => {if (!current() || loading || page !== (source.getSnapshot().tab === 'people' ? source.getSnapshot().people : source.getSnapshot().invitations)) return; setCursors([...cursors, page.nextCursor]); void source.load(page.nextCursor);}}>{t('action.showMore')}</Button>}
      </View>{source.ownerControls() && <PeopleAdvanced ports={ports} runtime={runtime}/>}</View>}/>
    <BottomSheet scrollable={false} open={!!state.selected} onOpenChange={open => {if (!open && current() && source.getSnapshot().selected === state.selected) source.closePerson();}} title={state.selected?.username} dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}>
      {state.opening && <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('next.settings.loading')}</Text>}
      {state.person && <PersonDetails key={`${state.person.accountId}:${state.person.revision}:${state.person.limitsRevision}`} source={source} person={state.person} runtime={runtime} ask={ask}/>}
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {if (state.selected && current()) void source.open(state.selected);}, 'server-console')}/>}
    </BottomSheet>
    {state.invitationOpening?.ready && state.invitationOpening.invitation ? <InvitationReadySheet key={`ready:${state.invitationOpening.invitation.id}:${state.invitationOpening.invitation.revision}`} source={source} opening={state.invitationOpening} runtime={runtime} serverName={ports.context.serverName}
        signIn={ports.context.account.account.hostedAccountId ? 'portico' : 'local'}/>
      : state.invitationOpening && <InvitationEditor key={state.invitationOpening.invitation ? `${state.invitationOpening.invitation.id}:${state.invitationOpening.invitation.revision}` : 'new'} source={source} opening={state.invitationOpening} runtime={runtime}/>}
    <BottomSheet open={!!held} showClose={false} onOpenChange={open => {if (!open) close();}} title={held?.action === 'revoke' ? t('web.access.revoke') : held ? held.person.username : undefined}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" onPress={close}>{t('action.cancel')}</Button><Button variant={held?.action === 'role' || held?.action === 'enable' ? 'primary' : 'danger'} disabled={!held || held.action === 'remove' && typedName !== held.preview.username} onPress={() => {void confirm();}}>{t(held?.action === 'remove' ? 'action.delete' : held?.action === 'revoke' ? 'web.access.revoke' : held?.action === 'devices' ? 'action.signOut' : 'action.save')}</Button></View>}>
      {held?.action === 'remove' ? <View style={{gap: 12}}><Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('web.people.removeBody', {profiles: held.preview.profiles, devices: held.preview.devices, savedResources: held.preview.savedResources})}</Text><TextField label={held.preview.username} value={typedName} onChangeText={setTypedName} autoCapitalize="none" autoCorrect={false}/></View>
        : <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{held?.action === 'devices' ? t(held.device ? 'next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.0.control.itemAction.confirm' : 'next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.confirm') : held?.action === 'role' ? t('web.people.changeRole', {role: roleLabel(t, held.role!)}) : held?.action === 'disable' ? t('web.people.disableConfirm') : held?.action === 'enable' ? t('web.people.enableConfirm') : t('web.access.revokeNoLonger')}</Text>}
    </BottomSheet>
  </View>;
}

function PersonDetails({source, person, runtime, ask}: {source: NativePeopleSource; person: AccessMember; runtime: NativeShellRuntime; ask: (value: Confirmation) => void}) {
  const theme = useTheme(), t = runtime.text.t, state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot);
  const [tab, setTab] = useState('account'), [notes, setNotes] = useState(person.notes ?? ''), [limits, setLimits] = useState<MemberLimits>(person.limits);
  const [allLibraries, setAllLibraries] = useState(!person.allowedLibraries.length), [libraries, setLibraries] = useState<readonly string[]>(person.allowedLibraries);
  const streamLimit = peopleStreamLimits.read({limits}), streamChoices = ['default', 'unlimited', '1', '2', '3', '4', '6'];
  const managed = source.ownerControls() && person.role !== 'owner' && person.accountId !== source.context.account.account.id;
  const current = () => source.current() && source.getSnapshot().person === person && !source.getSnapshot().busy;
  const tabTitles = ['next.settingsSpec.server.people.panels.person.tabs.0.title', 'next.settingsSpec.server.people.panels.person.tabs.1.title', 'next.settingsSpec.server.people.panels.person.tabs.2.title', 'next.settingsSpec.server.people.panels.person.tabs.3.title', 'next.settingsSpec.server.people.panels.person.tabs.4.title'] as const;
  const tabs = <Tabs testID="people-person-tabs" value={tab} items={['account', 'access', 'profiles', 'devices', 'advanced'].map((value, index) => ({value, label: t(tabTitles[index]!)}))} onValueChange={value => {
      if (!current() || !['account', 'access', 'profiles', 'devices', 'advanced'].includes(value)) return;
      setTab(value); if (value === 'devices') void source.loadDevices(person); if (value === 'access') void source.loadLibraries();
    }}/>;
  const body = <>
    {tab === 'account' && <>
      <SettingsRow first label={t('next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.0.label')} value={person.username}/>
      {person.email && <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{person.email}</Text>}
      <TextField label={t('next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.2.label')} value={notes} maxLength={500} editable={managed && !state.busy} onChangeText={setNotes}/>
      {managed && <>
        <Button variant="glass" disabled={state.busy || notes === (person.notes ?? '')} onPress={() => {if (current()) void source.notes(person, notes);}}>{t('action.save')}</Button>
        <Select label={t('web.access.roleLabel')} value={person.role} disabled={state.busy} options={(['member', 'admin'] as const).map(role => ({value: role, label: roleLabel(t, role)}))} onValueChange={role => {if (current() && role !== person.role && (role === 'admin' || role === 'member')) ask({person, action: 'role', role});}}/>
        <Button variant={person.disabled ? 'glass' : 'danger'} disabled={state.busy} onPress={() => {if (current()) ask({person, action: person.disabled ? 'enable' : 'disable'});}}>{t(person.disabled ? 'next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.second.label' : 'next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.label')}</Button>
        <Button variant="danger" disabled={state.busy} onPress={() => {if (!current()) return; void source.previewDeletion(person).then(preview => {if (preview && current()) ask({person, action: 'remove', preview});});}}>{t('action.delete')}</Button>
      </>}
    </>}
    {tab === 'access' && <>
      <LibraryAccess source={source} runtime={runtime} all={allLibraries} selected={libraries} disabled={!managed || state.busy} onChange={(all, ids) => {if (current()) {setAllLibraries(all); setLibraries(ids);}}}/>
      {managed && <Button testID="people-save-libraries" variant="glass" disabled={state.busy || !allLibraries && !libraries.length || (allLibraries ? !person.allowedLibraries.length : JSON.stringify(libraries) === JSON.stringify(person.allowedLibraries))}
        onPress={() => {if (current() && (allLibraries || libraries.length)) void source.access(person, allLibraries ? [] : libraries);}}>{t('action.save')}</Button>}
      <SettingsRow first label={t('next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.1.label')} control={<Switch accessibilityLabel={t('next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.1.label')} checked={limits.allowRemote} disabled={!managed || state.busy} onCheckedChange={allowRemote => {if (current() && managed) setLimits({...limits, allowRemote});}}/>}/>
      <Select label={t('web.access.maxStreams')} value={streamLimit} disabled={!managed || state.busy} options={[...(!streamChoices.includes(streamLimit) ? [{value: streamLimit, label: streamLimit, disabled: true}] : []), ...streamChoices.map(value => ({value, label: value === 'default' ? t('next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.0.label') : value === 'unlimited' ? t('settings.server.unlimited') : value}))]} onValueChange={value => {if (current() && managed) setLimits(peopleStreamLimits.patch({limits}, value));}}/>
    </>}
    {tab === 'profiles' && <PeopleMemberProfiles people={source} person={person} runtime={runtime}/>}
    {tab === 'devices' && <>
      <FlatList style={{flex: 1}} testID="people-devices" data={state.devices?.person === person ? state.devices.items : []} keyExtractor={device => device.id}
        renderItem={({item: device}) => <View style={{paddingVertical: 12, gap: 6, borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
          <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{device.name || device.platform}</Text>
          <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{[device.platform, device.app, device.appVersion, device.profileName].filter(Boolean).join(' · ')}</Text>
          <Text style={[shellType(13, 400, 18), {color: theme.colors.textTertiary}]}>{t('web.devices.lastSeen', {when: defaultI18n.date(device.lastSeenAt)})}</Text>
          {managed && <Button testID={`people-device-signout-${device.id}`} variant="glass" size="compact" disabled={state.busy || state.devicesLoading} onPress={() => {if (current()) ask({person, device, action: 'devices'});}}>{t('action.signOut')}</Button>}
        </View>}
        ListEmptyComponent={!state.devicesLoading && state.devices && !state.devicesError ? <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{t('web.devices.none')}</Text> : null}/>
      {state.devicesLoading && <StateView title={t('status.loading')}/>}
      {state.devicesError !== undefined && <StateView variant="inline" {...runtime.error(state.devicesError, () => {if (current()) void source.loadDevices(person);}, 'server-console')}/>}
      {state.devices?.cursor && <Button variant="glass" disabled={state.busy || state.devicesLoading} onPress={() => {if (current()) void source.loadDevices(person, true);}}>{t('action.showMore')}</Button>}
      <Button variant="text" size="compact" disabled={state.busy || state.devicesLoading} onPress={() => {if (current()) void source.loadDevices(person);}}>{t('action.refresh')}</Button>
      {managed && <Button testID="people-devices-signout-all" variant="glass" disabled={state.busy || state.devicesLoading || !state.devices?.items.length} onPress={() => {if (current()) ask({person, action: 'devices'});}}>{t('next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.label')}</Button>}
    </>}
    {tab === 'advanced' && <SettingsRow first label={t('next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.0.label')} control={<Switch testID="people-access-tokens" accessibilityLabel={t('next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.0.label')} checked={limits.accessTokens} disabled={!managed || state.busy} onCheckedChange={accessTokens => {if (current() && managed) setLimits({...limits, accessTokens});}}/>}/>}
    {managed && (tab === 'access' || tab === 'advanced') && <Button testID="people-save-limits" variant="glass" disabled={state.busy || limits === person.limits} onPress={() => {if (current()) void source.limits(person, limits);}}>{t('action.save')}</Button>}
  </>;
  return <View style={{flex: 1, gap: 12}}>{tabs}
    {tab === 'devices' || tab === 'access' ? <View style={{flex: 1, gap: 12}}>{body}</View> : <ScrollView contentContainerStyle={{gap: 12}}>{body}</ScrollView>}
  </View>;
}

function LibraryAccess({source, runtime, all, selected, disabled, onChange}: {source: NativePeopleSource; runtime: NativeShellRuntime; all: boolean; selected: readonly string[]; disabled: boolean; onChange: (all: boolean, ids: readonly string[]) => void}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), t = runtime.text.t;
  const rows = [...(state.libraries ?? []), ...selected.filter(id => !state.libraries?.some(library => library.id === id)).map(id => ({id, name: id}))];
  return <View style={{gap: 8, ...(!all ? {flex: 1} : {})}}>
    <Select testID="people-library-mode" label={t('web.people.visibleLibraries')} value={all ? 'all' : 'some'} disabled={disabled}
      options={[{value: 'all', label: t('server.people.allLibraries')}, {value: 'some', label: t('next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.control.options.1.label')}]}
      onValueChange={value => {if (value === 'all' || value === 'some') onChange(value === 'all', selected);}}/>
    {!all && <FlatList style={{flex: 1}} testID="people-library-choices" data={rows} keyExtractor={library => library.id}
      renderItem={({item}) => <SettingsRow label={item.name} control={<Switch accessibilityLabel={item.name} checked={selected.includes(item.id)} disabled={disabled} onCheckedChange={checked => onChange(false, checked ? [...selected, item.id] : selected.filter(id => id !== item.id))}/>}/>}/>}
    {state.librariesLoading && <StateView title={t('status.loading')}/>}
    {state.librariesError !== undefined && <StateView variant="inline" {...runtime.error(state.librariesError, () => {void source.loadLibraries();}, 'server-console')}/>}
  </View>;
}

function InvitationEditor({source, opening, runtime}: {source: NativePeopleSource; opening: InvitationOpening; runtime: NativeShellRuntime}) {
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), t = runtime.text.t;
  const [notes, setNotes] = useState(opening.invitation?.notes ?? ''), [role, setRole] = useState(opening.invitation?.role === 'admin' ? 'admin' : 'member'), [days, setDays] = useState('7');
  const [all, setAll] = useState(!opening.invitation?.allowedLibraries.length), [libraries, setLibraries] = useState<readonly string[]>(opening.invitation?.allowedLibraries ?? []);
  useLayoutEffect(() => {void source.loadLibraries();}, [source]);
  const current = () => source.current() && source.getSnapshot().invitationOpening === opening && !source.getSnapshot().busy;
  return <BottomSheet scrollable={false} open title={t('web.access.inviteSomeone')} dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy} onOpenChange={open => {if (!open && current()) source.closeInvitation();}}
    footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={state.busy} onPress={() => {if (current()) source.closeInvitation();}}>{t('action.cancel')}</Button>
      <Button testID="people-save-invitation" disabled={state.busy || state.loading || !all && !libraries.length} onPress={() => {if (current() && !state.loading && (all || libraries.length)) void source.saveInvitation(opening, {notes, role: role === 'admin' ? 'admin' : 'member', days: Number(days), allowedLibraries: all ? [] : libraries});}}>{t(opening.invitation ? 'action.save' : 'web.access.createInvitation')}</Button></View>}>
    <TextField label={t('next.settingsSpec.server.people.panels.invite.groups.0.rows.1.label')} value={notes} maxLength={500} editable={!state.busy} onChangeText={setNotes}/>
    <Select label={t('web.access.roleLabel')} value={role} disabled={state.busy} options={(['member', 'admin'] as const).map(value => ({value, label: roleLabel(t, value)}))} onValueChange={value => {if (current() && (value === 'admin' || value === 'member')) setRole(value);}}/>
    <LibraryAccess source={source} runtime={runtime} all={all} selected={libraries} disabled={state.busy} onChange={(next, ids) => {if (current()) {setAll(next); setLibraries(ids);}}}/>
    {!opening.invitation && <Select label={t('next.settingsSpec.server.people.advanced.groups.3.rows.0.label')} value={days} disabled={state.busy} options={(['1', '3', '7', '30'] as const).map((value, index) => ({value, label: t((['next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.0.label', 'next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.1.label', 'next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.2.label', 'next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.3.label'] as const)[index]!)}))} onValueChange={value => {if (current() && ['1', '3', '7', '30'].includes(value)) setDays(value);}}/>}
    {opening.invitation?.state === 'pending' && <Button testID="people-share-invitation" variant="glass" disabled={state.busy} onPress={() => {if (current()) source.shareInvitation(opening);}}>{t('invite.share')}</Button>}
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {if (current()) source.closeInvitation();}, 'server-console')}/>}
  </BottomSheet>;
}

function PeopleAdvanced({ports, runtime}: Props) {
  const group = useStorageForms(ports, IDS, runtime.reportError);
  return group ? <BoundPeopleAdvanced key={group.mount} group={group} runtime={runtime}/> : null;
}
function BoundPeopleAdvanced({group, runtime}: {group: StorageForms; runtime: NativeShellRuntime}) {
  const t = runtime.text.t, form = group.forms.runtime, state = useFormState<RuntimeSettings, unknown>(form), save = useStorageSet(group);
  const rows = settingsSpec.find(section => section.scope === 'server' && section.id === 'people')?.advanced?.groups?.flatMap(group => group.rows) ?? [];
  return <NativeAdvancedSection testID="people-advanced" summary={save.dirty ? t('settings.save.unsaved') : t('next.settings.allDefaults')}>
    {state.loadError !== undefined && <StateView variant="inline" {...runtime.error(state.loadError, () => {void form.load();}, 'server-console')}/>}
    {state.draft && rows.map(row => {
      const binding = settingsBinding(row.id); if (!binding || binding.form !== 'runtime') return null;
      const value = readSettingsBinding(binding, {forms: {runtime: state.draft}}); if (!value) return null;
      // The authored option says "administrator"; this existing binding stores
      // the server's "admin" enum. Project it locally without changing the API.
      const shown = row.id === 'people.inviteRole' && value.current === 'admin' ? 'administrator' : value.current;
      return <NativeSettingControl key={row.id} row={row} value={shown} t={t} disabled={save.saving} onChange={next => {const projected = row.id === 'people.inviteRole' && next === 'administrator' ? 'admin' : next; const patch = settingsBindingPatch(binding, projected); form.set(current => applySettingsBindingPatch(current, patch));}}/>;
    })}
    {save.dirty && <View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={save.saving} onPress={() => group.set.discard()}>{t('action.discard')}</Button><Button loading={save.saving} disabled={!!save.problem} onPress={() => {void group.set.save();}}>{t('action.save')}</Button></View>}
    {save.saveError !== undefined && <StateView variant="inline" {...runtime.error(save.saveError, () => {void group.set.save();}, 'server-console')}/>}
  </NativeAdvancedSection>;
}
