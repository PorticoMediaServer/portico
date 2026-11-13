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
/** Whether a screen now sits on top of this subtree's own layer (a pushed route or the player over the page), not a
 * page `Modal`'s signal. An overlay opened from that layer belongs to it: it closes, and hands no focus back into it. */
const LayerCovered = createContext(false);
export function useLayerCovered(): boolean {return useContext(LayerCovered);}

/** Says a subtree is covered without blocking it natively: the shell's layers while a TV page `Modal` is over them
 * (`tv-page-modal`). The platform already keeps D-pad focus out of them; this keeps W-9 and the preferred asks
 * quiet, so nothing under the page claims focus in its own window. Blocking natively under a `Modal` would move tvOS
 * focus (D7). It adds no view. */
export function CoveredSignal({covered, children}: {covered: boolean; children: React.ReactNode}) {
  const outer = useContext(Covered);
  return <Covered.Provider value={covered || outer}>{children}</Covered.Provider>;
}

/** A page `Modal` drawn from inside the shell's layers (the in-app Switch profile page, from its route) is its own window
 * over all of them: while it is held the shell marks its layers covered, and that must not reach the page itself. */
export function UncoveredWindow({children}: {children: React.ReactNode}) {
  return <Covered.Provider value={false}><LayerCovered.Provider value={false}>{children}</LayerCovered.Provider></Covered.Provider>;
}

export function CoveredLayer({covered, children, style, testID, hostRef}: {covered: boolean; children: React.ReactNode; style?: StyleProp<ViewStyle>; testID?: string;
  /** The layer's own native host, for a caller that claims focus into it (W-12: the shell's last fallback). */
  hostRef?: React.Ref<View>}) {
  const {isTV} = useTheme();
  const outer = useContext(Covered), outerLayer = useContext(LayerCovered), inside = covered || outer, layer = covered || outerLayer;
  const hidden = {accessibilityElementsHidden: covered, importantForAccessibility: covered ? 'no-hide-descendants' as const : 'auto' as const};
  const content = <Covered.Provider value={inside}><LayerCovered.Provider value={layer}>{children}</LayerCovered.Provider></Covered.Provider>;
  if (!isTV) return <View ref={hostRef} style={style} testID={testID} pointerEvents={covered ? 'none' : 'auto'} {...hidden}>{content}</View>;
  return <TVFocusGuideView ref={hostRef as never} style={style} testID={testID} focusable={!covered} autoFocus {...hidden}>{content}</TVFocusGuideView>;
}
