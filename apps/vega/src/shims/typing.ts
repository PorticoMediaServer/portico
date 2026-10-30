import * as Vega from '@amazon-devices/react-native-kepler';

/**
 * What the rest of the app needs to know while a field is being typed into.
 *
 * The system keyboard is a window of its own, kept up by the focus of the field's input. Two
 * things in the app would take that focus away, and with it the keyboard:
 *
 * - A control that asks for focus as it appears (search results arriving under the keyboard).
 *   Such a request is held here until typing ends, and the last one made is where focus then
 *   goes; with none, focus goes back to the field. (On tvOS the keyboard is presented over the
 *   app, and requests beneath it wait in the same way.)
 * - Back, which closes the keyboard. The system also sends that press to the app (as a remote
 *   event and as a hardware Back), sometimes after the keyboard has reported itself closed. It
 *   was the keyboard's press, not the screen's: both are kept from the screen while a field is
 *   being typed into and for a moment after.
 */
type Control = {node(): unknown; live: boolean};

let typing = 0, until = 0, guard: {remove(): void} | undefined;
let preferred: Control | null = null;

export const isTyping = (): boolean => typing > 0;
export const keyboardHasBack = (): boolean => typing > 0 || Date.now() < until;

export function typingStarted(): void {
  if (typing === 0) preferred = null;
  typing++;
  // Registered last, so asked first; registered again each time to stay that way.
  guard?.remove();
  guard = Vega.BackHandler.addEventListener('hardwareBackPress', keyboardHasBack);
}

export function typingEnded(): void {
  typing = Math.max(0, typing - 1);
  until = Date.now() + 500;
}

/** A control asked for focus while the keyboard was up. */
export function preferAfterTyping(control: Control): void {
  preferred = control;
}

/** The control that last asked, if it is still there. Asked once. */
export function takePreferred<C extends Control>(): C | null {
  const control = preferred;
  preferred = null;
  return control?.live ? (control as C) : null;
}
