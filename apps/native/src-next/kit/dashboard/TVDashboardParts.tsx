import React, {type ReactNode} from 'react';
import {ScrollView, Text, View, type AccessibilityRole} from 'react-native';
import {Focusable} from '../controls';
import {Artwork} from '../artwork';
import {ScreenText} from '../layout/Screen';
import {ServerHeader, type ServerFact} from '../shell';
import {shellType} from '../shell/visual';
import {textLines, useTheme} from '../theme';

/** The TV Dashboard (TV35) inside Server settings: it scrolls beside the server page list. */
export function TVDashboardBody({children}: {children: ReactNode}) {
  return <View style={{flex: 1}}>
    <ScrollView contentContainerStyle={{paddingBottom: 80}} style={{flex: 1}}>{children}</ScrollView>
  </View>;
}

/** The server's name (its header, held to one line), "Dashboard", and the owner's health facts: how the server is (with
 * its dot), up for, free space, the database. */
export function TVDashboardHead({serverName, role, title, facts}: {serverName: string; role: 'owner' | 'administrator'; title: string; facts: readonly ServerFact[]}) {
  const theme = useTheme();
  return <>
    <View style={{height: 60, overflow: 'hidden'}}><ServerHeader serverName={serverName} viewerRole={role} audience="administrators" facts={[]}/></View>
    <Text accessibilityRole="header" style={[shellType(44, 800), {color: theme.colors.text, marginBottom: 18}]}>{title}</Text>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 26, minHeight: 32, marginBottom: 26}}>{facts.map(fact => <View key={fact.id} style={{flexDirection: 'row', alignItems: 'center', gap: 10}}>
      {fact.tone && fact.tone !== 'muted' && <View style={{width: 12, height: 12, borderRadius: 6, backgroundColor: fact.tone === 'warning' ? theme.colors.amber : theme.colors.healthy}}/>}
      <Text style={[shellType(22, fact.tone ? 700 : 400), {color: fact.tone === 'normal' ? theme.colors.text : theme.colors.textSecondary}]}>{fact.text}</Text>
    </View>)}</View>
  </>;
}

/** A section's name: Now playing, Alerts, Background tasks, Disk usage. The first has no room above it. */
export function TVDashboardSectionLabel({children, first = false, role}: {children: ReactNode; first?: boolean; role?: AccessibilityRole}) {
  const theme = useTheme();
  return <Text accessibilityRole={role} style={[shellType(20, 700), {color: theme.colors.textSecondary, marginBottom: 14, marginTop: first ? 0 : 36}]}>{children}</Text>;
}

/** One stream in Now playing: its artwork, the title and what it is, who (the username, then a different profile), the
 * device, where and how it plays, and how far it has played. */
export function TVDashboardSession({focusId, testID, accessibilityLabel, onFocus, onBlur, artwork, title, secondary, people, fraction}: {
  focusId: string; testID: string; accessibilityLabel: string; onFocus: () => void; onBlur: () => void;
  artwork?: React.ComponentProps<typeof Artwork>['source']; title: string; secondary: readonly string[];
  /** Who and where: the first is drawn strong. */
  people: readonly string[]; fraction?: number;
}) {
  const theme = useTheme();
  return <Focusable focusId={focusId} testID={testID} onFocus={onFocus} onBlur={onBlur} accessibilityRole="text" accessibilityLabel={accessibilityLabel} focusKind="row"
    style={{minHeight: 120, flexDirection: 'row', gap: 22, alignItems: 'center', padding: 16, marginBottom: 8, borderRadius: 18, backgroundColor: theme.colors.raised}}>
    {artwork && <View style={{width: 130, height: 76, borderRadius: 10, overflow: 'hidden'}}><Artwork source={artwork}/></View>}
    <View style={{flex: 1, gap: 8}}>
      <Text {...textLines(1)} style={[shellType(26, 700), {color: theme.colors.text}]}>{title}</Text>
      {secondary.length > 0 && <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>{secondary.map((value, index) => <Text {...textLines(1)} key={index} style={[shellType(20), {color: theme.colors.textSecondary, flexShrink: 1}]}>{value}</Text>)}</View>}
      <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 16}}>{people.map((value, index) => <Text {...textLines(1)} key={index} style={[shellType(20, index === 0 ? 700 : 400), {color: theme.colors.textSecondary}]}>{value}</Text>)}</View>
    </View>
    {fraction !== undefined && <View style={{width: 160, height: 8, borderRadius: 4, overflow: 'hidden', backgroundColor: theme.colors.line}}><View style={{height: '100%', backgroundColor: theme.colors.accentStrong, width: `${fraction * 100}%`}}/></View>}
  </Focusable>;
}

/** An alert or a background task: its words in a raised row. */
export function TVDashboardItem({focusId, testID, label, onFocus, onBlur}: {focusId: string; testID: string; label: string; onFocus?: () => void; onBlur?: () => void}) {
  const theme = useTheme();
  return <Focusable focusId={focusId} testID={testID} onFocus={onFocus} onBlur={onBlur} accessibilityRole="text" accessibilityLabel={label} focusKind="row"
    style={{backgroundColor: theme.colors.raised, borderRadius: 16, padding: 24, marginBottom: 8}}><ScreenText>{label}</ScreenText></Focusable>;
}

/** Load more (or Try again) at a list's end. It stays focusable while a page is read, so focus never falls out of the list. */
export function TVDashboardMore({focusId, testID, label, pending, onFocus, onPress}: {focusId: string; testID: string; label: string; pending: boolean; onFocus: () => void; onPress: () => void}) {
  return <Focusable focusId={focusId} testID={testID} onFocus={onFocus} disabled={pending} focusableWhenDisabled onPress={onPress}><ScreenText>{label}</ScreenText></Focusable>;
}

/** TV35's Disk usage rows: what each is and how much. */
export function TVDiskUsage({title, rows}: {title: string; rows: readonly Readonly<{id: string; label: string; value: string}>[]}) {
  const theme = useTheme();
  return <View style={{marginTop: 36}}>
    <Text accessibilityRole="header" style={[shellType(20, 700), {color: theme.colors.textSecondary, marginBottom: 14}]}>{title}</Text>
    {rows.map(row => <View key={row.id} style={{flexDirection: 'row', justifyContent: 'space-between', gap: 20, padding: 22, marginBottom: 3, borderRadius: 12, backgroundColor: theme.colors.raised}}>
      <Text style={[shellType(24, 600), {color: theme.colors.text}]}>{row.label}</Text>
      <Text style={[shellType(22, 600), {color: theme.colors.textSecondary}]}>{row.value}</Text>
    </View>)}
  </View>;
}
