import React, {type ReactNode} from 'react';
import {Platform, Text, View} from 'react-native';
import {Focusable} from '../controls';
import {useFrameMetrics} from '../frames/metrics';
import {alpha} from '../shell/visual';
import {useTheme} from '../theme';

/** The mono face, chosen when drawn (iOS names it Menlo). */
const monoFace = () => Platform.OS === 'ios' ? 'Menlo' : 'monospace';

/** A folder the server keeps something in (Storage & backups): its name, the path (selectable, in the mono face), and a
 * line of help or a warning under it. */
export function StoragePath({label, path, help, warning, tone = 'default', children, testID}: {label: string; path?: string; help?: string;
  /** Said in red under the path ("The server can't write to this folder"). */ warning?: string; tone?: 'default' | 'caution'; children?: ReactNode; testID?: string}) {
  const theme = useTheme(), inset = useLabelInset();
  return <View testID={testID} style={{gap: theme.space.tight, paddingHorizontal: inset}}>
    <Text style={[theme.type.rowLabel, {color: tone === 'caution' ? theme.colors.amber : theme.colors.text}]}>{label}</Text>
    {path !== undefined && <Text selectable style={[theme.type.rowHelp, {color: theme.colors.textSecondary, fontFamily: monoFace()}]}>{path}</Text>}
    {help !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary}]}>{help}</Text>}
    {warning !== undefined && <Text accessibilityRole="alert" style={[theme.type.rowHelp, {color: theme.colors.record}]}>{warning}</Text>}
    {children}
  </View>;
}

/** What uses space (owner-server 8, Disk usage): the Total large, a line about it, then each kind of file the server keeps
 * with its size, its share of the total as a thin bar, and its help. */
export function StorageUsage({totalLabel, total, lede, rows, testID}: {totalLabel: string; total: string; lede: string;
  rows: readonly Readonly<{id: string; name: string; size: string; share?: number; help?: string}>[]; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
    <View style={{paddingHorizontal: theme.space.sm - 2, paddingVertical: theme.space.sm - 4, gap: theme.space.tight - 2}}>
      <View style={{flexDirection: 'row', justifyContent: 'space-between', alignItems: 'baseline'}}>
        <Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{totalLabel}</Text>
        <Text style={[theme.type.heading, {color: theme.colors.text}]}>{total}</Text>
      </View>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{lede}</Text>
    </View>
    {rows.map(row => <View key={row.id} style={{paddingHorizontal: theme.space.sm - 2, paddingVertical: theme.space.controlGap + 1, borderTopWidth: 1, borderTopColor: theme.colors.lineSoft}}>
      <View style={{flexDirection: 'row', justifyContent: 'space-between', gap: theme.space.controlGap}}>
        <Text style={[theme.type.rowLabel, {color: theme.colors.text, flex: 1}]}>{row.name}</Text>
        <Text style={[theme.type.rowLabel, {color: theme.colors.textSecondary}]}>{row.size}</Text>
      </View>
      {row.share !== undefined && <View accessible={false} style={{height: 5, borderRadius: 3, overflow: 'hidden', marginTop: theme.space.tight, marginBottom: 2, backgroundColor: alpha(theme.colors.text, .09)}}>
        <View style={{height: '100%', width: `${row.share * 100}%`, borderRadius: 3, backgroundColor: theme.colors.accent}}/></View>}
      {row.help !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, marginTop: 2}]}>{row.help}</Text>}
    </View>)}
  </View>;
}

/** One backup in the list (owner-server 12): when it was made, then its kind, size, the version that made it and whether
 * it's protected. Pressing it opens Restore and Delete. */
export function BackupRow({title, facts, onPress, disabled, first, testID, accessibilityLabel}: {title: string; facts: readonly string[]; onPress: () => void;
  disabled: boolean; first: boolean; testID: string; accessibilityLabel: string}) {
  const theme = useTheme();
  return <Focusable testID={testID} accessibilityRole="button" accessibilityLabel={accessibilityLabel} disabled={disabled} onPress={onPress} focusKind="row" focusRadius={0}
    style={{paddingHorizontal: theme.space.sm - 2, paddingVertical: theme.space.sm - 4, gap: theme.space.tight - 2, borderTopWidth: first ? 0 : 1, borderTopColor: theme.colors.lineSoft}}>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{title}</Text>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', columnGap: theme.space.sm - 4}}>
      {facts.map(fact => <Text key={fact} style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{fact}</Text>)}
    </View>
  </Focusable>;
}

/** A card holding rows (the backups list), raised with rounded corners. */
export function StorageList({children, testID}: {children: ReactNode; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{children}</View>;
}

/** A restore that waits for a restart: what will be restored, a line when the server can't restart itself, and its actions. */
export function PendingRestoreCard({title, note, actions, error, testID}: {title: string; note?: string; actions: ReactNode; error?: string; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{padding: theme.space.sm - 2, gap: theme.space.controlGap, borderRadius: 14, backgroundColor: theme.colors.raised}}>
    <Text style={[theme.type.rowLabel, {color: theme.colors.text}]}>{title}</Text>
    {note !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{note}</Text>}
    <StorageActions>{actions}</StorageActions>
    {error !== undefined && <Text accessibilityRole="alert" style={[theme.type.rowHelp, {color: theme.colors.record}]}>{error}</Text>}
  </View>;
}

/** Buttons in a row at their own width, wrapping when they don't fit (Change folder; Back up now; Restart now, Cancel).
 * `onPage` lines them up with the section labels, as a page's own text is; in a card or sheet they keep its padding. */
export function StorageActions({children, onPage = false}: {children: ReactNode; onPage?: boolean}) {
  const theme = useTheme(), inset = useLabelInset();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.space.xs, paddingHorizontal: onPage ? inset : 0}}>{children}</View>;
}

/** A sentence on the page: quiet (Checking…, Restarting…) or, as an alert, in red (Backup failed: …). */
export function StorageNote({children, alert = false, onPage = false}: {children: ReactNode; alert?: boolean; /** Lined up with the section labels. */ onPage?: boolean}) {
  const theme = useTheme(), inset = useLabelInset();
  return <Text accessibilityRole={alert ? 'alert' : 'text'} style={[theme.type.rowHelp, {color: alert ? theme.colors.record : theme.colors.textSecondary, paddingHorizontal: onPage ? inset : 0}]}>{children}</Text>;
}

/** A plain section's children sit at the card inset; text on the page starts where the section label does. */
function useLabelInset() {const frame = useFrameMetrics(); return Math.max(0, frame.gutter - frame.cardInset);}

/** A sheet's column of fields and lines, inset as the sheet's own title is (none on TV, where the side sheet has its own). */
export function StorageSheetBody({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.controlGap, paddingBottom: theme.space.tight - 2, paddingHorizontal: theme.isTV ? 0 : 20}}>{children}</View>;
}

/** A page section's column: its buttons, notes and list one under another. */
export function StorageColumn({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.controlGap}}>{children}</View>;
}
