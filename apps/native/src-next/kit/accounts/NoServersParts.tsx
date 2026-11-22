import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {FormScrollView} from '../../platform/keyboard';
import {useTheme} from '../theme';

/** The empty directory on phones and tablets: an optional way back, the title and body (or a notice), then the page's
 * groups in one scrolling column that stays above the keyboard. */
export function NoServersFrame({back, title, body, error, children}: {back?: ReactNode; title: string; body: string; error?: ReactNode; children: ReactNode}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  return <View testID="phone-no-servers" style={{flex: 1}}>
    <FormScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={{paddingHorizontal: 24, paddingTop: insets.top + 24, paddingBottom: Math.max(24, insets.bottom + 16), gap: 20}}>
      {back != null && <View style={{alignSelf: 'flex-start'}}>{back}</View>}
      <View style={{gap: 12}}>
        <Text accessibilityRole="header" style={[theme.type.accountTitle, {color: theme.colors.text}]}>{title}</Text>
        <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{body}</Text>
        {error}
      </View>
      {children}
    </FormScrollView>
  </View>;
}

/** A raised group with its heading, an optional body, then its fields and buttons. */
export function NoServersCard({title, body, children}: {title: string; body?: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: 12, padding: 16, borderRadius: 16, backgroundColor: theme.colors.raised}}>
    <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{title}</Text>
    {body !== undefined && <Text style={[theme.type.body, {color: theme.colors.textSecondary}]}>{body}</Text>}
    {children}
  </View>;
}

/** Buttons stacked a little tighter than the page's groups. */
export function NoServersButtons({children}: {children: ReactNode}) {
  return <View style={{gap: 10}}>{children}</View>;
}

/** A quiet line inside a group (how an invitation works). */
export function NoServersNote({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{children}</Text>;
}
