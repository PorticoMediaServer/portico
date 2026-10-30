import React, {forwardRef, type ReactNode} from 'react';
import {StyleSheet, Text, View, type StyleProp, type ViewStyle} from 'react-native';
import {defaultI18n} from '../../../../../packages/i18n/src/index';
import type {Notification} from '../../../../../packages/client-core/src/notifications';
import type {FactPart} from '../../../../../packages/client-core/src/presentation/kit-model';
import {ProfileFace, type ProfileFaceProps, type ProfileOption} from '../artwork/Profiles';
import {Focusable, type FocusableProps} from '../controls/Focusable';
import {Popover} from '../overlays/Menu';
import type {PopoverProps} from '../overlays/types';
import {BlurView, glassRecipe} from '../../platform/native-blur';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {focusTestID} from '../focus/registry';
import {alpha, shellType} from './visual';
import type {ServerRole} from './authority';

/** The face is owned by artwork. This adds the real shell control and unread dot only. */
export const ProfileAvatar = forwardRef<View, Omit<FocusableProps, 'children'> & {profile: ProfileOption; size?: number; unread?: boolean}>(function ProfileAvatar({profile, size = 34, unread = false, style, ...props}, ref) {
  const {colors} = useTheme();
  return <Focusable {...props} ref={ref} accessibilityRole="button" accessibilityLabel={props.accessibilityLabel ?? profile.name} focusRadius={size / 2} style={state => [{width: size, height: size}, typeof style === 'function' ? style(state) : style]}>
    <ProfileFace {...profile} size={size} locked={profile.pinRequired} scaleOnFocus={false}/>
    {unread && <View pointerEvents="none" style={{position: 'absolute', top: 0, right: 0, width: 7, height: 7, borderRadius: 4, backgroundColor: colors.accentStrong, borderWidth: 2, borderColor: colors.ink}}/>}
  </Focusable>;
});

export function FactParts({parts, size = 13, color, gap = 12}: {parts: readonly FactPart[]; size?: number; color?: string; gap?: number}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', flexWrap: 'wrap', gap}}>{parts.map(part => <Text key={part.id} style={[shellType(size, part.role === 'identity' ? 700 : 600, 'normal', 0, theme.textScale), {color: color ?? (part.role === 'identity' ? theme.colors.text : theme.colors.textTertiary)}]}>{part.text}</Text>)}</View>;
}
export type AccountAction = Readonly<{id: string; label: string; icon: IconName; onPress: () => void; trailing?: ReactNode; destructive?: boolean}>;
export type ProfileSwitchRowProps = {
  testID?: string;
  profiles: readonly ProfileOption[];
  selectedId: string;
  onSelect: (id: string) => void;
  onManage: () => void;
  onAdd?: () => void;
};
export function ProfileSwitchRow({profiles, selectedId, onSelect, onManage, onAdd, testID}: ProfileSwitchRowProps) {
  const theme = useTheme();
  return <View style={{paddingHorizontal: 20, paddingTop: 4, paddingBottom: 16}}>
    <View style={{flexDirection: 'row', alignItems: 'center', marginBottom: 10}}>
      <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary, flex: 1}]}>{defaultI18n.t('settings.section.profiles')}</Text>
      <Focusable testID={controlID(testID, 'manage')} accessibilityRole="button" accessibilityLabel={defaultI18n.t('entry.group.manage')} onPress={onManage} hitSlop={10}><Text style={[shellType(13, 700), {color: theme.colors.textSecondary}]}>{defaultI18n.t('entry.group.manage')}</Text></Focusable>
    </View>
    <View style={{flexDirection: 'row', gap: 14, flexWrap: 'wrap'}}>
      {profiles.map(profile => <Focusable key={profile.id} testID={controlID(testID, 'profile', profile.id)} accessibilityRole="button" accessibilityLabel={profile.name} accessibilityState={{selected: profile.id === selectedId}} disabled={profile.disabled || Boolean(profile.unavailableReason)} onPress={() => onSelect(profile.id)} style={{width: 62, alignItems: 'center'}} focusRadius={10}>
        <ProfileFace {...profile} size={48} locked={profile.pinRequired} selected={profile.id === selectedId} scaleOnFocus={false}/>
        <Text numberOfLines={1} style={[shellType(12.5, 600), {color: theme.colors.textSecondary, marginTop: 6, maxWidth: 62}]}>{profile.name}</Text>
      </Focusable>)}
      {onAdd && <Focusable testID={controlID(testID, 'add')} accessibilityRole="button" accessibilityLabel={defaultI18n.t('profile.add')} onPress={onAdd} style={{width: 62, alignItems: 'center'}} focusRadius={10}><ProfileFace initial="" add size={48}/><Text style={[shellType(12.5), {color: theme.colors.textSecondary, marginTop: 6}]}>{defaultI18n.t('profile.add')}</Text></Focusable>}
    </View>
  </View>;
}

