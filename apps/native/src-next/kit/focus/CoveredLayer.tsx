import React, {createContext, useContext} from 'react';
import {TVFocusGuideView, View, type StyleProp, type ViewStyle} from 'react-native';
import {useTheme} from '../theme';

/**
 * A layer another screen can cover (the shell's base page under a pushed TV route). On TV,
 * while covered, nothing inside it can take D-pad focus: the native container blocks its
 * descendants (Android `FOCUS_BLOCK_DESCENDANTS`, tvOS unfocusable guide), so focus can never
 * escape from the screen on top into the one underneath. Screen readers skip it too.
 *
 * The element stays the same covered or not, so the page underneath keeps its state, scroll
 * and the focus it returns to. It has no guide destinations of its own.
 *
 * Every TV layer is an `autoFocus` guide (W-9, W-12; TV focus review step 0): it remembers the
 * element last focused inside it and returns D-pad entry there, and a layer with nothing focusable
 * inside never takes focus itself (without it, Android focuses the empty container). On tvOS it is
 * also what keeps an uncovered layer alive: react-native-tvos makes a guide without `autoFocus` or
 * destinations unselectable (`isTVSelectable=false`), and uncovering changes no layout, so its
 * native guide is never removed and the whole page stays hidden from the focus engine (§4.4.1).
 */
/** W-9 (2eec0520): whether this subtree is under a covered layer. A control in it never acts and never draws
 * its focus ring, even if native focus was left on it when its layer was covered (covering blocks new focus but
 * cannot move focus that is already there). So OK can never reach a page the user can't see, and the ring
 * drawn on screen is always the control that answers. */
const Covered = createContext(false);
export function useCovered(): boolean {return useContext(Covered);}

export function CoveredLayer({covered, children, style, testID, hostRef}: {covered: boolean; children: React.ReactNode; style?: StyleProp<ViewStyle>; testID?: string;
  /** The layer's own native host, for a caller that claims focus into it (W-12: the shell's last fallback). */
  hostRef?: React.Ref<View>}) {
  const {isTV} = useTheme();
  const outer = useContext(Covered), inside = covered || outer;
  const hidden = {accessibilityElementsHidden: covered, importantForAccessibility: covered ? 'no-hide-descendants' as const : 'auto' as const};
  const content = <Covered.Provider value={inside}>{children}</Covered.Provider>;
  if (!isTV) return <View ref={hostRef} style={style} testID={testID} pointerEvents={covered ? 'none' : 'auto'} {...hidden}>{content}</View>;
  return <TVFocusGuideView ref={hostRef as never} style={style} testID={testID} focusable={!covered} autoFocus {...hidden}>{content}</TVFocusGuideView>;
}
