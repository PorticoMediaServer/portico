import React from 'react';
import {Text, View} from 'react-native';
import {defaultI18n} from '../../../../packages/i18n/src';
import {Menu, type MenuEntry} from '../kit/overlays';
import {ProfileFace} from '../kit/artwork';
import {nativeType, useTheme} from '../kit/theme';
import type {NativeShellSnapshot} from './runtime';
import {tvFocusIds} from './tv-focus';

/** The rail's measured opener anchors the drawn continuous profile flyout. */
export function TVProfileMenu({snapshot, open, onOpenChange, returnFocusRef}: {
  snapshot: NativeShellSnapshot; open: boolean; onOpenChange: (open: boolean) => void; returnFocusRef: React.RefObject<View | null>;
}) {
  const account = snapshot.account, theme = useTheme(), unread = snapshot.unreadNotificationCount;
  const items: MenuEntry[] = account.actions.map(action => ({id: action.id, label: action.label, icon: action.icon,
    destructive: action.destructive, onSelect: action.onPress,
    trailing: action.id === 'notifications' && typeof unread === 'number' && Number.isSafeInteger(unread) && unread > 0
      ? <Text style={[nativeType({size: 20, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.accentStrong}]}>{defaultI18n.t('settings.unreadCount', {count: unread})}</Text>
      : action.trailing}));
  if (account.serverAction && account.viewerRole !== 'member') {
    const action = account.serverAction;
    items.push({id: 'server-divider', separator: true}, {id: action.id, label: action.label, icon: action.icon, tone: 'owner', onSelect: action.onPress,
      trailing: <Text style={[nativeType({size: 20, weight: 700, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textSecondary, backgroundColor: `${theme.colors.ink}99`, paddingVertical: 3, paddingHorizontal: 12, borderRadius: 8}]}>{defaultI18n.t('next.shell.viewOnly')}</Text>});
  }
  const context = <View style={{flexDirection: 'row', alignItems: 'center', gap: 18, paddingTop: 14, paddingHorizontal: 18, paddingBottom: 18}}>
    <ProfileFace {...snapshot.profile} size={56} scaleOnFocus={false}/>
    <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[nativeType({size: 28, weight: 800, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.text}]}>{snapshot.profile.name}</Text>
      {snapshot.serverName && <Text numberOfLines={1} style={[nativeType({size: 20, weight: 400, tracking: 0, lineHeight: 'normal'}, theme.textScale), {color: theme.colors.textTertiary, marginTop: 2}]}>{snapshot.serverName}</Text>}
    </View>
  </View>;
  return <Menu testID={tvFocusIds(snapshot.authority).profileMenu} open={open} onOpenChange={onOpenChange}
    title={snapshot.profile.name} presentation="anchored" placement="right-end" width={640} offset={168} alignOffset={-8}
    // No initial focus is named: the shared menu takes its own safe first entry - the first action
    // that is enabled and has no enabled action before it - so the drawn order decides, and an
    // action that is absent or disabled cannot be focused by name.
    anchorRef={returnFocusRef} context={context} items={items} returnFocusRef={returnFocusRef}/>;
}
