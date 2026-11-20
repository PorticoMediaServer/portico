import React from 'react';
import type {SettingsPeopleQuery} from '../../../../../../packages/client-core/src/settings-people';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Select, Tabs, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsDataList} from '../../../kit/settings/SettingsDataList';
import {SettingsHelp} from '../../../kit/settings/SettingsParts';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {NativePeopleSource} from '../../../screens/settings-live/server/people-source';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {ServerPeopleAdvanced, ServerInvitationEditor, ServerPersonDetails} from './ServerPeopleDetails';
import {InvitationReadySheet} from './ServerInvitationReady';
import {invitationState, roleLabel, usePeopleSource, useServerPeople} from './useServerPeople';

type Props = {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime};
type Frame = Pick<React.ComponentProps<typeof SettingsPageFrame>, 'back' | 'server'>;

export function ServerPeoplePage({ports, runtime, retry, ...frame}: Props & Frame & {retry?: React.ReactNode}) {
  const source = usePeopleSource({ports, runtime}), {t} = runtime.text;
  return <SettingsPageFrame {...frame} title={t('settings.server.people')}
    panel={source && <PeoplePanel key={ports.context.authority().accountId} source={source} ports={ports} runtime={runtime} retry={retry}/>}>
    <SectionCard><StateRow kind="loading"/></SectionCard>
  </SettingsPageFrame>;
}