export type AccountMenuProps = Pick<PopoverProps, 'open' | 'onOpenChange' | 'anchorRef' | 'initialFocusRef' | 'returnFocusRef' | 'testID'> & {
  profile: ProfileOption;
  accountLine: string;
  actions: readonly AccountAction[];
  profiles?: ProfileSwitchRowProps;
  viewerRole: ServerRole;
  serverAction?: AccountAction;
};
export function AccountMenu({profile, accountLine, actions, profiles, viewerRole, serverAction, ...surface}: AccountMenuProps) {
  const theme = useTheme();
  const row = (action: AccountAction) => <Focusable key={action.id} testID={controlID(surface.testID, 'action', action.id)} accessibilityRole="button" accessibilityLabel={action.label} onPress={() => {surface.onOpenChange(false, 'action'); action.onPress();}} focusKind="row" focusRadius={10} style={{height: theme.isTV ? 64 : 46, marginHorizontal: 8, paddingHorizontal: 12, flexDirection: 'row', alignItems: 'center', gap: 14}}>
    <Icon name={action.icon} size={theme.isTV ? 28 : 22} color={action.destructive ? theme.colors.record : theme.colors.textSecondary}/><Text style={[shellType(theme.isTV ? 22 : 15), {color: action.destructive ? theme.colors.record : theme.colors.text, flex: 1}]}>{action.label}</Text>{action.trailing}
  </Focusable>;
  return <Popover {...surface} width={theme.isTV ? 480 : 340} placement="bottom-end" title={profile.name} showTitle={false} modal>
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 14, padding: 20}}><ProfileFace {...profile} size={48}/><View style={{flex: 1}}><Text style={[shellType(17, 700), {color: theme.colors.text}]}>{profile.name}</Text><Text style={[shellType(13, 400), {color: theme.colors.textTertiary, marginTop: 2}]}>{accountLine}</Text></View></View>
    {!theme.isTV && profiles && <ProfileSwitchRow {...profiles}/>}
    {actions.map(row)}
    {serverAction && theme.isTV && viewerRole !== 'member' && <View style={{marginTop: 6, paddingVertical: 6, borderTopWidth: 1, borderTopColor: alpha(theme.colors.text, .08), backgroundColor: alpha(theme.colors.accentDeep, .16)}}>{row({...serverAction, trailing: <Text style={[shellType(theme.isTV ? 18 : 12, 700), {color: theme.colors.textTertiary}]}>{defaultI18n.t('next.shell.viewOnly')}</Text>})}</View>}
  </Popover>;
}

