/** The Android line (round 8): a focus ring is for keyboard and D-pad focus, never for touch. A touch hands focus to
 * what it opens (a sheet's first row, a dialog's Cancel) without the viewer having aimed at it, so focus that arrives
 * in the moment after a touch is not drawn. A key press has no touch before it, so its focus is. TV is always keys.
 * Plain module state, shared by the kit `Focusable` and the cards (and any part with its own Pressable). */
let lastTouch = 0;
export function noteTouch(): void {lastTouch = Date.now();}
export function focusFromTouch(isTV: boolean): boolean {return !isTV && Date.now() - lastTouch < 1000;}
