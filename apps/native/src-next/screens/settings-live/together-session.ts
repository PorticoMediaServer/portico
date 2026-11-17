import {RoomSessionService, type RoomSessionSnapshot} from '../../../../../packages/client-core/src/room-session';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {withGuardedRaw} from '../../../../../packages/client-core/src/home-selection/list-destinations';
import {nativeRouteCrypto} from '../../platform/server-connections';
import {NativeScreenLifetime} from './lifetime';

type Owner = Pick<NativeShellBootstrapContext, 'api' | 'authority' | 'current' | 'session' | 'subscribeAuthority' | 'events'>;
const sessions = new WeakMap<Owner, RoomSessionService>();
const closed: RoomSessionSnapshot = Object.freeze({directory: [], directoryPhase: 'idle', phase: 'idle', busy: false, clockOffsetMs: 0});

/** One real room service per restored authority, borrowed by the screen and
 * player follower. Leaving a screen never creates, closes or replaces a room. */
export function nativeTogetherSession(context: Owner): RoomSessionService {
  if (!context.current()) throw Object.assign(new Error('native_together_retired'), {name: 'AbortError'});
  const existing = sessions.get(context);
  if (existing) return existing;
  const lifetime = new NativeScreenLifetime(context);
  const service = new RoomSessionService({
    api: withGuardedRaw({request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) =>
      lifetime.request(next => context.api.request<T>(path, method, body, next), signal)}, context.api, lifetime.assert),
    events: context.events,
    key: () => {lifetime.assert(); return [...nativeRouteCrypto.random(24)].map(byte => byte.toString(16).padStart(2, '0')).join('');},
    i18n: defaultI18n,
  });
  const read = service.getSnapshot;
  service.getSnapshot = () => lifetime.current() ? read() : closed;
  sessions.set(context, service);
  let unsubscribe = () => {};
  const dispose = () => {
    if (sessions.get(context) !== service) return;
    sessions.delete(context); unsubscribe(); service.dispose(); lifetime.dispose();
  };
  unsubscribe = context.subscribeAuthority(() => {if (!lifetime.current()) dispose();});
  return service;
}
