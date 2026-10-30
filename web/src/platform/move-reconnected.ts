/** Servers this browser has already reconnected to after a move to Portico Accounts. Once a
 * person has used their old sign-in here, this browser stops offering it for that server. Not
 * a credential: only server ids, bounded, and any storage failure reads as "not yet". */
const key = 'portico.move-reconnected.v1', limit = 32;
type Store = Pick<Storage, 'getItem' | 'setItem'>;
const store = (): Store | undefined => {try {return globalThis.localStorage;} catch {return undefined;}};
function read(storage: Store | undefined): string[] {
  try {
    const value: unknown = JSON.parse(storage?.getItem(key) ?? '[]');
    return Array.isArray(value) ? value.filter((id): id is string => typeof id === 'string' && id.length > 0 && id.length <= 256).slice(-limit) : [];
  } catch {return [];}
}
export function moveReconnected(serverId: string, storage: Store | undefined = store()): boolean {return read(storage).includes(serverId);}
export function rememberMoveReconnected(serverId: string, storage: Store | undefined = store()): void {
  if (!serverId || serverId.length > 256) return;
  const ids = read(storage).filter(id => id !== serverId);
  try {storage?.setItem(key, JSON.stringify([...ids, serverId].slice(-limit)));} catch {/* Only an offer is affected. */}
}
