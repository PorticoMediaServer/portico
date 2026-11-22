import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Focusable, TextField} from '../controls';
import {Icon} from '../Icon';
import {ProfileFace} from '../artwork';
import {useTheme} from '../theme';

/** A dialog's or sheet's buttons, side by side. */
export function ProfileDialogFooter({children}: {children: ReactNode}) {
  return <View style={{flexDirection: 'row', gap: 8}}>{children}</View>;
}

/** A raised, rounded list (the profiles, a profile's actions). */
export function ProfileList({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{children}</View>;
}

/** Settings › Profiles: the list with its rows, then what's said under it. */
export function ProfileManagementBlock({children}: {children: ReactNode}) {
  return <View testID="settings-profile-management" style={{gap: 8}}>{children}</View>;
}

/** One profile in Settings › Profiles: its face (locked when it has a PIN), name, what it is, and More when manageable. */
export function ProfileListRow({testID, name, detail, initial, art, locked, source, more, disabled, onPress}: {testID: string; name: string; detail: string;
  initial: string; art: React.ComponentProps<typeof ProfileFace>['color']; locked: boolean; source?: React.ComponentProps<typeof ProfileFace>['source'];
  more: boolean; disabled: boolean; onPress: () => void}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={name} disabled={disabled} onPress={onPress} focusKind="row"
    style={{minHeight: 62, paddingHorizontal: 14, paddingVertical: 8, flexDirection: 'row', alignItems: 'center', gap: 12}}>
    <ProfileFace initial={initial} color={art} size={44} locked={locked} source={source} scaleOnFocus={false}/>
    <View style={{flex: 1}}><Text style={[theme.type.body, {color: theme.colors.text, fontWeight: '700'}]}>{name}</Text><Text style={[theme.type.caption, {color: theme.colors.textTertiary}]}>{detail}</Text></View>
    {more && <Icon name="i-more" size={20} color={theme.colors.textTertiary}/>}
  </Focusable>;
}

/** A profile's actions: its name and role over the raised list, then a problem and a notice. */
export function ProfileActionsBlock({heading, children, after}: {heading: string; children: ReactNode; after?: ReactNode}) {
  const theme = useTheme();
  return <View testID="profile-actions" style={{marginTop: 18}}>
    <Text style={[theme.type.captionStrong, {color: theme.colors.textTertiary, marginBottom: 8}]}>{heading}</Text>
    <ProfileList>{children}</ProfileList>
    {after}
  </View>;
}

/** A quiet line (inside a list, or a sheet's explanation). */
export function ProfileNote({children, testID, live = false}: {children: string; testID?: string; live?: boolean}) {
  const theme = useTheme();
  return <Text testID={testID} accessibilityLiveRegion={live ? 'polite' : undefined} style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** What just happened to a profile, said as it appears. */
export function ProfileNotice({children}: {children: string}) {
  const theme = useTheme();
  return <Text accessibilityLiveRegion="polite" style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: 8}]}>{children}</Text>;
}

/** The PIN editor's two entries with their labels. */
export function ProfilePinFields({children}: {children: ReactNode}) {
  return <View style={{gap: 12}}>{children}</View>;
}

/** A field's label above a PIN entry. */
export function ProfileFieldLabel({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.captionStrong, {color: theme.colors.text}]}>{children}</Text>;
}

/** What deleting a profile does. */
export function ProfileDeletionText({children}: {children: ReactNode}) {
  return <View style={{gap: 4}}>{children}</View>;
}

/** The deletion sheet's main line. */
export function ProfileDeletionBody({children, live = false}: {children: string; live?: boolean}) {
  const theme = useTheme();
  return <Text accessibilityLiveRegion={live ? 'polite' : undefined} style={[theme.type.body, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** A confirmation with nothing to say beyond its title and description still draws an (empty) body. */
export function ProfileEmptyBody() {
  return <View/>;
}

/** The TV PIN editor's Repeat field, set a little below Choose. */
export function ProfileRepeatField(props: React.ComponentProps<typeof TextField>) {
  return <TextField {...props} containerStyle={{marginTop: 12}}/>;
}
