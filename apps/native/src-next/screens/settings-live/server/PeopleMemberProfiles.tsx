import React, {useLayoutEffect, useMemo, useState, useSyncExternalStore} from 'react';
import {Text, View} from 'react-native';
import type {AccessMember} from '../../../../../../packages/client-core/src/server-administration';
import type {MemberProfile} from '../../../../../../packages/client-core/src/member-profiles';
import type {ProfileArt} from '../../../../../../packages/client-core/src/profile-management';
import type {NativeShellRuntime} from '../../../shell/runtime';
import {useAvatarRoute} from '../../../platform/profile-avatars';
import {ProfileFace} from '../../../kit/artwork';
import {Button, TextField} from '../../../kit/controls';
import {BottomSheet} from '../../../kit/overlays';
import {SettingsRow} from '../../../kit/shell';
import {StateView} from '../../../kit/states';
import {useTheme} from '../../../kit/theme';
import {ProfileColorPicker, ProfileRestrictionsFields} from '../ProfileEditorFields';
import type {NativePeopleSource} from './people-source';
import {NativeMemberProfilesSource} from './member-profiles-source';

type Props = {people: NativePeopleSource; person: AccessMember; runtime: NativeShellRuntime};
export function PeopleMemberProfiles({people, person, runtime}: Props) {
  const [source, setSource] = useState<NativeMemberProfilesSource>();
  useLayoutEffect(() => {
    const next = new NativeMemberProfilesSource(people.context, person, () => people.current() && people.getSnapshot().person === person, runtime.reportError);
    setSource(next); void next.load(); return next.dispose;
  }, [people, person, runtime.reportError]);
  return source?.person === person && source.current() ? <Profiles source={source} runtime={runtime}/> : null;
}

export function Profiles({source, runtime}: {source: NativeMemberProfilesSource; runtime: NativeShellRuntime}) {
  useAvatarRoute(source.context.api);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), t = runtime.text.t, theme = useTheme();
  const chip = (label: string) => <Text key={label} style={[theme.type.caption, {color: theme.colors.textSecondary, paddingHorizontal: 8, paddingVertical: 3, borderRadius: 6, backgroundColor: theme.colors.raised}]}>{label}</Text>;
  return <View testID="people-profiles" style={{gap: 12}}>
    {state.items?.map(profile => <View key={profile.id} testID={`people-profile-${profile.id}`} style={{flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 72, paddingVertical: 12, borderBottomWidth: 1, borderBottomColor: theme.colors.lineSoft}}>
      <ProfileFace initial={profile.name.slice(0, 1).toUpperCase()} color={profile.art} locked={profile.pinRequired} size={44} source={source.picture(profile, 44)}/>
      <View style={{flex: 1, gap: 6}}><Text style={[theme.type.body, {color: theme.colors.text, fontWeight: '700'}]}>{profile.name}</Text>
        <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 6}}>{profile.primary && chip(t('profile.main'))}{profile.pinRequired && chip(t('profile.protected'))}</View>
      </View>
      {source.canManage(profile) && <Button testID={`people-profile-manage-${profile.id}`} variant="glass" size="compact" onPress={() => source.manage(profile)}>{t('next.settingsSpec.server.people.panels.person.tabs.2.groups.0.rows.0.control.itemAction.label')}</Button>}
    </View>)}
    {state.loading && <StateView title={t('status.loading')}/>}
    {state.changed && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('error.changed')}</Text>}
    {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {void source.load();}, 'server-console')}/>}
    {state.managing && <MemberProfileEditor key={`${state.managing.id}:${state.managing.revision}:${state.managing.restrictions.revision}`} source={source} profile={state.managing} runtime={runtime}/>}
  </View>;
}

