import React, {createContext, useContext, useEffect, useRef} from 'react';
import {Platform, TVFocusGuideView, View, type FocusGuideMethods, type StyleProp, type ViewStyle} from 'react-native';
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

export function CoveredLayer({covered, children, style, testID, hostRef, inertWhenUncovered = false}: {covered: boolean; children: React.ReactNode; style?: StyleProp<ViewStyle>; testID?: string;
  /** The layer's own native host, for a caller that claims focus into it (W-12: the shell's last fallback). */
  hostRef?: React.Ref<View>;
  /** A wrapper that only covers (the playback layer and the watch chrome under a presented state, #610): while
   *  uncovered it is no focus guide at all on TV, so it never changes D-pad entry into what it holds. Android:
   *  a view is a guide only with `autoFocus` or destinations. tvOS: the guide made while covered is removed by
   *  asking for no destinations once `autoFocus` is off (`setFocusDestinations` re-runs `handleFocusGuide`,
   *  which removes a guide with neither); a prop change alone would leave it, hiding the subtree.
   *  Android (#616 review): uncovering sends `tvFocusable=true`, which also makes the wrapper itself focusable
   *  (`ReactViewManager.setTvFocusable` calls `setFocusable`), and `FOCUS_AFTER_DESCENDANTS` offers it to the D-pad
   *  whenever nothing inside is (nothing playing, chrome hidden): an invisible full-screen target with no way out.
   *  No prop unblocks the subtree without that, so the layer always holds `AndroidSelfFocusStop`. */
  inertWhenUncovered?: boolean}) {
  const {isTV} = useTheme();
  const guide = useRef<View & FocusGuideMethods>(null);
  useEffect(() => {if (isTV && inertWhenUncovered && !covered) guide.current?.setDestinations?.([]);}, [isTV, inertWhenUncovered, covered]);
  const outer = useContext(Covered), inside = covered || outer;
  const hidden = {accessibilityElementsHidden: covered, importantForAccessibility: covered ? 'no-hide-descendants' as const : 'auto' as const};
  const content = <Covered.Provider value={inside}>{children}</Covered.Provider>;
  if (!isTV) return <View ref={hostRef} style={style} testID={testID} pointerEvents={covered ? 'none' : 'auto'} {...hidden}>{content}</View>;
  if (inertWhenUncovered) return <TVFocusGuideView ref={guide as never} style={style} testID={testID} focusable={!covered} autoFocus={covered} {...hidden}>
    {content}{Platform.OS === 'android' && <AndroidSelfFocusStop/>}</TVFocusGuideView>;
  return <TVFocusGuideView ref={hostRef as never} style={style} testID={testID} focusable={!covered} autoFocus {...hidden}>{content}</TVFocusGuideView>;
}

/** Android TV only: an empty `autoFocus` guide, 1×1 and far off screen, inside an inert-when-uncovered layer. It is
 *  always one of the layer's focusables (`ReactViewGroup.addFocusables` adds a guide itself, focusable or not), so
 *  the layer never offers its own full-screen box; and it can never hold focus (a guide's `requestFocus` moves to
 *  its first focusable descendant and it has none). Off screen and out of every beam, it is picked only when there
 *  is no other target in that direction, where the move did nothing anyway. Covered, the layer blocks it too. */
function AndroidSelfFocusStop() {
  return <TVFocusGuideView autoFocus pointerEvents="none" accessibilityElementsHidden importantForAccessibility="no-hide-descendants"
    testID="covered-layer-focus-stop" style={offScreen}/>;
}
const offScreen = {position: 'absolute', left: -4096, top: -4096, width: 1, height: 1} as const;
