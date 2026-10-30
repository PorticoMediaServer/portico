import type {HttpLocalApi} from '../../../../packages/client-core/src';
import type {AppServerSession, SignInKind} from '../../../../packages/client-core/src/app-sessions';
import {parseServerSignIn} from '../../../../packages/client-core/src/sign-in';
import {negotiateNativeServer} from './negotiation';
import {routePath} from '../../../../packages/contracts/src/routes';
import {NativeQueueStore} from './queue-storage';

type ServerDisplay = Readonly<{serverId: string; fingerprint: string; name: string}>;
const key = 'accounts.server-names.v1';
const invalid = () => Object.assign(new Error('native_server_name_unavailable'), {code: 'invalid_saved_connection'});
const cancelled = () => Object.assign(new Error('native_server_name_cancelled'), {name: 'AbortError'});
const text = (value: unknown, max: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= max && !/[\p{Cc}\p{Cf}]/u.test(value);
function parse(raw: unknown): readonly ServerDisplay[] {
  if (raw == null) return [];
  if (!raw || typeof raw !== 'object' || !('version' in raw) || raw.version !== 1 || !('servers' in raw) || !Array.isArray(raw.servers) || raw.servers.length > 1024) throw invalid();
  const seen = new Set<string>();
  return raw.servers.map((value: unknown) => {
    if (!value || typeof value !== 'object' || !('serverId' in value) || !text(value.serverId, 256) ||
        !('fingerprint' in value) || typeof value.fingerprint !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(value.fingerprint) ||
        !('name' in value) || !text(value.name, 120) || seen.has(value.serverId)) throw invalid();
    seen.add(value.serverId);
    return Object.freeze({serverId: value.serverId, fingerprint: value.fingerprint, name: value.name});
  });
}

/** Display data only. It neither selects a session nor supplies any authority. A name is
 * learned from the server over its verified route, never synthesized from an address. */
/** `setup` reads a server nobody has set up yet (first-run setup) and requires exactly that;
 * every other caller is signing in, and a server still awaiting setup is refused for it. */
export async function rememberNativeServerDisplay(api: HttpLocalApi, options: {
  serverId: string; signIn?: SignInKind; signal?: AbortSignal; isCurrent: () => boolean; setup?: boolean;
}) {
  const connection = api.getRouteConnection();
  const current = () => options.isCurrent() && !options.signal?.aborted && api.getRouteConnection() === connection;
  if (!current()) throw cancelled();
  if (!connection || connection.pin.serverId !== options.serverId || connection.getSnapshot().phase !== 'ready') throw invalid();
  const abort = new AbortController(), cancel = () => abort.abort();
  options.signal?.addEventListener('abort', cancel, {once: true});
  const timer = setTimeout(cancel, 15000);
  try {
    const server = parseServerSignIn(await api.request(routePath('get_system'), 'GET', undefined, abort.signal));
    // Every connection that reads the server's facts negotiates with them (O-159).
    negotiateNativeServer(server);
    if (!current() || abort.signal.aborted) throw cancelled();
    if (server.id !== connection.pin.serverId || server.setupRequired !== (options.setup === true) || options.signIn && server.signIn !== options.signIn) throw invalid();
    await new NativeQueueStore().change(key, raw => {
      if (!current()) throw cancelled();
      const servers = parse(raw).filter(value => value.serverId !== server.id);
      if (servers.length >= 1024) throw invalid();
      servers.push(Object.freeze({serverId: server.id, fingerprint: connection.pin.fingerprint, name: server.name}));
      return {record: {version: 1, servers}, value: undefined};
    }, {error: invalid});
    if (!current()) throw cancelled();
    return server;
  } finally {clearTimeout(timer); options.signal?.removeEventListener('abort', cancel);}
}

/** An absent or mismatched name is an incomplete current record, never an address alias. */
export function readNativeServerDisplays(servers: readonly AppServerSession[]) {
  return new NativeQueueStore().change(key, raw => {
    const names = parse(raw);
    return {record: raw, value: servers.map(value => {
      const display = names.find(item => item.serverId === value.connection.pin.serverId && item.fingerprint === value.connection.pin.fingerprint);
      if (!display) throw invalid();
      return display;
    })};
  }, {error: invalid});
}
