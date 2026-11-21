import React, {type ReactNode} from 'react';
import {Text, View} from 'react-native';
import {useFrameMetrics} from '../frames/metrics';
import {useTheme} from '../theme';

/** A plain section's children sit at the card inset; text on the page starts where the section label does. */
function useLabelInset() {const frame = useFrameMetrics(); return Math.max(0, frame.gutter - frame.cardInset);}

/** A heading on a settings page inside a plain section, with its line of help (Messages; What this server can do). */
export function PageHeading({title, lede}: {title: string; lede?: string}) {
  const theme = useTheme(), inset = useLabelInset();
  return <View style={{gap: theme.space.tight - 2, paddingHorizontal: inset}}>
    <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text}]}>{title}</Text>
    {lede !== undefined && <Text style={[theme.type.rowHelp, {color: theme.colors.textSecondary}]}>{lede}</Text>}
  </View>;
}

/** A sentence on a settings page: quiet (Loading…, None yet) or, as an alert, in red. `onPage` lines it up with the
 * section labels; in a card or sheet it keeps that one's padding. */
export function PageNote({children, alert = false, strong = false, onPage = false}: {children: ReactNode; alert?: boolean; /** The empty state's own line, in the text colour. */ strong?: boolean; onPage?: boolean}) {
  const theme = useTheme(), inset = useLabelInset();
  return <Text accessibilityRole={alert ? 'alert' : 'text'} style={[strong ? theme.type.rowLabel : theme.type.rowHelp,
    {color: alert ? theme.colors.record : strong ? theme.colors.text : theme.colors.textSecondary, paddingHorizontal: onPage ? inset : 0}]}>{children}</Text>;
}

/** Buttons (and switches) in a row at their own width, wrapping when they don't fit. */
export function PageActions({children, onPage = false}: {children: ReactNode; onPage?: boolean}) {
  const theme = useTheme(), inset = useLabelInset();
  return <View style={{flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: theme.space.xs, paddingHorizontal: onPage ? inset : 0}}>{children}</View>;
}

/** A page section's column: headings, notes, cards and buttons one under another. */
export function PageColumn({children, testID}: {children: ReactNode; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{gap: theme.space.sm - 4}}>{children}</View>;
}

/** Rows in one raised card with rounded corners (reports, feedback, records). */
export function PageList({children, testID}: {children: ReactNode; testID?: string}) {
  const theme = useTheme();
  return <View testID={testID} style={{borderRadius: 14, overflow: 'hidden', backgroundColor: theme.colors.raised}}>{children}</View>;
}

/** A sheet's column of fields and lines, inset as the sheet's own title is (none on TV, where the side sheet has its own). */
export function SheetBody({children}: {children: ReactNode}) {
  const theme = useTheme();
  return <View style={{gap: theme.space.controlGap, paddingBottom: theme.space.tight - 2, paddingHorizontal: theme.isTV ? 0 : 20}}>{children}</View>;
}
