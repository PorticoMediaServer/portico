import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import QRCode from 'react-native-qrcode-svg';
import {shellType} from '../shell/visual';
import {useTheme} from '../theme';

/** Server settings frame 29: a sign-in move under way. The direction, how many still wait (of the total only when the
 * server recorded one), where they sign in afterwards, since when, a progress bar and the card's action (Open People),
 * then `children` (Finish now while anyone still waits). */
export function SignInMoveCard({title, waiting, backIn, since, progress, action, children}: {title: string; waiting: string; backIn: string; since: string;
  progress?: Readonly<{total: number; done: number}>; action: ReactNode; children?: ReactNode}) {
  const theme = useTheme();
  return <View testID="server-signin-move" style={{backgroundColor: theme.colors.raised, borderRadius: 14, overflow: 'hidden'}}>
    <View style={{padding: 14, gap: 6}}>
      <Text style={[shellType(15, 700), {color: theme.colors.text}]}>{title}</Text>
      <Text style={[shellType(14, 600, 20), {color: theme.colors.text}]}>{waiting}</Text>
      <Text style={[shellType(13, 400, 18), {color: theme.colors.textSecondary}]}>{backIn}</Text>
      <Text style={[shellType(13, 400, 18), {color: theme.colors.textTertiary}]}>{since}</Text>
      {progress && <View testID="server-signin-progress" accessibilityRole="progressbar" accessibilityValue={{min: 0, max: progress.total, now: progress.done}}
        style={{height: 4, backgroundColor: theme.colors.lineSoft, borderRadius: 2, marginTop: 4}}>
        <View style={{height: 4, borderRadius: 2, backgroundColor: theme.colors.accentStrong, width: `${Math.max(0, Math.min(1, progress.done / progress.total)) * 100}%`}}/>
      </View>}
      {action}
    </View>
    {children}
  </View>;
}

/** On a TV, where the web settings can't open: the address as a code to scan, and in words. */
export function SignInWebCode({url, label}: {url: string; label: string}) {
  const theme = useTheme();
  return <View testID="server-signin-web-qr" style={{alignItems: 'center', gap: 12}}>
    <QRCode value={url} size={240}/>
    <Text style={[shellType(14, 400, 20), {color: theme.colors.textSecondary}]}>{label}</Text>
  </View>;
}

/** Buttons stacked a little apart (Finish now's two; the page's web and Refresh buttons). */
export function SignInFinishFooter({children}: {children: ReactNode}) {
  return <View style={{gap: 10}}>{children}</View>;
}
