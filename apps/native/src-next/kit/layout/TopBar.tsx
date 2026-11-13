import React, {type ReactNode} from 'react';
import {StyleSheet, Text, View} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {BlurView} from '../../platform/native-blur';
import {glassRecipe} from '../../platform/glass';
import {Focusable} from '../controls/Focusable';
import {Icon, type IconName} from '../Icon';
import {useTheme} from '../theme';
import {alpha, shellType} from '../shell/visual';

/**
 * phone-app `.ph-top.solid`: the bar a page's content scrolls under. Ink at .82 over a 24 blur with one
 * hairline, in the tab bar's order (blur first, the tint above it), so the standard backdrop shows through
 * softly. The other order lets iOS's dark blur turn the band grey.
 */
export function BarSurface({hairline = true}: {hairline?: boolean}) {
  const theme = useTheme();
  // Clipped to the bar: Android's BlurView otherwise dims the whole screen under it (12 Nov, the library page).
  return <View pointerEvents="none" style={[StyleSheet.absoluteFill, {overflow: 'hidden'}, hairline ? {borderBottomWidth: 1, borderBottomColor: alpha(theme.colors.text, .07)} : null]}>
    {glassRecipe().nativeBlur && <BlurView pointerEvents="none" style={StyleSheet.absoluteFill} blurType="dark" blurAmount={24} reducedTransparencyFallbackColor={theme.colors.ink}/>}
    <View style={[StyleSheet.absoluteFill, {backgroundColor: alpha(theme.colors.ink, .82)}]}/>
  </View>;
}

/**
 * phone-app `.ph-top` before a page scrolls: ink at .75 fading to clear, so the wordmark and buttons read over the
 * hero and the backdrop. Its own full-bleed layer, so it spans the whole bar whatever padding the bar's row has.
 */
export function BarScrim() {
  const theme = useTheme();
  return <View pointerEvents="none" style={StyleSheet.absoluteFill}>
    <Svg accessible={false} width="100%" height="100%"><Defs><LinearGradient id="portico-bar-scrim" x1="0" y1="0" x2="0" y2="1">
      <Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.75}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={0}/>
    </LinearGradient></Defs><Rect width="100%" height="100%" fill="url(#portico-bar-scrim)"/></Svg>
  </View>;
}

/** phone-detail's round control over a page's own artwork: a 38 pt glass circle (blur under ink at .45). */
export const RoundBarButton = React.forwardRef<View, {icon: IconName; label: string; onPress: () => void; testID?: string; disabled?: boolean; active?: boolean}>(function RoundBarButton({icon, label, onPress, testID, disabled, active = false}, ref) {
  const theme = useTheme();
  return <Focusable ref={ref} testID={testID} accessibilityRole="button" accessibilityLabel={label} disabled={disabled} onPress={onPress} hitSlop={3} focusRadius={19} style={styles.round}>
    <View pointerEvents="none" style={{flex: 1, borderRadius: 19, overflow: 'hidden'}}>
      <BlurView blurType="dark" blurAmount={18} overlayColor="transparent" reducedTransparencyFallbackColor={theme.colors.ink} style={{flex: 1}}>
        <View collapsable={false} style={{flex: 1, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.ink, .45)}}>
          <Icon name={icon} size={22} color={active ? theme.colors.accentStrong : theme.colors.text}/>
        </View>
      </BlurView>
    </View>
  </Focusable>;
});

export type TopBarBack = Readonly<{label: string; onPress: () => void; testID?: string; disabled?: boolean}>;

/**
 * The one top bar of a pushed phone page (phone-app, phone-detail and phone-read `.bar`): a 44 pt row under
 * the status bar with "‹ Label" (16/600, secondary), a centred 17/700 title or control, and trailing actions.
 * Over a page's own artwork the back is the round glass button instead. `leadingTitle` is a page's large title
 * collapsed into the bar after it scrolls away (collections-46 46b): set after the back, not centred. `surface` draws the bar a list
 * scrolls under; `children` (a pivot strip, a search field) sit inside the same bar, under the row.
 */
export function TopBar({back, overArtwork = false, title, leadingTitle, center, trailing, surface = false, safeArea = true, children, testID}: {
  back?: TopBarBack; overArtwork?: boolean; title?: string; leadingTitle?: string; center?: ReactNode; trailing?: ReactNode;
  surface?: boolean; safeArea?: boolean; children?: ReactNode; testID?: string;
}) {
  const theme = useTheme(), insets = useSafeAreaInsets();
  return <View testID={testID} pointerEvents="box-none" style={{paddingTop: safeArea ? insets.top : 0, zIndex: theme.layers.sticky}}>
    {surface && <BarSurface/>}
    <View pointerEvents="box-none" style={styles.row}>
      {back && (overArtwork
        ? <RoundBarButton testID={back.testID} icon="i-left" label={back.label} disabled={back.disabled} onPress={back.onPress}/>
        : <Focusable testID={back.testID} accessibilityRole="button" accessibilityLabel={back.label} disabled={back.disabled} onPress={back.onPress} hitSlop={{top: 3, bottom: 3}} style={styles.back}>
          <View style={styles.backContent}>
            <Icon name="i-left" size={22} color={theme.colors.textSecondary}/>
            <Text numberOfLines={1} style={[shellType(16, 600), {color: theme.colors.textSecondary, flexShrink: 1}]}>{back.label}</Text>
          </View>
        </Focusable>)}
      {leadingTitle !== undefined && <Text accessibilityRole="header" numberOfLines={1} style={[shellType(17, 700), {flex: 1, marginLeft: 8, color: theme.colors.text}]}>{leadingTitle}</Text>}
      {(title || center) && <View pointerEvents="box-none" style={styles.centre}>
        {center ?? <Text accessibilityRole="header" numberOfLines={1} style={[shellType(17, 700), {color: theme.colors.text}]}>{title}</Text>}
      </View>}
      {trailing && <View style={styles.trailing}>{trailing}</View>}
    </View>
    {children}
  </View>;
}

const styles = StyleSheet.create({
  row: {height: 44, paddingLeft: 12, paddingRight: 10, flexDirection: 'row', alignItems: 'center'},
  back: {height: 38, maxWidth: 118, marginLeft: -4, paddingRight: 6, justifyContent: 'center'},
  backContent: {flexDirection: 'row', alignItems: 'center'},
  round: {width: 38, height: 38, borderRadius: 19},
  centre: {position: 'absolute', left: 118, right: 118, top: 0, height: 44, alignItems: 'center', justifyContent: 'center'},
  trailing: {marginLeft: 'auto', flexDirection: 'row', alignItems: 'center', gap: 6},
});
