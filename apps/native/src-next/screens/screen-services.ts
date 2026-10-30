import {presentError, type ErrorContext} from '../../../../packages/client-core/src/presentation/errors';
import {deviceOnline} from '../platform/server-connections';
import type {ContentEntry} from '../../../../packages/client-core/src/library-content';
import type {ToastMessage} from '../kit/overlays/types';
import type {NativeShellRuntime} from '../shell/runtime';
import {nativeEntryRoute, nativeEntryRouteSupported, type NativeShellNavigation} from '../shell/routes';
import {NativeScreenLifetime, type NativeScreenAuthority} from './settings-live/lifetime';

export type NativeScreenServicesOwner = NativeScreenAuthority & Readonly<{
  navigation: Pick<NativeShellNavigation, 'open'>;
}>;

/** Authority-local feedback and navigation. A screen still owns its entry,
 * membership and action lifetime; this service does not substitute Home grants. */
export class NativeScreenServices {
  private readonly lifetime: NativeScreenLifetime;
  private readonly listeners = new Set<() => void>();
  private toast: ToastMessage | null = null;
  private sequence = 0;
  private off: () => void;
  private disposed = false;
  private readonly owner: NativeScreenServicesOwner;
  private readonly newRequestId: () => Promise<string>;

  constructor(owner: NativeScreenServicesOwner, newRequestId: () => Promise<string>) {
    this.owner = owner; this.newRequestId = newRequestId;
    this.lifetime = new NativeScreenLifetime(owner);
    this.lifetime.assert();
    this.off = owner.subscribeAuthority(() => {if (!this.lifetime.current()) this.dispose();});
  }

  getSnapshot = (): ToastMessage | null => this.lifetime.current() ? this.toast : null;
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener); return () => {this.listeners.delete(listener);};
  };
  private publish(): void {for (const listener of [...this.listeners]) listener();}
  dismiss = (id: string): void => {
    if (!this.lifetime.current() || this.toast?.id !== id) return;
    this.toast = null; this.publish();
  };

  requestId = async (): Promise<string> => {
    this.lifetime.assert();
    const id = await this.newRequestId();
    this.lifetime.assert();
    return id;
  };
  navigate = (entry: ContentEntry): void => {
    this.lifetime.assert();
    if (!nativeEntryRouteSupported(entry)) {this.reportError(Object.assign(new Error('unavailable'), {code: 'unavailable'}), 'detail'); return;}
    this.owner.navigation.open(nativeEntryRoute(entry), this.lifetime.authority);
  };
  /** One quiet confirmation, such as "Link copied". It replaces any toast on screen. */
  notice = (message: string): void => {
    if (!this.lifetime.current()) return;
    this.toast = Object.freeze({id: `native-screen-notice-${++this.sequence}`, message});
    this.publish();
  };
  reportError = (error: unknown, context: ErrorContext = 'generic'): void => {
    if (!this.lifetime.current()) return;
    const presented = presentError(error, context, {operation: 'action', deviceOnline: deviceOnline()});
    if (presented.silent) return;
    logErrorToast(error, context, presented.category);
    this.toast = Object.freeze({id: `native-screen-error-${++this.sequence}`, message: presented.body});
    this.publish();
  };
  /** Builds an error row's props during render, so it never throws (class 7): a row still mounted
   * after the authority retired keeps its message and offers no Try again. */
  error = (error: unknown, retry: () => void, context: ErrorContext = 'generic') => {
    const presented = presentError(error, context, {deviceOnline: deviceOnline()});
    return {title: presented.title, body: presented.body, tone: 'error',
      ...(this.lifetime.current() && (presented.action === 'try-again' || presented.action === 'refresh')
        ? {primaryAction: {id: 'retry', label: presented.actionLabel, onPress: () => {if (this.lifetime.current()) retry();}}} : {})} satisfies ReturnType<NativeShellRuntime['error']>;
  };

  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.off(); this.lifetime.dispose();
    this.toast = null; this.publish(); this.listeners.clear();
  };
}

/** Which failure raised an error toast (E-mobile-11/24: toasts from unidentified background reads).
 * Release builds too; codes and classes only. A message is kept only when it is itself a code. */
function logErrorToast(error: unknown, context: ErrorContext, category: string) {
  const value = (error ?? {}) as {name?: unknown; code?: unknown; status?: unknown; message?: unknown};
  const token = (raw: unknown) => typeof raw === 'string' && /^[A-Za-z0-9_.:-]{1,64}$/.test(raw) ? raw : undefined;
  // Passive: a diagnostic sink that fails never changes the toast.
  try {
    console.warn(`portico_error_toast ${JSON.stringify({context, category, code: token(value.code), status: typeof value.status === 'number' ? value.status : undefined,
      class: token(value.name) ?? (error instanceof Error ? error.constructor.name : typeof error), message: token(value.message)})}`);
  } catch { /* passive */ }
}
