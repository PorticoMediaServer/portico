import React, {useRef, useState} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {defaultI18n} from '../../../../../../packages/i18n/src/index';
import {shellNativeFixtures as identity, shellNativeCases as fixture} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/shell-native-fixtures';
import type {Gallery} from '../../gallery-registry';
import {OverlaysProvider} from '../../../kit/overlays/OverlayHost';
import {Dialog} from '../../../kit/overlays/surface';
import {Menu} from '../../../kit/overlays/Menu';
import {Artwork} from '../../../kit/artwork/Artwork';
import {Icon} from '../../../kit/Icon';
import type {ProfileOption} from '../../../kit/artwork/Profiles';
import {useTheme} from '../../../kit/theme';
import {AccountMenu, NotificationItem, ProfileAvatar, ProfileSwitchRow, WatchTogetherPill} from '../../../kit/shell';

function AccountGallery() {
  const theme = useTheme();
  const [profile, setProfile] = useState<ProfileOption>(fixture.profiles[0]);
  const [open, setOpen] = useState(false);
  const [manage, setManage] = useState(false);
  const [result, setResult] = useState('');
  const anchor = useRef<View>(null);
  const choose = (id: string) => {const found = fixture.profiles.find(candidate => candidate.id === id); if (found) {setProfile(found); setOpen(false);}};
  const actions = [
    {id: 'profiles', icon: 'i-person' as const, label: defaultI18n.t('next.shell.switchProfile'), onPress: () => setManage(true)},
    {id: 'notifications', icon: 'i-bell' as const, label: defaultI18n.t('next.shell.notifications'), onPress: () => setResult(defaultI18n.t('next.shell.notifications'))},
    {id: 'server', icon: 'i-server' as const, label: defaultI18n.t('next.shell.switchServer'), onPress: () => setResult(defaultI18n.t('next.shell.switchServer'))},
    {id: 'settings', icon: 'i-settings' as const, label: defaultI18n.t('next.shell.settings'), onPress: () => setResult(defaultI18n.t('next.shell.settings'))},
    {id: 'together', icon: 'i-people' as const, label: defaultI18n.t('next.shell.watchTogether'), onPress: () => setResult(defaultI18n.t('next.shell.watchTogether'))},
    {id: 'downloads', icon: 'i-dl' as const, label: defaultI18n.t('next.shell.downloads'), onPress: () => setResult(defaultI18n.t('next.shell.downloads'))},
    {id: 'feedback', icon: 'i-flag' as const, label: defaultI18n.t('next.shell.feedback'), onPress: () => setResult(defaultI18n.t('next.shell.feedback'))},
    {id: 'help', icon: 'i-help' as const, label: defaultI18n.t('next.shell.help'), onPress: () => setResult(defaultI18n.t('next.shell.help'))},
    {id: 'signout', icon: 'i-out' as const, label: defaultI18n.t('next.shell.signOut'), onPress: () => setResult(defaultI18n.t('next.shell.signOut'))},
  ];
  return <OverlaysProvider><ScrollView contentContainerStyle={{padding: theme.isTV ? 96 : 20, gap: 24}}><ProfileAvatar testID="dev.shell.account.avatar" ref={anchor} profile={profile} unread size={theme.isTV ? 64 : 34} onPress={() => setOpen(true)}/><ProfileSwitchRow testID="dev.shell.account.switch" profiles={fixture.profiles} selectedId={profile.id} onSelect={choose} onManage={() => setManage(true)} onAdd={() => setManage(true)}/><Text style={[theme.type.body, {color: theme.colors.text}]}>{result}</Text></ScrollView>
    <AccountMenu testID="dev.shell.account.menu" profile={profile} accountLine={identity.serverName} open={open} onOpenChange={setOpen} anchorRef={anchor} viewerRole="owner" profiles={{profiles: fixture.profiles, selectedId: profile.id, onSelect: choose, onManage: () => setManage(true)}} actions={theme.isTV ? actions.filter(action => ['profiles', 'server', 'notifications', 'together'].includes(action.id)) : actions.filter(action => action.id !== 'profiles')} serverAction={{id: 'server-settings', label: defaultI18n.t('next.shell.serverSettings'), icon: 'i-server', onPress: () => setResult(defaultI18n.t('next.shell.serverSettings'))}}/>
    <Dialog testID="dev.shell.account.manage" open={manage} onOpenChange={setManage} title={defaultI18n.t('settings.section.profiles')}><ProfileSwitchRow testID="dev.shell.account.manage.switch" profiles={fixture.profiles} selectedId={profile.id} onSelect={id => {choose(id); setManage(false);}} onManage={() => setManage(false)}/></Dialog>
  </OverlaysProvider>;
}
function NotificationsGallery() {
  const theme = useTheme();
  const [notification, setNotification] = useState({...fixture.notification, read: false, archived: false});
  const [menu, setMenu] = useState(false);
  const [opened, setOpened] = useState(false);
  return <OverlaysProvider><ScrollView contentContainerStyle={{padding: 16}}><NotificationItem notification={notification} title={notification.title} body={notification.body} when={notification.when} subject={theme.platform === 'phone' ? <Icon name="i-dl" size={18} color={theme.colors.textSecondary}/> : <Artwork source={fixture.avatarSubject}/>} actions={[{id: 'open', label: defaultI18n.t('next.shell.native.open'), onPress: () => {setOpened(true); setNotification({...notification, read: true});}}]} menuLabel={defaultI18n.t('next.shell.native.moreOptions')} onMenu={() => setMenu(true)}/></ScrollView><Menu open={menu} onOpenChange={setMenu} title={notification.title} presentation="sheet" items={[{id: 'read', label: defaultI18n.t('next.shell.markRead'), onSelect: () => setNotification({...notification, read: true})}, {id: 'archive', label: defaultI18n.t('next.shell.archive'), onSelect: () => setNotification({...notification, archived: true})}, {id: 'inbox', label: defaultI18n.t('next.shell.moveToInbox'), onSelect: () => setNotification({...notification, archived: false})}]}/><Dialog open={opened} onOpenChange={setOpened} title={notification.title}><Text style={[theme.type.body, {color: theme.colors.text}]}>{notification.body}</Text></Dialog></OverlaysProvider>;
}
function TogetherGallery() {
  const theme = useTheme();
  const [open, setOpen] = useState(false);
  return <OverlaysProvider><View style={{padding: theme.isTV ? 96 : 20}}>{[
    {id: 'in-step', ...fixture.groupStates[0]}, {id: 'waiting-for-member', ...fixture.groupStates[1]},
    {id: 'reconnecting', ...fixture.groupStates[2]}, {id: 'host-reconnecting', ...fixture.groupStates[3]},
  ].map(state => <View key={state.id} style={{marginBottom: 20}}><WatchTogetherPill testID={`dev.shell.together.${state.id}`} name={fixture.group.name} status={state.status} waiting={state.waiting} people={fixture.profiles} accessibilityLabel={defaultI18n.t('next.shell.togetherLabel', {name: fixture.group.name, count: fixture.profiles.length, state: state.status})} onPress={() => setOpen(true)}/></View>)}</View><Dialog testID="dev.shell.together.detail" open={open} onOpenChange={setOpen} title={fixture.group.name} placement={theme.isTV ? 'side' : undefined}>{fixture.profiles.map(profile => <View key={profile.id} style={{paddingVertical: 12}}><Text style={[theme.type.body, {color: theme.colors.text}]}>{profile.name}</Text></View>)}</Dialog></OverlaysProvider>;
}
const galleries: readonly Gallery[] = [
  {id: 'shell-account', title: 'next.shell.native.accountGallery', render: AccountGallery, platforms: ['phone', 'tablet', 'tv']},
  {id: 'shell-notifications', title: 'next.shell.native.notificationGallery', render: NotificationsGallery, platforms: ['phone', 'tablet']},
  {id: 'shell-together', title: 'next.shell.native.togetherGallery', render: TogetherGallery, platforms: ['phone', 'tablet', 'tv']},
];
export default galleries;
