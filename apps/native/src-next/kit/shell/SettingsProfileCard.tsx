import React from 'react';
import {Text, View} from 'react-native';
import {ProfileFace, type ProfileOption} from '../artwork/Profiles';
import {Focusable} from '../controls/Focusable';
import {Icon} from '../Icon';
import {useTheme} from '../theme';

/** The profile card at the top of Settings on a phone (phone-app 16): the avatar, the name, "Signed in to … as …", a chevron.
 * The web kit's `SettingsProfileCard` draws the same card. */
export function SettingsProfileCard({profile, description, onPress, testID}: {profile: ProfileOption; description: string; onPress: () => void; testID?: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={profile.name} onPress={onPress} focusRadius={14}
    style={{marginHorizontal: 16, marginTop: 4, paddingHorizontal: 14, paddingVertical: 12, borderRadius: 14, backgroundColor: theme.colors.raised, flexDirection: 'row', alignItems: 'center', gap: 14}}>
    <ProfileFace initial={profile.initial} color={profile.color} size={56} scaleOnFocus={false}/>
    <View style={{flex: 1, minWidth: 0}}>
      <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{profile.name}</Text>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{description}</Text>
    </View>
    <Icon name="i-right" size={20} color={theme.colors.lineStrong}/>
  </Focusable>;
}
