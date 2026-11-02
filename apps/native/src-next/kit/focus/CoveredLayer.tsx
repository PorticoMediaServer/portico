import React from 'react';
import {TVFocusGuideView, View, type StyleProp, type ViewStyle} from 'react-native';
import {useTheme} from '../theme';

/**
 * A layer another screen can cover (the shell's base page under a pushed TV route). On TV,
 * while covered, nothing inside it can take D-pad focus: the native container blocks its
 * descendants (Android `FOCUS_BLOCK_DESCENDANTS`, tvOS unfocusable guide), so focus can never
 * escape from the screen on top into the one underneath. Screen readers skip it too.
 *
 * The element stays the same covered or not, so the page underneath keeps its state, scroll
 * and the focus it returns to. Uncovered, the container never takes focus itself: Android adds
 * it only when nothing inside can be focused, and it has no guide destinations of its own.
 */
export function CoveredLayer({covered, children, style, testID}: {covered: boolean; children: React.ReactNode; style?: StyleProp<ViewStyle>; testID?: string}) {
  const {isTV} = useTheme();
  const hidden = {accessibilityElementsHidden: covered, importantForAccessibility: covered ? 'no-hide-descendants' as const : 'auto' as const};
  if (!isTV) return <View style={style} testID={testID} pointerEvents={covered ? 'none' : 'auto'} {...hidden}>{children}</View>;
  return <TVFocusGuideView style={style} testID={testID} focusable={!covered} {...hidden}>{children}</TVFocusGuideView>;
}
