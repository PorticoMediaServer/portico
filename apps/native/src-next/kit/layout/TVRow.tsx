import React, {cloneElement, useCallback, useState, type ReactElement} from 'react';
import {useTheme} from '../theme';
import {FocusScrollRowScope} from '../focus';

/**
 * Every horizontal scroller goes through a TVRow (TV-FOCUS-REVIEW step 5, W-17).
 *
 * Android's HorizontalScrollView is focusable itself whenever scrolling is enabled
 * (ReactHorizontalScrollViewManager.setScrollEnabled → setFocusable), and with no focusable descendant D-pad
 * focus lands on the scroller: nothing is drawn focused and the arrows go nowhere. Turning scrolling off makes it
 * no focus stop but also stops focus-follow scrolling, so it is off only while the row holds no focus stop.
 *
 * The row counts its live focus stops itself: every kit focus target (Focusable, Button, cards, fields) inside it
 * registers while it is enabled, and the row sets its one scroller's `scrollEnabled` (keeping a caller's own
 * `scrollEnabled={false}`). Touch always scrolls as the caller says.
 */
export function TVRow({children}: {children: ReactElement<{scrollEnabled?: boolean | null}>}) {
  const {isTV} = useTheme();
  const [stops, setStops] = useState(0);
  const count = useCallback((change: 1 | -1) => setStops(value => value + change), []);
  const own = children.props.scrollEnabled;
  return <FocusScrollRowScope count={count}>{isTV ? cloneElement(children, {scrollEnabled: stops > 0 && own !== false}) : children}</FocusScrollRowScope>;
}