function PeoplePanel({source, ports, runtime, retry}: Props & {source: NativePeopleSource; retry?: React.ReactNode}) {
  const {t} = runtime.text, people = useServerPeople(source);
  const {state, loading, page, held, ownerControls} = people;
  const searchLabel = t('web.shell.search'), role = t('web.access.roleLabel'), status = t('web.general.status');
  const sort = t('web.filters.sortBy'), direction = t('builder.preset.order'), inviteStatus = t('web.access.invitationStatus');
  const header = <>
    {retry}
    <Tabs value={state.tab} onValueChange={people.selectTab}
      items={[{value: 'people', label: t('settings.server.people')}, {value: 'invitations', label: t('settings.server.invitations')}]}/>
    <PanelBlock><TextField testID="people-search" accessibilityLabel={searchLabel} placeholder={searchLabel} value={people.search} maxLength={160}
      returnKeyType="search" editable={!state.busy} onChangeText={people.setSearch} onSubmitEditing={people.searchPeople}/></PanelBlock>
    <PanelBlock><OverlayFootRow layout="halves">
      <Button size="compact" variant="glass" onPress={people.searchPeople} disabled={loading}>{searchLabel}</Button>
      <Button variant="glass" size="compact" icon="i-refresh" disabled={loading} onPress={people.refresh}>{t('action.refresh')}</Button>
    </OverlayFootRow></PanelBlock>
    <SectionCard>
      {state.tab === 'people' ? <>
        <SettingsRow first label={role} control={<Select accessibilityLabel={role} value={state.query.role} disabled={loading}
          options={[{value: 'all', label: t('web.people.allRoles')}, ...(['owner', 'admin', 'member'] as const).map(role => ({value: role, label: roleLabel(t, role)}))]}
          onValueChange={role => {if (['all', 'owner', 'admin', 'member'].includes(role)) people.choose({role: role as SettingsPeopleQuery['role']});}}/>}/>
        <SettingsRow label={status} control={<Select accessibilityLabel={status} value={state.query.status} disabled={loading}
          options={[{value: 'all', label: t('web.people.allStatuses')}, {value: 'now', label: t('web.people.playingNow')},
            {value: 'month', label: t('next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.2.label')},
            {value: 'idle', label: t('web.people.inactive')}, {value: 'off', label: t('web.people.disabledBadge')}]}
          onValueChange={status => {if (['all', 'now', 'month', 'idle', 'off'].includes(status)) people.choose({status: status as SettingsPeopleQuery['status']});}}/>}/>
        <SettingsRow label={sort} control={<Select testID="people-sort" accessibilityLabel={sort} value={state.query.sort} disabled={loading}
          options={(['person', 'role', 'access', 'profiles', 'active'] as const).map((value, index) => ({value, label: t(([
            'next.settingsSpec.server.people.groups.0.rows.0.control.columns.0.label', 'next.settingsSpec.server.people.groups.0.rows.0.control.columns.1.label',
            'next.settingsSpec.server.people.groups.0.rows.0.control.columns.2.label', 'next.settingsSpec.server.people.groups.0.rows.0.control.columns.3.label',
            'next.settingsSpec.server.people.groups.0.rows.0.control.columns.4.label',
          ] as const)[index]!)}))}
          onValueChange={sort => {if (['person', 'role', 'access', 'profiles', 'active'].includes(sort)) people.choose({sort: sort as SettingsPeopleQuery['sort']});}}/>}/>
        <SettingsRow label={direction} control={<Select testID="people-direction" accessibilityLabel={direction} value={state.query.direction} disabled={loading}
          options={(['asc', 'desc'] as const).map(value => ({value, label: t('web.filters.direction', {kind: 'other', dir: value})}))}
          onValueChange={direction => {if (direction === 'asc' || direction === 'desc') people.choose({direction});}}/>}/>
      </> : <SettingsRow first label={inviteStatus} control={<Select accessibilityLabel={inviteStatus} value={state.invitationStatus} disabled={loading}
        options={[{value: '', label: t('web.access.allInvitations')}, ...['pending', 'accepted', 'expired', 'revoked'].map(value => ({value, label: invitationState(t, value)}))]}
        onValueChange={people.filterInvitation}/>}/>}
    </SectionCard>
    {ownerControls && <PanelBlock><Button testID="people-invite" variant="glass" size="compact" disabled={loading}
      onPress={() => people.invite()}>{t('web.access.inviteSomeone')}</Button></PanelBlock>}
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, people.refresh, 'server-console')}/>}
    {page && <SettingsHelp>{defaultI18n.number(page.matchedCount)} {t(state.tab === 'people'
      ? 'next.settingsSpec.server.people.groups.0.rows.0.control.noun.1' : 'next.settingsSpec.server.people.groups.1.rows.0.control.noun.1')}</SettingsHelp>}
    {loading && <SectionCard><StateRow kind="loading"/></SectionCard>}
  </>;

  const pendingInvitation = state.invitationOpening, invitation = pendingInvitation?.invitation;
  return <>
    <SettingsDataList testID="server-people" items={people.rows} itemKey={row => 'username' in row ? row.accountId : row.id}
      header={header} empty={!loading && page && <SectionCard><StateRow kind="empty" icon="i-person" title={t('web.people.noResults')}/></SectionCard>}
      row={item => 'username' in item
        ? <SectionCard>
          <SettingsRow first testID={`people-person-${item.accountId}`} label={item.username} accessibilityLabel={item.username}
            onPress={loading ? undefined : () => people.openPerson(item)}
            description={[roleLabel(t, item.role), item.email,
              item.allowedLibraries.length ? item.libraryNames.join(' · ') : t('server.people.allLibraries'),
              item.effectiveMaxStreams === null ? t('settings.server.unlimited') : t('server.people.streams', {count: item.effectiveMaxStreams}),
              item.profileNames.filter(name => item.profileNames.length > 1 || name !== item.username).join(' · '),
              item.liveState === 'watching' ? t('next.settingsSpec.server.people.groups.0.rows.0.control.filters.1.options.1.label')
                : item.liveState === 'listening' ? t('next.settings.listeningNow') : item.lastActiveAt ? defaultI18n.date(item.lastActiveAt) : '—',
            ].filter((part): part is string => !!part)}/>
          {ownerControls && item.status === 'left' && <PanelBlock>
            <Button testID={`people-let-back-in-${item.accountId}`} variant="glass" size="compact" disabled={loading}
              onPress={() => people.ask({row: item, action: 'letBackIn'})}>{t('next.settings.letBackIn')}</Button>
          </PanelBlock>}
        </SectionCard>
        : <SectionCard>
          <SettingsRow first label={item.notes || roleLabel(t, item.role)}
            description={[roleLabel(t, item.role), invitationState(t, item.state), defaultI18n.date(item.expiresAt)]}/>
          {ownerControls && item.state === 'pending' && <PanelBlock><OverlayFootRow layout="halves">
            <Button variant="glass" size="compact" disabled={loading} onPress={() => people.invite(item)}>{t('next.selection.edit')}</Button>
            <Button size="compact" variant="danger" disabled={loading} onPress={() => people.ask({action: 'revoke', invitation: item})}>{t('web.access.revoke')}</Button>
          </OverlayFootRow></PanelBlock>}
        </SectionCard>}
      footer={<>
        {(people.hasPrevious || !!page?.nextCursor) && <PanelBlock><OverlayFootRow layout="halves">
          {people.hasPrevious && <Button variant="glass" disabled={loading} onPress={people.previousPage}>{t('action.back')}</Button>}
          {!!page?.nextCursor && <Button variant="glass" disabled={loading} onPress={people.more}>{t('action.showMore')}</Button>}
        </OverlayFootRow></PanelBlock>}
        {ownerControls && <ServerPeopleAdvanced ports={ports} runtime={runtime}/>}
      </>}/>
    <BottomSheet scrollable={false} open={!!state.selected} onOpenChange={open => {if (!open) people.closePerson();}}
      title={state.selected?.username} dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy}>
      {state.opening && <SectionCard><StateRow kind="loading"/></SectionCard>}
      {state.person && <ServerPersonDetails key={`${state.person.accountId}:${state.person.revision}:${state.person.limitsRevision}`}
        source={source} person={state.person} runtime={runtime} ask={people.ask}/>}
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, people.retryPerson, 'server-console')}/>}
    </BottomSheet>
    {pendingInvitation?.ready && invitation
      ? <InvitationReadySheet key={`ready:${invitation.id}:${invitation.revision}`} source={source} opening={pendingInvitation} runtime={runtime}
        serverName={ports.context.serverName} signIn={ports.context.account.account.hostedAccountId ? 'portico' : 'local'}/>
      : pendingInvitation && <ServerInvitationEditor key={invitation ? `${invitation.id}:${invitation.revision}` : 'new'}
        source={source} opening={pendingInvitation} runtime={runtime}/>}
    <BottomSheet open={!!held} showClose={false} fitContent keyboardAvoiding onOpenChange={open => {if (!open) people.close();}}
      title={held?.action === 'revoke' ? t('web.access.revoke') : held?.action === 'letBackIn' ? t('next.settings.letBackInTitle', {name: held.row.username}) : held?.person.username}
      footer={<OverlayFootRow layout="halves">
        <Button variant="glass" onPress={people.close}>{t('action.cancel')}</Button>
        <Button variant={held?.action === 'role' || held?.action === 'enable' || held?.action === 'letBackIn' ? 'primary' : 'danger'}
          disabled={!held || held.action === 'remove' && people.typedName !== held.preview.username} onPress={() => {void people.confirm();}}>
          {t(held?.action === 'remove' ? 'action.delete' : held?.action === 'revoke' ? 'web.access.revoke' : held?.action === 'devices' ? 'action.signOut'
            : held?.action === 'letBackIn' ? 'next.settings.letBackIn' : 'action.save')}
        </Button>
      </OverlayFootRow>}>
      {held?.action === 'remove' ? <>
        <SettingsHelp>{t('next.settingsSpec.server.people.groups.0.rows.0.control.bulk.2.confirm')}</SettingsHelp>
        <PanelBlock><TextField label={held.preview.username} value={people.typedName} onChangeText={people.changeTypedName}
          autoCapitalize="none" autoCorrect={false}/></PanelBlock>
      </> : held && <SettingsHelp>{held.action === 'devices' ? t(held.device
        ? 'next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.0.control.itemAction.confirm'
        : 'next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.confirm')
        : held.action === 'letBackIn' ? t('next.settings.letBackInBody') : held.action === 'role' ? t('web.people.changeRole', {role: roleLabel(t, held.role!)})
          : held.action === 'disable' ? t('web.people.disableConfirm') : held.action === 'enable' ? t('web.people.enableConfirm') : t('web.access.revokeNoLonger')}</SettingsHelp>}
    </BottomSheet>
  </>;
}
