import React, {type ReactNode} from 'react';
import {Platform, Text, View} from 'react-native';
import {CoverFan} from '../artwork';
import type {ArtworkSource} from '../artwork';
import {Focusable} from '../controls';
import {useFrameMetrics} from '../frames/metrics';
import {Icon} from '../Icon';
import {alpha} from '../shell/visual';
import {SkeletonBlock} from '../states';
import {useTheme} from '../theme';

/** The mono face, chosen when drawn (iOS names it Menlo). */
const monoFace = () => Platform.OS === 'ios' ? 'Menlo' : 'monospace'; // lint-strings-allow: a font's name, not copy

/** One library in the owner's list (owner-server, Libraries): its covers fanned, the name, what it holds, how its last
 * scan went (accent while scanning), and its ⋯. */
export function OwnerLibraryRow({name, counts, status, live, covers, square, first, onOpen, onMenu, menuLabel}: {name: string; counts: readonly string[];
  status: string; live: boolean; covers: readonly ArtworkSource[]; square: boolean; first: boolean; onOpen: () => void; onMenu: () => void; menuLabel: string}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 4, minHeight: 84, paddingHorizontal: theme.space.sm - 4, paddingVertical: theme.space.controlGap,
    borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft}}>
    <Focusable accessibilityRole="button" accessibilityLabel={name} onPress={onOpen} focusKind="row" focusRadius={10} style={{flex: 1, flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 4}}>
      <CoverFan covers={[...covers]} shape={square ? 'square' : 'poster'} variant={square ? 'music' : 'library'} width={62} height={square ? 56 : 66}/>
      <View style={{flex: 1, minWidth: 0, gap: 2}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{name}</Text>
        {counts.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.controlGap, rowGap: 2}}>
          {counts.map((part, index) => <Text key={index} style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{part}</Text>)}</View>}
        <Text style={[theme.type.captionStrong, {color: live ? theme.colors.accentStrong : theme.colors.textTertiary}]}>{status}</Text>
      </View>
    </Focusable>
    <Focusable accessibilityRole="button" accessibilityLabel={menuLabel} onPress={onMenu} focusKind="chip" focusRadius={16} hitSlop={8}
      style={{width: 34, height: 34, borderRadius: 17, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .06)}}>
      <Icon name="i-more" size={18} color={theme.colors.textSecondary}/></Focusable>
  </View>;
}

/** The list's rows while it first loads: a cover's box and two lines, three times. */
export function OwnerLibraryRowsLoading({label}: {label: string}) {
  const theme = useTheme();
  return <View accessibilityLabel={label} accessibilityRole="progressbar" style={{padding: theme.space.sm - 2, gap: theme.space.sm}}>
    {[0, 1, 2].map(row => <View key={row} style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.sm - 4}}>
      <SkeletonBlock width={62} height={62}/><View style={{flex: 1, gap: theme.space.tight}}><SkeletonBlock width="60%" height={14}/><SkeletonBlock width="40%" height={11}/></View>
    </View>)}
  </View>;
}

/** A library's page head: what it holds (the first count bold), Scanning while a scan runs or how the last one went,
 * then its actions (Scan now and ⋯). */
export function OwnerLibraryHeader({counts, scanning, status, actions}: {counts: readonly string[]; scanning?: string; status?: string; actions: ReactNode}) {
  const theme = useTheme(), frame = useFrameMetrics();
  // In a plain section it starts where the section labels do.
  return <View style={{gap: theme.space.tight - 2, paddingHorizontal: Math.max(0, frame.gutter - frame.cardInset)}}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: theme.space.sm, rowGap: 2}}>
      {counts.map((count, index) => <Text key={count} style={[index ? theme.type.rowHelp : theme.type.rowLabel, {color: index ? theme.colors.textSecondary : theme.colors.text}]}>{count}</Text>)}
      {scanning !== undefined && <Text style={[theme.type.captionStrong, {color: theme.colors.accentStrong}]}>{scanning}</Text>}
    </View>
    {status !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{status}</Text>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, marginTop: theme.space.xs}}>{actions}</View>
  </View>;
}

/** While a scan runs: a line saying the server is scanning, and Open dashboard under it. */
export function LibraryScanningNote({text, action}: {text: string; action: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.xs, padding: theme.space.sm - 4, borderRadius: 12, backgroundColor: alpha(theme.colors.accent, .12)}}>
    <View style={{flexDirection: 'row', gap: theme.space.controlGap}}>
      <Icon name="i-info" size={18} color={theme.colors.accentStrong}/>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary, flex: 1}]}>{text}</Text>
    </View>
    <View style={{flexDirection: 'row', marginLeft: 28}}>{action}</View>
  </View>;
}

/** A row's trailing status in its tone (healthy, amber, or as an alert in red), with Try again beside it when offered. */
export function LibraryStatusControl({text, tone, alert = false, action}: {text?: string; tone?: 'ok' | 'warn' | 'quiet'; alert?: boolean; action?: ReactNode}) {
  const theme = useTheme();
  const color = alert ? theme.colors.record : tone === 'warn' ? theme.colors.amber : tone === 'ok' ? theme.colors.healthy : theme.colors.textSecondary;
  return <View style={{flexDirection: 'row', alignItems: 'center', gap: theme.space.controlGap, flexShrink: 1}}>
    {text !== undefined && <Text accessibilityRole={alert ? 'alert' : 'text'} style={[alert ? theme.type.captionStrong : theme.type.rowLabel, {color, flexShrink: 1}]}>{text}</Text>}
    {action}
  </View>;
}

/** One file the scan skipped: its path in the mono face, then the server's reason. */
export function SkippedFileLine({path, message, first}: {path: string; message: string; first: boolean}) {
  const theme = useTheme();
  return <View style={{paddingVertical: theme.space.controlGap, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft, gap: 3}}>
    <Text selectable style={[theme.type.caption, {color: theme.colors.textSecondary, fontFamily: monoFace()}]}>{path}</Text>
    <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{message}</Text>
  </View>;
}
