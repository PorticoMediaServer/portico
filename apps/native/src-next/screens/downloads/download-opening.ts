import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {authorityKey} from '../../shell/model';
import {closeNativeDownload, getNativeDownloadSnapshot, type NativeDownloadRequest} from './request';

type Context = Pick<NativeShellBootstrapContext, 'api' | 'session' | 'authority' | 'current' | 'navigation'>;
const principalOf = (context: Context) => {
  const session = context.session();
  return session ? JSON.stringify([session.viewer.serverId, session.viewer.authority, session.viewer.accountId,
    session.viewer.profileId, session.viewer.role, session.sessionFamilyId, session.tokenGeneration,
    session.authorizationHorizon, session.deviceId, session.installationId]) : undefined;
};

type Binding = Readonly<{
  context: Context; api: Context['api']; navigation: Context['navigation'];
  route: ReturnType<Context['navigation']['getSnapshot']>['route'];
  revision: ReturnType<Context['navigation']['getSnapshot']>['revision'];
  originalAuthority: ReturnType<Context['authority']>; authority: string;
  principal: string | undefined; lifetime: NativeDownloadRequest['lifetime'];
}> & {retired: boolean; command?: object};
// Request custody outlives a component instance. StrictMode may replay the same
// original tuple, but a resident request never captures a successor's context.
// null reserves capture before supplied getters can reenter another constructor.
const bindings = new WeakMap<NativeDownloadRequest, Binding | null>();
function bindingFor(context: Context, request: NativeDownloadRequest): Binding | undefined {
  if (bindings.has(request)) return bindings.get(request) ?? undefined;
  if (getNativeDownloadSnapshot() !== request) return undefined;
  bindings.set(request, null);
  try {
    const api = context.api, navigation = context.navigation, snapshot = navigation.getSnapshot();
    const originalAuthority = Object.freeze({...context.authority()});
    const binding: Binding = {context, api, navigation, route: snapshot.route, revision: snapshot.revision,
      originalAuthority, authority: authorityKey(originalAuthority), principal: principalOf(context), lifetime: request.lifetime, retired: false};
    bindings.set(request, binding);
    return binding;
  } catch {return undefined;}
}

function paintFor(context: Context, binding: Binding | undefined, pageCurrent?: () => boolean): () => boolean {
  return () => {
    if (!binding || binding.retired) return false;
    try {
      const lifetime = binding.lifetime;
      if (context !== binding.context || lifetime && (lifetime.signal.aborted || lifetime.isCurrent() !== true)
        || pageCurrent && pageCurrent() !== true
        || !binding.principal || !context.current() || context.navigation !== binding.navigation
        || context.api !== binding.api || authorityKey(context.authority()) !== binding.authority || principalOf(context) !== binding.principal) {
        binding.retired = true; return false;
      }
      const now = context.navigation.getSnapshot();
      if (now.route !== binding.route || now.revision !== binding.revision) {binding.retired = true; return false;}
      return !binding.retired;
    } catch {binding.retired = true; return false;}
  };
}

/** Capture during the original layout only. The returned read-only permit may
 * outlive ordinary sheet disposal; it never claims commands or reads a latest
 * request/context, and shares the resident request's first original binding. */
export function captureNativeDownloadPaintCurrent(context: Context, request: NativeDownloadRequest, pageCurrent?: () => boolean): () => boolean {
  return paintFor(context, bindingFor(context, request), pageCurrent);
}

/** Business lifetime for exactly one shell sheet opening. Retained exit paint
 * must not retain this object as an interactive controller or restart it. */
export function createNativeDownloadOpening(context: Context, request: NativeDownloadRequest) {
  const abort = new AbortController(), binding = bindingFor(context, request);
  let retired = false, pending: object | undefined;
  const paintCurrent = paintFor(context, binding);
  // Check store identity again after supplied callbacks: they may replace an
  // equal-valued request while this original opening is being inspected.
  const current = () => !retired && !abort.signal.aborted && getNativeDownloadSnapshot() === request && paintCurrent()
    && !retired && !abort.signal.aborted && getNativeDownloadSnapshot() === request;
  const retire = () => {if (!retired) {retired = true; abort.abort();}};
  const close = (): boolean => {retire(); return closeNativeDownload(request);};
  return Object.freeze({request, signal: abort.signal, current, retire,
    /** Inert exit paint only: ordinary close retires business authority, while
     * a route/viewer change must also remove its retained pixels immediately. */
    paintCurrent,
    claim: (): object | undefined => {
      if (!binding || binding.command || pending || retired) return undefined;
      const ticket = pending = {};
      binding.command = ticket;
      if (current() && pending === ticket && binding.command === ticket) return ticket;
      if (pending === ticket) pending = undefined;
      if (binding.command === ticket) binding.command = undefined;
      return undefined;
    },
    owns: (ticket: object) => pending === ticket && binding?.command === ticket && current(),
    release: (ticket: object) => {
      if (pending !== ticket) return;
      pending = undefined;
      if (binding?.command === ticket) binding.command = undefined;
    },
    // Compare-close may retire the original request after its page leaves. It
    // can never dismiss a successor, even when every displayed value is equal.
    close,
    viewDownloads: (): boolean => {
      if (!binding || !current() || !close() || getNativeDownloadSnapshot() !== undefined || !paintCurrent()
        || getNativeDownloadSnapshot() !== undefined) return false;
      binding.navigation.open({kind: 'downloads'}, binding.originalAuthority);
      return true;
    },
  });
}

export type NativeDownloadOpening = ReturnType<typeof createNativeDownloadOpening>;
