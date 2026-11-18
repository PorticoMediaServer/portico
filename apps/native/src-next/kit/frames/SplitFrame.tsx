import React, {type ReactNode} from 'react';
import {ScrollView, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import {frameSpec} from '@portico/design';
import {Button} from '../controls/Button';
import {BottomSheet} from '../overlays/BottomSheet';
import {useTheme} from '../theme';
import {useFrameMetrics} from './metrics';

/** The pane's measurements: its width beside the page on each wide form. R5 interim (tablet-play 6 and 7: 380 pt
 * beside the guide; desktop-live live-sheet: 600; final3-tv-10: 534 of 1600, so 640 of 1920). The rules hand owns the
 * final numbers in `frameSpec`. */
export const paneSpec = {width: {phone: 0, medium: 380, expanded: 420, tv: 640}, webWidth: 600} as const; // R5 interim

export type FramePane = Readonly<{
  /** The pane's title, in large type: the programme, the rule, Rules. */
  title: string;
  /** One line over the title: the channel's mark and name, "Series rule". */
  eyebrow?: ReactNode;
  /** One line under the title: the episode, the counts. */
  subtitle?: string;
  close: Readonly<{label: string; onPress: () => void}>;
  /** The pane's body: rows, buttons, a form. */
  children: ReactNode;
  /** Buttons that stay at the pane's foot (Save, Cancel). */
  footer?: ReactNode;
}>;

export type SplitFrameProps = Readonly<{
  /** The page: a frame (`LiveFrame`, `BrowseFrame`). It takes the room the pane leaves. */
  children: ReactNode;
  /** The second pane, when one is open. Absent, the page has the whole window. */
  pane?: FramePane;
  testID?: string;
}>;

/** A page and a second pane on it (tablet-play 6 and 7, tablet 11b, desktop-live live-sheet, phone-guide g3, final3-tv-10):
 * the programme's details beside the guide, a rule beside the rules, Downloads' Rules beside the list. From tablet width
 * the pane is a raised column on the right edge and the page takes the rest, so the page stays usable; on a phone it is a
 * bottom sheet over the page; on TV the same column, with focus inside it. */
export function SplitFrame({children, pane, testID}: SplitFrameProps) {
  const theme = useTheme(), frame = useFrameMetrics();
  if (frame.form === 'phone') return <View testID={testID} style={{flex: 1}}>
    {children}
    {/* phone-guide g3: the sheet's own header is the pane's (eyebrow, title, subtitle); its body scrolls and its foot stays. */}
    {pane && <BottomSheet open onOpenChange={open => {if (!open) pane.close.onPress();}} title={pane.title} accessibilityLabel={pane.title} showClose={false}
      headerContent={<View style={{paddingHorizontal: frame.gutter, paddingBottom: theme.space.sm}}><PaneHeader pane={pane} sheet/></View>} footer={pane.footer}>
      <View style={{paddingHorizontal: frame.gutter, gap: frame.headingGap}}>{pane.children}</View>
    </BottomSheet>}
  </View>;
  return <View testID={testID} style={{flex: 1, flexDirection: 'row'}}>
    <View style={{flex: 1, minWidth: 0}}>{children}</View>
    {pane && <PaneColumn pane={pane}/>}
  </View>;
}

/** The pane's own header: the eyebrow, the title in large type (a sheet's is a row heading), the subtitle; a round close at the top right. */
function PaneHeader({pane, sheet = false}: {pane: FramePane; sheet?: boolean}) {
  const theme = useTheme(), frame = useFrameMetrics();
  return <View style={{flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.sm}}>
    <View style={{flex: 1, minWidth: 0, gap: theme.space.tight}}>
      {pane.eyebrow}
      <Text accessibilityRole="header" style={[sheet ? theme.type.rowHeading : theme.type.pageTitle, {color: theme.colors.text}]}>{pane.title}</Text>
      {pane.subtitle !== undefined && <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{pane.subtitle}</Text>}
    </View>
    {/* A sheet closes by its grab and the scrim; TV by the remote's Back, which the key hints name. */}
    {!sheet && frame.form !== 'tv' && <Button variant="glass" size="small" icon="i-x" iconOnly accessibilityLabel={pane.close.label} onPress={pane.close.onPress}/>}
  </View>;
}

/** From tablet width: a raised column on the right edge, its header, its scrolling body and its foot. */
function PaneColumn({pane}: {pane: FramePane}) {
  const theme = useTheme(), frame = useFrameMetrics(), insets = useSafeAreaInsets(), padding = frame.cardPadding;
  return <View style={{width: paneSpec.width[frame.form], backgroundColor: theme.colors.raised, paddingTop: Math.max(insets.top, frame.headerGap)}}>
    <View style={{paddingHorizontal: padding, paddingTop: padding}}><PaneHeader pane={pane}/></View>
    <ScrollView style={{flex: 1}} contentContainerStyle={{padding, gap: frame.headingGap, paddingBottom: pane.footer ? padding : Math.max(insets.bottom, frameSpec.sheet.footerClear)}}>{pane.children}</ScrollView>
    {pane.footer && <View style={{flexDirection: 'row', gap: theme.space.sm, paddingHorizontal: padding, paddingTop: theme.space.sm, paddingBottom: Math.max(insets.bottom, padding)}}>{pane.footer}</View>}
  </View>;
}
