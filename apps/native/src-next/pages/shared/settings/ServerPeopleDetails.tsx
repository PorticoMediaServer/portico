import React from 'react';
import type {AccessMember} from '../../../../../../packages/client-core/src/server-administration';
import {defaultI18n} from '../../../../../../packages/i18n/src';
import {Button, Segmented, Select, Switch, TextField} from '../../../kit/controls';
import {PanelBlock, SectionCard, SettingsPageFrame, StateRow} from '../../../kit/frames';
import {BottomSheet} from '../../../kit/overlays';
import {OverlayFootRow} from '../../../kit/overlays/surface';
import {SettingsDataList} from '../../../kit/settings/SettingsDataList';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {NativeAdvancedSection} from '../../../kit/settings/AdvancedSection';
import {NativeSettingControl} from '../../../kit/settings/SettingControl';
import type {NativeSettingsLivePorts} from '../../../screens/settings-live/ports';
import type {InvitationOpening, NativePeopleSource} from '../../../screens/settings-live/server/people-source';
import type {StorageForms} from '../../../screens/settings-live/server/storage/forms';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {ServerMemberProfiles} from './ServerMemberProfiles';
import {roleLabel, type Confirmation} from './useServerPeople';
import {usePeopleAdvanced, usePeopleAdvancedForms, usePeopleLibraryAccess, useServerInvitationEditor, useServerPersonDetails} from './useServerPeopleDetails';

