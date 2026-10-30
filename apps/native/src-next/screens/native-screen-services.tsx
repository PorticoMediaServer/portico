import React, {useSyncExternalStore} from 'react';
import {NativeModules} from 'react-native';
import {useDockClearance} from '../kit/dock/DockClearance';
import type {ErrorContext} from '../../../../packages/client-core/src/presentation/errors';
import type {NativeShellBootstrapContext} from '../shell/bootstrap';
import {ToastHost} from '../kit/overlays';
import {StateView} from '../kit/states';
import {NativeScreenServices} from './screen-services';

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

/** Mount once in the genuine shared overlay outlet. */
export function NativeScreenFeedbackHost({service, bottomOffset}: {service: NativeScreenServices; bottomOffset?: number}) {
  const toast = useSyncExternalStore(service.subscribe, service.getSnapshot);
  const dockTop = useDockClearance();
  return <ToastHost toast={toast} onDismiss={service.dismiss} bottomOffset={bottomOffset} obstructionTop={dockTop}/>;
}

export function nativeScreenError(service: NativeScreenServices, context: ErrorContext) {
  return (error: unknown, retry: () => void) => <StateView variant="centered" {...service.error(error, retry, context)}/>;
}