export function MemberProfileEditor({source, profile, runtime}: {source: NativeMemberProfilesSource; profile: MemberProfile; runtime: NativeShellRuntime}) {
  useAvatarRoute(source.context.api);
  const state = useSyncExternalStore(source.subscribe, source.getSnapshot, source.getSnapshot), t = runtime.text.t, theme = useTheme();
  const [action, setAction] = useState<Readonly<{kind: 'rename' | 'colour' | 'limits'}>>(), [name, setName] = useState(profile.name), [art, setArt] = useState<ProfileArt>(profile.art), [draft, setDraft] = useState(profile.restrictions);
  const view = useMemo(() => ({source, profile, active: true, action: undefined as typeof action}), [source, profile]);
  useLayoutEffect(() => {view.active = true; return () => {view.active = false; view.action = undefined;};}, [view]);
  const current = () => view.active && view.source === source && view.profile === profile && view.action === action && source.canManage(profile) && source.getSnapshot().managing === profile;
  const open = (kind: 'rename' | 'colour' | 'limits') => {
    if (!current()) return;
    const next = Object.freeze({kind}); view.action = next; setAction(next); setName(profile.name); setArt(profile.art); setDraft(profile.restrictions);
  };
  const changed = JSON.stringify(draft) !== JSON.stringify(profile.restrictions);
  const save = () => {
    if (!current()) return;
    if (action?.kind === 'rename' && name.trim() && name.trim().length <= 120 && !/[\x00-\x1f\x7f]/.test(name)) void source.rename(profile, name);
    if (action?.kind === 'colour') void source.colour(profile, art);
    if (action?.kind === 'limits') void source.restrict(profile, {
      ratingSystem: draft.ratingSystem, maximumAgeRating: draft.maximumAgeRating, allowUnrated: draft.allowUnrated, blockedLabels: draft.blockedLabels,
      allowDownloads: draft.allowDownloads, allowLiveTv: draft.allowLiveTv, allowDvr: draft.allowDvr, allowWatchTogether: draft.allowWatchTogether,
    });
  };
  const title = action?.kind === 'rename' ? t('profile.renameTitle', {name: profile.name}) : action?.kind === 'limits' ? t('limits.title', {name: profile.name}) : profile.name;
  const dismiss = () => {if (current()) {if (action) {view.action = undefined; setAction(undefined);} else source.close();}};
  return <>
    <BottomSheet testID="people-profile-editor" open title={title} description={action?.kind === 'limits' ? t('limits.subtitle') : undefined} dismissOnBack={!state.busy} dismissOnGesture={!state.busy} dismissOnOutsidePress={!state.busy} onOpenChange={open => {if (!open) dismiss();}}
      footer={action ? <View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={state.busy} onPress={dismiss}>{t('action.cancel')}</Button>
        <Button testID="people-profile-save" loading={state.busy} disabled={state.busy || action?.kind === 'rename' && (!name.trim() || name.trim() === profile.name || /[\x00-\x1f\x7f]/.test(name)) || action?.kind === 'colour' && art === profile.art || action?.kind === 'limits' && !changed} onPress={save}>{t(action?.kind === 'limits' ? 'limits.save' : 'action.save')}</Button></View> : undefined}>
      {!action && <View style={{gap: 12}}>
        <View style={{alignItems: 'center', paddingVertical: 12}}><ProfileFace initial={profile.name.slice(0, 1).toUpperCase()} color={profile.art} locked={profile.pinRequired} size={64} source={source.picture(profile, 64)}/></View>
        {!theme.isTV && <Button testID="people-profile-picture" variant="glass" disabled={!source.canPickPicture(profile)} loading={state.busy} onPress={() => {if (current()) void source.choosePicture(profile);}}>{t(profile.avatar ? 'profile.changePicture' : 'profile.addPicture')}</Button>}
        {profile.avatar && <Button testID="people-profile-remove-picture" variant="glass" disabled={!source.canManage(profile)} onPress={() => {if (current()) void source.removePicture(profile);}}>{t('profile.removePicture')}</Button>}
        <Button testID="people-profile-rename" variant="glass" disabled={state.busy} onPress={() => {open('rename');}}>{t('profile.rename')}</Button>
        <Button testID="people-profile-colour" variant="glass" disabled={state.busy} onPress={() => {open('colour');}}>{t('next.settingsSpec.account.profile.groups.1.rows.1.label')}</Button>
        <Button testID="people-profile-limits" variant="glass" disabled={state.busy} onPress={() => {open('limits');}}>{t('profile.limits')}</Button>
        {profile.pinRequired && <SettingsRow label={t('profile.protected')} control={<Button testID="people-profile-remove-pin" variant="danger" size="compact" disabled={state.busy} onPress={() => source.askRemovePIN(profile)}>{t('profile.removePin')}</Button>}/>}
      </View>}
      {action?.kind === 'rename' && <TextField testID="people-profile-name" label={t('profile.name')} value={name} maxLength={120} editable={!state.busy} onChangeText={value => {if (current()) setName(value);}}/>}
      {action?.kind === 'colour' && <ProfileColorPicker testID="people-profile-colours" value={art} disabled={state.busy} onChange={value => {if (current()) setArt(value);}}/>}
      {action?.kind === 'limits' && <ProfileRestrictionsFields draft={draft} systems={state.systems} disabled={state.busy} recordingLocked={source.context.account.account.role !== 'owner' && !profile.restrictions.allowDvr} current={current} onChange={patch => {if (current()) setDraft(value => ({...value, ...patch}));}}/>}
      {state.changed && <Text accessibilityRole="alert" style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('error.changed')}</Text>}
      {state.error !== undefined && <StateView variant="inline" {...runtime.error(state.error, () => {void source.load();}, 'server-console')}/>}
    </BottomSheet>
    <BottomSheet testID="people-profile-pin-confirmation" open={state.pinConfirmation?.profile === profile} title={t('profile.removePinConfirm.title', {name: profile.name})} description={t('profile.removePinConfirm.body', {name: profile.name})} onOpenChange={open => {if (!open) source.cancelRemovePIN();}}
      footer={<View style={{flexDirection: 'row', gap: 8}}><Button variant="glass" disabled={state.busy} onPress={source.cancelRemovePIN}>{t('action.cancel')}</Button><Button testID="people-profile-confirm-remove-pin" variant="danger" disabled={state.busy} onPress={() => {void source.removePIN(profile, state.pinConfirmation);}}>{t('profile.removePin')}</Button></View>}><View/></BottomSheet>
  </>;
}