export function ServerPersonDetails({source, person, runtime, ask}: {
  source: NativePeopleSource; person: AccessMember; runtime: NativeShellRuntime; ask: (value: Confirmation) => void;
}) {
  const {t} = runtime.text, detail = useServerPersonDetails(source, person, ask);
  const {state, tab, managed, notes, allLibraries, libraries, limits} = detail;
  const tabTitles = ['next.settingsSpec.server.people.panels.person.tabs.0.title', 'next.settingsSpec.server.people.panels.person.tabs.1.title',
    'next.settingsSpec.server.people.panels.person.tabs.2.title', 'next.settingsSpec.server.people.panels.person.tabs.3.title',
    'next.settingsSpec.server.people.panels.person.tabs.4.title'] as const;
  const tabs = <Segmented testID="people-person-tabs" variant="phone" fill value={tab} onValueChange={detail.selectTab}
    items={detail.tabs.map((value, index) => ({value, label: t(tabTitles[index]!)}))}/>;
  const devices = state.devices?.person === person ? state.devices.items : [];
  let body: React.ReactNode;
  if (tab === 'devices') body = <SettingsDataList testID="people-devices" items={devices} itemKey={device => device.id}
    row={(device, index) => <SectionCard>
      <SettingsRow first={index === 0} label={device.name || device.platform}
        description={[device.platform, device.app, device.appVersion, device.profileName, t('web.devices.lastSeen', {when: defaultI18n.date(device.lastSeenAt)})]
          .filter((part): part is string => !!part)}/>
      {managed && <PanelBlock><Button testID={`people-device-signout-${device.id}`} variant="glass" size="compact"
        disabled={state.busy || state.devicesLoading} onPress={() => detail.signOutDevice(device)}>{t('action.signOut')}</Button></PanelBlock>}
    </SectionCard>}
    empty={state.devicesLoading ? <SectionCard><StateRow kind="loading"/></SectionCard> : state.devices && !state.devicesError
      ? <SectionCard><StateRow kind="empty" icon="i-phone" title={t('web.devices.none')}/></SectionCard> : null}
    footer={<>
      {state.devicesError !== undefined && <StateView variant="inline" {...runtime.error(state.devicesError, detail.reloadDevices, 'server-console')}/>}
      {state.devices?.cursor && <PanelBlock><Button variant="glass" disabled={state.busy || state.devicesLoading}
        onPress={detail.moreDevices}>{t('action.showMore')}</Button></PanelBlock>}
      <PanelBlock><Button variant="text" size="compact" disabled={state.busy || state.devicesLoading}
        onPress={detail.reloadDevices}>{t('action.refresh')}</Button></PanelBlock>
      {managed && <PanelBlock><Button testID="people-devices-signout-all" variant="glass"
        disabled={state.busy || state.devicesLoading || !devices.length} onPress={() => detail.signOutDevice()}>
        {t('next.settingsSpec.server.people.panels.person.tabs.3.groups.0.rows.1.control.label')}</Button></PanelBlock>}
    </>}/>;
  else if (tab === 'access') {
    const remote = t('next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.1.label');
    body = <PeopleLibraryAccess source={source} runtime={runtime} all={allLibraries} selected={libraries}
      disabled={!managed || state.busy} onChange={detail.changeLibraries} footer={<>
        {managed && <PanelBlock><Button testID="people-save-libraries" variant="glass"
          disabled={state.busy || !allLibraries && !libraries.length || (allLibraries ? !person.allowedLibraries.length : JSON.stringify(libraries) === JSON.stringify(person.allowedLibraries))}
          onPress={detail.saveLibraries}>{t('action.save')}</Button></PanelBlock>}
        <SectionCard>
          {person.accountLimits && <SettingsRow first label={remote} control={<Switch accessibilityLabel={remote} checked={limits.allowRemote}
            disabled={!managed || state.busy} onCheckedChange={detail.changeRemote}/>}/>}
          <SettingsRow first={!person.accountLimits} label={t('web.access.maxStreams')} control={<Select
            accessibilityLabel={t('web.access.maxStreams')} value={detail.streamLimit} disabled={!managed || state.busy}
            options={[...(!detail.streamChoices.includes(detail.streamLimit) ? [{value: detail.streamLimit, label: detail.streamLimit, disabled: true}] : []),
              ...detail.streamChoices.map(value => ({value, label: value === 'default' ? t('next.settingsSpec.server.people.panels.person.tabs.1.groups.1.rows.0.control.options.0.label')
                : value === 'unlimited' ? t('settings.server.unlimited') : value}))]} onValueChange={detail.changeStreamLimit}/>}/>
        </SectionCard>
        {managed && <PanelBlock><Button testID="people-save-limits" variant="glass" disabled={state.busy || limits === person.limits}
          onPress={detail.saveLimits}>{t('action.save')}</Button></PanelBlock>}
      </>}/>;
  }
  else body = <SettingsPageFrame hideHeader title={person.username}>
    {tab === 'account' && <>
      <SectionCard><SettingsRow first label={t('next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.0.label')}
        value={person.username} description={person.email ?? undefined}/></SectionCard>
      <PanelBlock><TextField label={t('next.settingsSpec.server.people.panels.person.tabs.0.groups.0.rows.2.label')}
        value={notes} maxLength={500} editable={managed && !state.busy} onChangeText={detail.setNotes}/></PanelBlock>
      {managed && <>
        <PanelBlock><Button variant="glass" disabled={state.busy || notes === (person.notes ?? '')} onPress={detail.saveNotes}>{t('action.save')}</Button></PanelBlock>
        <SectionCard><SettingsRow first label={t('web.access.roleLabel')} control={<Select accessibilityLabel={t('web.access.roleLabel')}
          value={person.role} disabled={state.busy} options={(['member', 'admin'] as const).map(role => ({value: role, label: roleLabel(t, role)}))}
          onValueChange={detail.changeRole}/>}/></SectionCard>
        <PanelBlock><OverlayFootRow layout="halves">
          <Button variant={person.disabled ? 'glass' : 'danger'} disabled={state.busy} onPress={detail.changeEnabled}>
            {t(person.disabled ? 'next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.second.label'
              : 'next.settingsSpec.server.people.panels.person.tabs.0.groups.1.rows.0.control.label')}</Button>
          <Button variant="danger" disabled={state.busy} onPress={detail.reviewDeletion}>{t('action.delete')}</Button>
        </OverlayFootRow></PanelBlock>
      </>}
    </>}
    {tab === 'profiles' && <ServerMemberProfiles people={source} person={person} runtime={runtime}/>}
    {tab === 'advanced' && <>
      {person.accountLimits && <SectionCard><SettingsRow first label={t('next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.0.label')}
        control={<Switch testID="people-access-tokens" accessibilityLabel={t('next.settingsSpec.server.people.panels.person.tabs.4.groups.0.rows.0.label')}
          checked={limits.accessTokens} disabled={!managed || state.busy} onCheckedChange={detail.changeTokens}/>}/></SectionCard>}
      {managed && <PanelBlock><Button testID="people-save-limits" variant="glass" disabled={state.busy || limits === person.limits}
        onPress={detail.saveLimits}>{t('action.save')}</Button></PanelBlock>}
    </>}
  </SettingsPageFrame>;
  return <><PanelBlock>{tabs}</PanelBlock>{body}</>;
}

function PeopleLibraryAccess({source, runtime, all, selected, disabled, onChange, header, footer}: {
  source: NativePeopleSource; runtime: NativeShellRuntime; all: boolean; selected: readonly string[];
  disabled: boolean; onChange: (all: boolean, ids: readonly string[]) => void; header?: React.ReactNode; footer?: React.ReactNode;
}) {
  const {t} = runtime.text, {state, rows, retry} = usePeopleLibraryAccess(source, selected);
  const label = t('web.people.visibleLibraries');
  return <SettingsDataList testID="people-library-choices" items={all ? [] : rows} itemKey={library => library.id}
    header={<>{header}<SectionCard><SettingsRow first label={label}
      control={<Select testID="people-library-mode" accessibilityLabel={label} value={all ? 'all' : 'some'} disabled={disabled}
        options={[{value: 'all', label: t('server.people.allLibraries')},
          {value: 'some', label: t('next.settingsSpec.server.people.panels.person.tabs.1.groups.0.rows.0.control.options.1.label')}]}
        onValueChange={value => {if (value === 'all' || value === 'some') onChange(value === 'all', selected);}}/>}/></SectionCard></>}
    row={(item, index) => <SettingsRow first={index === 0} label={item.name} control={<Switch accessibilityLabel={item.name}
      checked={selected.includes(item.id)} disabled={disabled}
      onCheckedChange={checked => onChange(false, checked ? [...selected, item.id] : selected.filter(id => id !== item.id))}/>}/>}
    footer={<>
      {state.librariesLoading && <SectionCard><StateRow kind="loading"/></SectionCard>}
      {state.librariesError !== undefined && <StateView variant="inline" {...runtime.error(state.librariesError, retry, 'server-console')}/>}
      {footer}
    </>}/>;
}

