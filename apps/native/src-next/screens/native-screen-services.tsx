import React, {useSyncExternalStore} from 'react';
import {NativeModules} from 'react-native';
import {useDockClearance} from '../kit/dock/DockClearance';
import type {ErrorContext} from '../../../../packages/client-core/src/presentation/errors';
import type {NativeShellBootstrapContext} from '../shell/bootstrap';
import {ToastHost} from '../kit/overlays';
import {StateLayout, StateView} from '../kit/states';
import type {StateContentProps} from '../kit/states/types';
import {useTheme} from '../kit/theme';
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

/** A screen's whole-page load failure (states.html 13, phone-detail.html): the standard backdrop, the state drawn below
 * the screen's own Back row and clear of the status bar, as the title page's removed and restricted states are. TV keeps
 * its side backdrop with the state centred. Inline places (a panel, a plate) keep nativeScreenError. */
export function nativeScreenErrorPage(service: NativeScreenServices, context: ErrorContext) {
  return (error: unknown, retry: () => void) => <ScreenErrorPage {...service.error(error, retry, context)}/>;
}
function ScreenErrorPage(props: StateContentProps) {
  const theme = useTheme();
  return <StateLayout {...props} testID={props.testID ?? 'screen-error-page'} contentTop={theme.isTV ? undefined : 250} backdropHeight={theme.isTV ? undefined : 420}/>;
}
