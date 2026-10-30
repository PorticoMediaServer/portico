import {defaultI18n} from '@i18n';
import {randomId} from '@core/random-id.ts';
import type {ShellAssemblyContext} from '../../shell/bootstrap';
import {createMusicMixesController, type MusicMixesController} from './music-mixes';

type Shared = {source: MusicMixesController; leases: number; dispose: () => void};
const owners = new WeakMap<ShellAssemblyContext, Shared>();

/** Both screen bindings lease this same captured source; zero leases erase it.
 * Neither acquiring a lease nor importing this module starts a read. */
export function retainMusicMixes(context: ShellAssemblyContext) {
  let shared = owners.get(context);
  if (!shared) {
    const session = context.session(), viewer = session.viewer, life = new AbortController();
    if (viewer.authority !== 'local' && viewer.authority !== 'hosted') throw new DOMException('Music authority unavailable', 'AbortError');
    const family = session.sessionFamilyId, role = viewer.role;
    const principal = Object.freeze({serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId});
    const current = () => {
      if (life.signal.aborted || !context.isCurrent()) return false;
      const value = context.session();
      return value.sessionFamilyId === family && value.viewer.serverId === principal.serverId && value.viewer.authority === principal.authority
        && value.viewer.accountId === principal.accountId && value.viewer.profileId === principal.profileId && value.viewer.role === role;
    };
    const source = createMusicMixesController({api: context.api, principal, signal: life.signal, isCurrent: current,
      requestId: async () => randomId(), t: defaultI18n.t});
    const off = context.preferences?.subscribe(source.relabel) ?? (() => {});
    shared = {source, leases: 0, dispose: () => {off();life.abort();source.dispose();}};
    owners.set(context, shared);
  }
  const owner = shared;owner.leases++;
  let active = true;
  return Object.freeze({source: owner.source, release() {
    if (!active) return;
    active = false;
    if (--owner.leases === 0) {if (owners.get(context) === owner) owners.delete(context);owner.dispose();}
  }});
}
