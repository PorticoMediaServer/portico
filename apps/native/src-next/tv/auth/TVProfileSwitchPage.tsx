import React, {useCallback, useLayoutEffect, useRef, useState} from 'react';
import {Modal, View} from 'react-native';
import {createFocusRegistry, FocusProvider, holdTVPageModal, UncoveredWindow, type TVPageModalHold} from '../../kit/focus';
import {useNativeDismissal} from '../../kit/overlays/native-dismissal';
import {useOverlayLifecycle} from '../../kit/overlays/OverlayHost';
import {OVERLAY_PAINT_LIVE, OverlayPaintProvider} from '../../kit/overlays/OverlayPaintContext';

/**
 * TV: the in-app Switch profile picker, its PIN page and the forgotten-PIN page, as one full-screen page `Modal` over the
 * shell, drawn the way the launch picker is: outside the shell's route layer. Inside the pushed route's layer a face
 * given focus could not be left on Android TV (no direction moved), and with no ask nothing took focus at all.
 * The platform keeps everything under the `Modal` from taking D-pad focus and the held page-modal signal keeps the
 * shell's layers from asking for it; the page itself is not covered (`UncoveredWindow`). Android TV's Back reaches the
 * `Modal`'s `onRequestClose` (the key listener never sees it): one step back each time, the PIN page to the picker and the
 * picker away. When it has gone, focus returns to where it was opened from, or, after a profile switch, the page under
 * it takes its own entry (DS-427).
 */
export function TVProfileSwitchPage({leave, onBack, onGone, children}: {
  /** Set while the page is up: how the picker's last Back leaves (the platform's own dismissal, then `onGone`). */
  leave: React.MutableRefObject<(() => void) | undefined>;
  /** One step back (Back, the Menu key). */
  onBack: () => void;
  /** The page has gone: the route closes. */
  onGone: () => void;
  children: React.ReactNode;
}) {
  const dismissal = useNativeDismissal(onGone), hold = useRef<TVPageModalHold>(undefined);
  useLayoutEffect(() => {const lease = holdTVPageModal(); hold.current = lease; return lease.release;}, []);
  useLayoutEffect(() => {leave.current = dismissal.close; return () => {leave.current = undefined;};}, [leave, dismissal.close]);
  const pageCurrent = useCallback(() => hold.current?.pageCurrent() ?? true, []);
  // Its own window, so its own focus registry: the shell's keeps the opener retained until the page has gone, and the
  // PIN pad waits for its first key while any focus is retained in its registry.
  const [registry] = useState(createFocusRegistry);
  return <Modal visible={dismissal.visible} transparent={false} animationType="none" onRequestClose={onBack} onDismiss={dismissal.onDismiss}>
    <OverlayPaintProvider mode={OVERLAY_PAINT_LIVE} returnCurrent={pageCurrent}>
      <UncoveredWindow><TVProfileSwitchLifecycle onBack={onBack}><FocusProvider registry={registry}>{children}</FocusProvider></TVProfileSwitchLifecycle></UncoveredWindow>
    </OverlayPaintProvider>
  </Modal>;
}
/** The opener's capture and return, and the Menu key on tvOS. The picker and the PIN page take their own starting focus. */
function TVProfileSwitchLifecycle({onBack, children}: {onBack: () => void; children: React.ReactNode}) {
  useOverlayLifecycle(true, true, onBack);
  return <View style={{flex: 1}}>{children}</View>;
}