export type NotificationItemProps = {
  testID?: string;
  notification: Pick<Notification, 'id' | 'read' | 'archived' | 'severity'>;
  title: string;
  body: string;
  when: string;
  subject: ReactNode;
  actions: readonly {id: string; label: string; onPress: () => void}[];
  onMenu: () => void;
  menuLabel: string;
  separator?: boolean;
  style?: StyleProp<ViewStyle>;
};
export function NotificationItem({notification, title, body, when, subject, actions, onMenu, menuLabel, separator = false, style, testID}: NotificationItemProps) {
  const theme = useTheme();
  return <View style={[{paddingTop: 14, paddingBottom: 14, paddingLeft: 18, paddingRight: 14, flexDirection: 'row', gap: 12, borderTopWidth: separator ? 1 : 0, borderTopColor: alpha(theme.colors.lineStrong, .45)}, style]}>
    {!notification.read && !notification.archived && <View style={{position: 'absolute', left: 7, top: 26, width: 6, height: 6, borderRadius: 3, backgroundColor: theme.colors.accentStrong}}/>}
    <View style={{width: 32, height: 32, borderRadius: 9, overflow: 'hidden', alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.raisedBright}}>{subject}</View>
    <View style={{flex: 1, minWidth: 0}}><Text style={[shellType(15, 700), {color: theme.colors.text, paddingRight: 30}]}>{title}</Text><Text style={[shellType(13, 400, 18.85), {color: theme.colors.textSecondary, marginTop: 3}]}>{body}</Text><Text style={[shellType(12), {color: theme.colors.textTertiary, marginTop: 5}]}>{when}</Text>
      <View style={{flexDirection: 'row', gap: 8, marginTop: 10, flexWrap: 'wrap'}}>{actions.map(action => <Focusable key={action.id} testID={controlID(testID, 'notification', notification.id, 'action', action.id)} accessibilityRole="button" accessibilityLabel={action.label} onPress={action.onPress} focusRadius={10} hitSlop={6} style={{height: 32, paddingHorizontal: 12, borderRadius: 10, backgroundColor: theme.colors.raisedHigh, alignItems: 'center', justifyContent: 'center'}}><Text style={[shellType(13, 700), {color: theme.colors.text}]}>{action.label}</Text></Focusable>)}</View>
    </View>
    <Focusable testID={controlID(testID, 'notification', notification.id, 'menu')} accessibilityRole="button" accessibilityLabel={menuLabel} onPress={onMenu} onLongPressAction={onMenu} style={{position: 'absolute', right: 8, top: 6, width: 34, height: 34, alignItems: 'center', justifyContent: 'center'}} focusRadius={17}><Icon name="i-more" size={20} color={theme.colors.textTertiary}/></Focusable>
  </View>;
}

export type WatchTogetherPillProps = {
  /** Caller includes the actual group identity in this fence. */
  testID?: string;
  name: string;
  status: string;
  waiting: boolean;
  people: readonly Pick<ProfileFaceProps, 'initial' | 'color' | 'tint' | 'source'>[];
  onPress: () => void;
  accessibilityLabel: string;
};
export function WatchTogetherPill({name, status, waiting, people, onPress, accessibilityLabel, testID}: WatchTogetherPillProps) {
  const theme = useTheme();
  const tv = theme.isTV;
  const blur = glassRecipe();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={accessibilityLabel} onPress={onPress} focusRadius={tv ? 32 : 21} style={{height: tv ? 64 : 42, maxWidth: tv ? 380 : 250, borderRadius: tv ? 32 : 21, borderWidth: 1, borderColor: alpha(theme.colors.text, .12), paddingLeft: tv ? 10 : 6, paddingRight: tv ? 26 : 13, gap: tv ? 14 : 8, flexDirection: 'row', alignItems: 'center', alignSelf: 'flex-start', backgroundColor: alpha(theme.colors.ink, tv ? .5 : .45)}}>
    {blur.nativeBlur && <View pointerEvents="none" style={[StyleSheet.absoluteFill, {borderRadius: tv ? 32 : 21, overflow: 'hidden'}]}><BlurView style={StyleSheet.absoluteFill} blurType="dark" blurAmount={tv ? 20 : 18} reducedTransparencyFallbackColor={theme.colors.ink}/></View>}
    <View style={{flexDirection: 'row'}}>{people.slice(0, 3).map((person, index) => <ProfileFace key={index} {...person} size={tv ? 42 : 28} scaleOnFocus={false} style={{marginLeft: index ? tv ? -11 : -8 : 0, borderWidth: tv ? 2.5 : 2, borderColor: theme.colors.ink, borderRadius: tv ? 23.5 : 16}}/>)}</View>
    <View style={{flexShrink: 1, minWidth: 0}}><Text numberOfLines={1} style={[shellType(tv ? 22 : 12.5, 700, tv ? 26 : 15), {color: theme.colors.text}]}>{name}</Text><View style={{flexDirection: 'row', alignItems: 'center', gap: tv ? 8 : 5}}><View style={{width: tv ? 9 : 6, height: tv ? 9 : 6, borderRadius: 5, backgroundColor: waiting ? theme.colors.amber : theme.colors.healthy}}/><Text numberOfLines={1} style={[shellType(tv ? 20 : 11.5, 600, tv ? 24 : 'normal'), {color: waiting ? theme.colors.amber : theme.colors.healthy}]}>{status}</Text></View></View>
  </Focusable>;
}

const controlID = (prefix: string | undefined, ...parts: readonly string[]) => prefix ? focusTestID(prefix, JSON.stringify(parts)) : undefined;
