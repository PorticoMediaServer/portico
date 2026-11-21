import React, {type ReactNode} from 'react';
import {Platform, ScrollView, Text, View, type LayoutChangeEvent} from 'react-native';
import {Focusable} from '../controls';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

/** The mono face, chosen when drawn (iOS names it Menlo). */
const monoFace = () => Platform.OS === 'ios' ? 'Menlo' : 'monospace';

/** Messages' detail window (owner-server 33): whether the server is recording more detail and until when, and the
 * owner's Start or Stop. */
export function DetailWindowCard({title, body, action}: {title: string; body: string; action?: ReactNode}) {
  const theme = useTheme();
  return <View style={{padding: theme.space.sm - 2, gap: theme.space.xs, borderRadius: 14, backgroundColor: theme.colors.raised}}>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{title}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{body}</Text>
    {action !== undefined && <View style={{flexDirection: 'row'}}>{action}</View>}
  </View>;
}

/** "Following": the green dot while Live is on. */
export function LiveMark({label}: {label: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.xs}}>
    <View style={{width: 8, height: 8, borderRadius: 4, backgroundColor: theme.colors.healthy}}/>
    <Text style={[theme.type.captionStrong, {color: theme.colors.healthy}]}>{label}</Text>
  </View>;
}

/** The message log: a sunken card of lines, newest first. */
export function MessageLog({children, onLayout}: {children: ReactNode; onLayout?: (event: LayoutChangeEvent) => void}) {
  const theme = useTheme();
  return <View onLayout={onLayout} style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.messageLog, borderWidth: 1, borderColor: theme.colors.lineSoft}}>{children}</View>;
}

/** One line of the log: its time, level (coloured by how bad) and kind in the mono face, then the message (selectable),
 * then its values. An error line is washed red. */
export function MessageLine({time, level, tone, kind, message, detail, first}: {time: string; level: string; tone: string; kind: string; message: string; detail?: string; first: boolean}) {
  const theme = useTheme(), error = tone === 'error';
  const levelColor = error ? theme.colors.danger : tone === 'warn' ? theme.colors.amber : tone === 'info' ? theme.colors.textSecondary : theme.colors.textTertiary;
  const meta = [theme.type.caption, {fontFamily: monoFace(), color: theme.colors.textTertiary}];
  return <View style={{gap: 3, paddingVertical: 7, paddingHorizontal: 12, backgroundColor: error ? alpha(theme.colors.danger, .07) : undefined, borderTopWidth: first ? 0 : 1, borderColor: alpha(theme.colors.lineStrong, .35)}}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.controlGap}}>
      <Text style={meta}>{time}</Text>
      <Text style={[theme.type.captionStrong, {fontFamily: monoFace(), color: levelColor}]}>{level}</Text>
      <Text style={meta}>{kind}</Text>
    </View>
    <Text selectable style={[theme.type.rowHelp, {color: error ? theme.colors.text : theme.colors.textSecondary}]}>{message}</Text>
    {detail !== undefined && <Text selectable style={meta}>{detail}</Text>}
  </View>;
}

/** A row in a troubleshooting list (a feedback report, an app report, a record): its title, a line under it, and its
 * status in the accent colour. Pressable when it opens something. */
export function TroubleshootingRow({title, detail, status, onPress, disabled, first, testID}: {title: string | readonly string[]; detail?: string; status?: string;
  onPress?: () => void; disabled?: boolean; first: boolean; testID?: string}) {
  const theme = useTheme(), titles = typeof title === 'string' ? [title] : title;
  const body = <>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap, rowGap: 3}}>
      {titles.map((part, index) => <Text key={index} style={[theme.type.rowLabel, {color: theme.colors.text}]}>{part}</Text>)}
    </View>
    {detail !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{detail}</Text>}
    {status !== undefined && <Text style={[theme.type.captionStrong, {color: theme.colors.accentStrong}]}>{status}</Text>}
  </>;
  const style = {paddingHorizontal: theme.space.sm - 2, paddingVertical: theme.space.sm - 4, gap: 4, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft};
  return onPress ? <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={titles.join(', ')} disabled={disabled} onPress={onPress} focusKind="row" focusRadius={0} style={style}>{body}</Focusable>
    : <View testID={testID} style={style}>{body}</View>;
}

/** A card for one thing the server reports on its own (a capability check, a file it couldn't prepare): its name, a word
 * for its state (green, amber or quiet), its sentences, and a path in the mono face. */
export function FactCard({title, state, lines, path, titleMono = false}: {title: string; state?: Readonly<{label: string; tone: string}>; lines: readonly string[];
  path?: string; /** The title is itself a path. */ titleMono?: boolean}) {
  const theme = useTheme();
  const tone = state?.tone === 'ok' ? theme.colors.healthy : state?.tone === 'warn' ? theme.colors.amber : theme.colors.textTertiary;
  return <View style={{padding: theme.space.sm - 4, gap: 4, borderRadius: 12, backgroundColor: theme.colors.raised}}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'space-between', columnGap: theme.space.controlGap, rowGap: 4}}>
      <Text selectable={titleMono} style={[theme.type.rowLabel, {color: theme.colors.text, flexShrink: 1}, titleMono ? {fontFamily: monoFace()} : null]}>{title}</Text>
      {state && <Text style={[theme.type.captionStrong, {color: tone}]}>{state.label}</Text>}
    </View>
    {lines.map((line, index) => <Text key={index} style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{line}</Text>)}
    {path !== undefined && <Text selectable style={[theme.type.rowHelp, {color: theme.colors.textTertiary, fontFamily: monoFace()}]}>{path}</Text>}
  </View>;
}

/** A report's text in a sheet: what it says and its attached log, in the mono face, scrolling within the sheet. */
export function ReportText({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <ScrollView style={{maxHeight: 320}}>
    <Text selectable style={[theme.type.rowHelp, {color: theme.colors.textSecondary, fontFamily: monoFace()}]}>{children}</Text>
  </ScrollView>;
}
