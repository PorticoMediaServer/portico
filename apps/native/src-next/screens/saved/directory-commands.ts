import {PersonalSavedService, type PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import type {NativeShellRoute} from '../../shell/routes';
import type {NativeDestinationActivation, NativeDestinationActivationPort} from '../destinations/focus';
import {nativeDestinationLifetime} from '../destinations/lifetime';
import {nativeScreenServices} from '../native-screen-services';
import {linkNativeAbortSignals} from '../../platform/abort-signals';

export type SavedDirectoryCommandLease = Pick<NativeDestinationActivation, 'visible' | 'revision' | 'intent'>;

export type NativeSavedDirectoryCommands = Readonly<{
  pin: (resource: PersonalResource, lease: SavedDirectoryCommandLease, pinned: boolean) => Promise<void>;
  rename: (resource: PersonalResource, lease: SavedDirectoryCommandLease, name: string, summary: string) => Promise<void>;
  remove: (resource: PersonalResource, lease: SavedDirectoryCommandLease) => Promise<void>;
  dispose: () => void;
}>;

const cancelled = () => Object.assign(new Error('cancelled'), {name: 'AbortError', code: 'cancelled'});

/** One directory row's real canonical command. The lease is the exact activation
 * snapshot that drew the menu; the exact host route, authority and lifetime are
 * checked before and after every transport/request-id await. One owned command is
 * aborted synchronously on navigation, authority or activation change. */
export function createNativeSavedDirectoryCommands(context: NativeShellBootstrapContext, route: NativeShellRoute, activation: NativeDestinationActivationPort): NativeSavedDirectoryCommands {
  const lifetime = nativeDestinationLifetime(context), common = nativeScreenServices(context);
  let active: AbortController | undefined, disposed = false;

  const run = async (resource: PersonalResource, lease: SavedDirectoryCommandLease, action: 'pin' | 'update' | 'delete', command: (service: PersonalSavedService) => Promise<boolean>) => {
    if (disposed || active) throw Object.assign(new Error('busy'), {code: 'busy'});
    const ticket = new AbortController(); active = ticket;
    const live = () => {
      if (disposed || ticket.signal.aborted || !context.current() || !lifetime.current()) return false;
      if (context.navigation.getSnapshot().route !== route) return false;
      const now = activation.getSnapshot();
      return now.visible && now.revision === lease.revision && now.intent === lease.intent;
    };
    const stop = () => ticket.abort();
    const offs = [activation.subscribe(stop), context.navigation.subscribe(stop), context.subscribeAuthority(stop)];
    const assert = () => {if (!live()) throw cancelled();};
    let service: PersonalSavedService | undefined;
    try {
      assert();
      if (!resource.actions.includes(action)) throw cancelled();
      const api = {request: async <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> => {
        assert();
        const linked = signal ? linkNativeAbortSignals([ticket.signal, signal]) : undefined;
        try {
          const result = await context.api.request<T>(path, method, body, linked?.signal ?? ticket.signal);
          assert(); return result;
        } finally {linked?.dispose();}
      }};
      const requestId = async () => {
        assert();
        const id = await common.requestId();
        assert(); return id;
      };
      service = new PersonalSavedService(api, lifetime.scope, requestId);
      await service.select({view: 'resource', resourceId: resource.id});
      assert();
      const loaded = service.getSnapshot().resource;
      if (!loaded || loaded.id !== resource.id || loaded.serverId !== resource.serverId || loaded.viewerFence !== resource.viewerFence
        || loaded.kind !== resource.kind || !loaded.actions.includes(action)
        || (action === 'pin' ? loaded.pinRevision !== resource.pinRevision : loaded.revision !== resource.revision)) throw cancelled();
      const applied = await command(service);
      assert();
      if (applied !== true) {
        const failure = service.getSnapshot().mutationError;
        throw Object.assign(new Error(failure?.message ?? 'saved_command_refused'), {code: failure?.code ?? 'saved_command_refused'});
      }
    } finally {
      service?.dispose();
      for (const off of offs) off();
      ticket.abort();
      if (active === ticket) active = undefined;
    }
  };

  return Object.freeze({
    pin: (resource, lease, pinned) => run(resource, lease, 'pin', service => service.mutate({action: 'pin', pinned})),
    rename: (resource, lease, name, summary) => run(resource, lease, 'update', service => service.mutate({action: 'update', name, summary})),
    remove: (resource, lease) => run(resource, lease, 'delete', service => service.mutate({action: 'delete'})),
    dispose: () => {disposed = true; active?.abort(); active = undefined;},
  });
}
