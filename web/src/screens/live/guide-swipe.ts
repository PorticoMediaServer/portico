import {useRef, type MouseEvent, type PointerEvent} from 'react';

type Gesture = {id: number; x: number; y: number; horizontal: boolean};
const INTENT_PX = 12;
const SWIPE_PX = 48;

/** Touch changes the guide's existing time window; mouse and keyboard keep
 * their native tile behaviour. Vertical intent belongs to page scrolling. */
export function useGuideSwipe(onEarlier: () => void, onLater: () => void) {
  const gesture = useRef<Gesture | undefined>(undefined);
  const suppressClick = useRef(false);
  return {
    onPointerDown(event: PointerEvent<HTMLDivElement>) {
      suppressClick.current = false;
      if (!event.isPrimary) {gesture.current = undefined; return;}
      if (event.pointerType !== 'touch' && event.pointerType !== 'pen') return;
      gesture.current = {id: event.pointerId, x: event.clientX, y: event.clientY, horizontal: false};
    },
    onPointerMove(event: PointerEvent<HTMLDivElement>) {
      const current = gesture.current;
      if (!current || current.id !== event.pointerId) return;
      const x = Math.abs(event.clientX - current.x), y = Math.abs(event.clientY - current.y);
      if (!current.horizontal) {
        if (y >= INTENT_PX && y >= x) {gesture.current = undefined; return;}
        if (x < INTENT_PX || x < y * 1.5) return;
        current.horizontal = true;
        // Capture only after horizontal intent, so a tap still reaches its tile.
        event.currentTarget.setPointerCapture(event.pointerId);
      }
      suppressClick.current = true;
      event.preventDefault();
    },
    onPointerUp(event: PointerEvent<HTMLDivElement>) {
      const current = gesture.current;
      gesture.current = undefined;
      if (!current || current.id !== event.pointerId || !current.horizontal) return;
      suppressClick.current = true;
      const delta = event.clientX - current.x;
      if (Math.abs(delta) < SWIPE_PX) return;
      if (delta > 0) onEarlier(); else onLater();
    },
    onPointerCancel() {gesture.current = undefined;},
    onLostPointerCapture() {gesture.current = undefined;},
    onClickCapture(event: MouseEvent<HTMLDivElement>) {
      // Keyboard/programmatic activation has detail0 and is never a swipe click.
      if (!suppressClick.current || event.detail === 0) return;
      suppressClick.current = false;
      event.preventDefault();
      event.stopPropagation();
    },
  };
}
