import React, {type ComponentProps, type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {Button} from '../controls';
import {useTheme} from '../theme';

/** The account workspace's page: its title with Done/Back on one line, then the page's content in one column. */
export function WorkspaceFrame({scrollRef, title, action, overlay, children}: {scrollRef: React.RefObject<ScrollView | null>; title: string; action: ReactNode;
  /** Drawn after the scrolling column, inside the page (the confirmation sheet). */ overlay?: ReactNode; children: ReactNode}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  return <View testID="phone-serverless-settings" style={{flex: 1}}>
    <ScrollView ref={scrollRef} contentContainerStyle={{paddingTop: insets.top + 20, paddingBottom: Math.max(24, insets.bottom + 16), paddingHorizontal: 24, gap: 18}}>
      <View style={{flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between'}}>
        <Text accessibilityRole="header" style={[theme.type.title, {color: theme.colors.text}]}>{title}</Text>
        {action}
      </View>
      {children}
    </ScrollView>
    {overlay}
  </View>;
}

/** Body text. */
export function WorkspaceText({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.body, {color: theme.colors.text}]}>{children}</Text>;
}

/** A quiet line. */
export function WorkspaceCaption({children}: {children: string}) {
  const theme = useTheme();
  return <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{children}</Text>;
}

/** What just happened, said to assistive technology as it appears. */
export function WorkspaceNotice({children}: {children: string}) {
  const theme = useTheme();
  return <Text accessibilityRole="alert" style={[theme.type.body, {color: theme.colors.text}]}>{children}</Text>;
}

/** One device, server or notification: a raised card, dimmed for a server that is offline. */
export function WorkspaceCard({testID, dim, children}: {testID?: string; /** Servers only: true while offline. */ dim?: boolean; children: ReactNode}) {
  const theme = useTheme();
  return <View testID={testID} style={{padding: 16, gap: 8, borderRadius: 12, backgroundColor: theme.colors.raised, ...(dim === undefined ? {} : {opacity: dim ? .62 : 1})}}>{children}</View>;
}

/** Newer and Older side by side. */
export function WorkspacePager({children}: {children: ReactNode}) {
  return <View style={{flexDirection: 'row', gap: 12}}>{children}</View>;
}

/** A confirmation sheet's buttons. */
export function WorkspaceFooter({children}: {children: ReactNode}) {
  return <View style={{gap: 10}}>{children}</View>;
}

/** Signing a device out or leaving a server: the danger-tinted button. */
export function WorkspaceDangerButton(props: ComponentProps<typeof Button>) {
  const theme = useTheme();
  return <Button {...props} style={{backgroundColor: `${theme.colors.danger}24`}} textStyle={{color: theme.colors.danger}}/>;
}