export function ServerInvitationEditor({source, opening, runtime}: {source: NativePeopleSource; opening: InvitationOpening; runtime: NativeShellRuntime}) {
  const {t} = runtime.text, model = useServerInvitationEditor(source, opening), {state} = model;
  const dayLabels = ['next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.0.label',
    'next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.1.label', 'next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.2.label',
    'next.settingsSpec.server.people.advanced.groups.3.rows.0.control.options.3.label'] as const;
  return <BottomSheet scrollable={false} open keyboardAvoiding title={t('web.access.inviteSomeone')}
    dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy} onOpenChange={open => {if (!open) model.close();}}
    footer={<OverlayFootRow layout="halves">
      <Button variant="glass" disabled={state.busy} onPress={model.close}>{t('action.cancel')}</Button>
      <Button testID="people-save-invitation" disabled={state.busy || state.loading || !model.all && !model.libraries.length}
        onPress={model.save}>{t(opening.invitation ? 'action.save' : 'web.access.createInvitation')}</Button>
    </OverlayFootRow>}>
    <PeopleLibraryAccess source={source} runtime={runtime} all={model.all} selected={model.libraries}
      disabled={state.busy} onChange={model.changeLibraries} header={<>
        <PanelBlock><TextField label={t('next.settingsSpec.server.people.panels.invite.groups.0.rows.1.label')}
          description={t('next.settings.people.notesHelp')} value={model.notes}
          maxLength={500} editable={!state.busy} onChangeText={model.setNotes}/></PanelBlock>
        <SectionCard><SettingsRow first label={t('web.access.roleLabel')} control={<Select accessibilityLabel={t('web.access.roleLabel')}
          value={model.role} disabled={state.busy} options={(['member', 'admin'] as const).map(value => ({value, label: roleLabel(t, value)}))}
          onValueChange={model.changeRole}/>}/></SectionCard>
      </>} footer={<>
        {!opening.invitation && <SectionCard><SettingsRow first label={t('next.settingsSpec.server.people.advanced.groups.3.rows.0.label')}
          control={<Select accessibilityLabel={t('next.settingsSpec.server.people.advanced.groups.3.rows.0.label')} value={model.days} disabled={state.busy}
            options={(['1', '3', '7', '30'] as const).map((value, index) => ({value, label: t(dayLabels[index]!)}))} onValueChange={model.changeDays}/>}/></SectionCard>}
        {opening.invitation?.state === 'pending' && <PanelBlock><Button testID="people-share-invitation" variant="glass"
          disabled={state.busy} onPress={model.share}>{t('invite.share')}</Button></PanelBlock>}
        {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, model.close, 'server-console')}/>}
      </>}/>
  </BottomSheet>;
}

export function ServerPeopleAdvanced({ports, runtime}: {ports: NativeSettingsLivePorts; runtime: NativeShellRuntime}) {
  const group = usePeopleAdvancedForms(ports, runtime);
  return group ? <PeopleAdvanced key={group.mount} group={group} runtime={runtime}/> : null;
}

function PeopleAdvanced({group, runtime}: {group: StorageForms; runtime: NativeShellRuntime}) {
  const {t} = runtime.text, model = usePeopleAdvanced(group), {state, save} = model;
  return <NativeAdvancedSection testID="people-advanced" summary={save.dirty ? t('settings.save.unsaved') : t('next.settings.allDefaults')}>
    {state.loadError !== undefined && <StateView variant="inline" {...runtime.error(state.loadError, model.load, 'server-console')}/>}
    {model.rows.map(({row, value, onChange}) => <NativeSettingControl key={row.id} row={row} value={value} t={t} disabled={save.saving} onChange={onChange}/>)}
    {save.dirty && <PanelBlock><OverlayFootRow layout="halves">
      <Button variant="glass" disabled={save.saving} onPress={model.discard}>{t('action.discard')}</Button>
      <Button loading={save.saving} disabled={!!save.problem} onPress={model.submit}>{t('action.save')}</Button>
    </OverlayFootRow></PanelBlock>}
    {save.saveError !== undefined && <StateView variant="inline" {...runtime.error(save.saveError, model.submit, 'server-console')}/>}
  </NativeAdvancedSection>;
}
