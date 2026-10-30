import {capturePorticoReturn, type CapturedPorticoReturn} from './platform/portico-return-dispatch';

export const accountReturnPath = '/auth/portico-return';
export type TakeAccountReturn = () => CapturedPorticoReturn | undefined;

/** Capture/scrub synchronously before installation, protected storage or render.
 * This is a one-use memory delivery, never a pending-kind or authority decision. */
export function captureWebAccountReturn(location: Pick<Location, 'href'>, history: Pick<History, 'replaceState'>): TakeAccountReturn | undefined {
  if (new URL(location.href).pathname !== accountReturnPath) return;
  let captured: CapturedPorticoReturn | undefined = capturePorticoReturn(location, history);
  return () => {const value = captured;captured = undefined;return value;};
}
