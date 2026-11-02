/**
 * W-17: whether a horizontal scroller may scroll, on TV, given what is inside it.
 *
 * Android's HorizontalScrollView is focusable itself (`setFocusable(true)`, descendants first), and
 * react-native-tvos ties that to the `scrollEnabled` prop: `ReactHorizontalScrollViewManager
 * .setScrollEnabled` calls `view.setFocusable(value)`. A scroll view has no `focusable` prop of
 * its own (only `ReactViewManager` does). So whenever a horizontal scroller on TV holds no
 * focusable descendant (a loading skeleton row, an empty row, a window of placeholders, passive or
 * disabled items) D-pad focus lands on the scroller itself: nothing is drawn as focused and the
 * arrows go nowhere.
 *
 * `scrollEnabled={false}` makes it non-focusable, but it also stops focus-follow scrolling
 * (`ReactHorizontalScrollView.computeScrollDeltaToGetChildRectOnScreen` returns 0 while scrolling
 * is off). It must therefore never be applied blanket-wide: a scroller that holds real, enabled
 * focus stops has to keep it on.
 *
 * The rule, in one place: on TV a horizontal scroller is scroll-enabled only while it contains at
 * least one focusable item. Touch is untouched (always enabled).
 */
export function tvScrollEnabled(isTV: boolean, hasFocusable: boolean): boolean {
  return !isTV || hasFocusable;
}
