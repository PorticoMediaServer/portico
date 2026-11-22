import {NativeModules} from 'react-native';
import type {NativeShellBootstrapContext} from '../shell/bootstrap';
import {NativeScreenServices} from './screen-services';

// The drawing parts live in the kit; they are re-exported here so every screen keeps its one import.
export {nativeScreenError, nativeScreenErrorPage, ScreenErrorPage} from '../kit/states/ScreenErrorPage';
export {NativeScreenFeedbackHost} from '../kit/overlays/ScreenFeedbackHost';

type Owner = Pick<NativeShellBootstrapContext, 'authority' | 'current' | 'subscribeAuthority' | 'navigation'>;
const services = new WeakMap<Owner, NativeScreenServices>();


/** Borrow the same feedback service for Home, Title and other bound modules.
 * The restored authority, rather than any route or menu, retires it. */
export function nativeScreenServices(context: Owner): NativeScreenServices {
  if (!context.current()) throw Object.assign(new Error('native_screen_scope_ended'), {name: 'AbortError'});
  let service = services.get(context);
  if (!service) {
    service = new NativeScreenServices(context, async () => {
      const native = NativeModules.PorticoRuntime;
      if (typeof native?.newRequestId !== 'function') throw new Error('native_request_id_unavailable');
      return native.newRequestId();
    });
    services.set(context, service);
  }
  return service;
}
