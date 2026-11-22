import React, {type ComponentProps, type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {Button} from '../controls';
import {useTheme} from '../theme';

/** Account security groups (Signing in, Two-step, Your data): a quiet heading over one raised box of rows. */
export function SecurityGroup({title, children}: {title: string; children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: 10}}><Text accessibilityRole="header" style={[theme.type.captionStrong, {color: theme.colors.textSecondary}]}>{title}</Text>
    <View style={{borderRadius: 14, backgroundColor: theme.colors.raised, padding: 14, gap: 14}}>{children}</View></View>;
}

/** One row: what it is and its state, then its control, which wraps under it when the row is narrow. */
export function SecurityRow({name, detail, children}: {name: string; detail: string; children?: ReactNode}) {
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 12}}>
    <View style={{flex: 1, minWidth: 150, gap: 4}}><SecurityText>{name}</SecurityText><SecurityText secondary>{detail}</SecurityText></View>{children}
  </View>;
}

/** A connected Google or Apple sign-in: its name over Connected, plain, with no control. */
export function SecurityProvider({name, detail}: {name: string; detail: string}) {
  return <View><SecurityText>{name}</SecurityText><SecurityText secondary>{detail}</SecurityText></View>;
}

/** Body text, plain or secondary. */
export function SecurityText({children, secondary = false}: {children: string; secondary?: boolean}) {
  const theme = useTheme();
  return <Text style={[theme.type.body, {color: secondary ? theme.colors.textSecondary : theme.colors.text}]}>{children}</Text>;
}

/** A secret or a list of recovery codes: strong, and selectable so it can be copied by hand. */
export function SecuritySecret({children}: {children?: string}) {
  const theme = useTheme();
  return <Text selectable style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{children}</Text>;
}

/** A strong heading inside a sheet (the final deletion step). */
export function SecurityHeading({children}: {children: string}) {
  const theme = useTheme();
  return <Text accessibilityRole="header" style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{children}</Text>;
}

/** A sheet's content, or the account deletion block under the groups. */
export function SecurityStack({children, after = false}: {children: ReactNode; /** Set a little apart from what's above. */ after?: boolean}) {
  return <View style={{gap: 12, ...(after ? {marginTop: 8} : {})}}>{children}</View>;
}

/** A sheet's footer buttons. */
export function SecurityFooter({children}: {children: ReactNode}) {
  return <View style={{gap: 10}}>{children}</View>;
}

/** Deleting can't be undone: the danger-tinted button, never the default. */
export function SecurityDangerButton(props: ComponentProps<typeof Button>) {
  const theme = useTheme();
  return <Button {...props} style={{backgroundColor: `${theme.colors.danger}24`}} textStyle={{color: theme.colors.danger}}/>;
}

/** More sign-in options, tucked just under the Signing in group. */
export function SecurityMoreButton(props: ComponentProps<typeof Button>) {
  return <Button {...props} variant="text" size="compact" style={{alignSelf: 'flex-start', marginTop: -4}}/>;
}
