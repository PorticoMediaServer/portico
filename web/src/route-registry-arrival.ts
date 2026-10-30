import {consumeAccountMoveFragment, type MoveCodeLink} from '@core/account-move.ts';
import {capturePorticoReturn, type CapturedPorticoReturn} from './platform/portico-return-dispatch';
import {captureWebAccountReturn, type TakeAccountReturn} from './route-registry-return';

export type TakeAccountMove = () => MoveCodeLink | undefined;
export type WebAccountArrival = Readonly<{
  takeAccountReturn?: TakeAccountReturn;
  takeAccountSecurity?: () => CapturedPorticoReturn | undefined;
  takeAccountMove?: TakeAccountMove;
  dispose: () => void;
}>;

/** Capture before installation/storage/render. A return owns its fixed path;
 * move links live only in this document's memory and never select authority. */
export function captureWebAccountArrival(location: Pick<Location, 'href'>, history: Pick<History, 'replaceState'>): WebAccountArrival {
  const candidate = new URL(location.href);
  if (candidate.pathname === '/auth/portico-return' && new URLSearchParams(candidate.hash.slice(1)).has('security')) {
    let captured: CapturedPorticoReturn | undefined = capturePorticoReturn(location, history);
    return Object.freeze({takeAccountSecurity: () => {const value = captured; captured = undefined; return value;}, dispose: () => {captured = undefined;}});
  }
  const takeAccountReturn = captureWebAccountReturn(location, history);
  if (takeAccountReturn) return Object.freeze({takeAccountReturn, dispose: () => {takeAccountReturn();}});
  const page = new URL(location.href);
  if (page.pathname !== '/sign-in' || !page.hash) return Object.freeze({dispose: () => {}});
  let captured: MoveCodeLink | undefined, failed = false, failure: unknown, scrubbed = false;
  try {
    captured = consumeAccountMoveFragment(page, {replaceState: (state, unused, url) => {
      history.replaceState(state, unused, url);scrubbed = true;
    }});
  } catch (error) {
    if (!scrubbed) throw error;
    failed = true;failure = error;
  }
  const dispose = () => {captured = undefined;failed = false;failure = undefined;};
  return Object.freeze({
    takeAccountMove: () => {
      const value = captured, error = failure, reject = failed;
      dispose();
      // Invalid input was already scrubbed. Reject at the entry boundary rather
      // than blanking startup or falling through to a saved selected family.
      if (reject) throw error;
      return value;
    },
    dispose,
  });
}
