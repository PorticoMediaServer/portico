import {routePath} from '../../../contracts/src/routes.ts';

export type RefusedReason = 'name' | 'http' | 'certificate' | 'proxy';
export type RefusedName = Readonly<{name: string; reason: RefusedReason; lastSeen: string; count: number}>;
export type RefusedAddressesSnapshot = Readonly<{loading: boolean; names: readonly RefusedName[]; failed: boolean; allowing?: string}>;
export type RefusedAddressesApi = Readonly<{request: <T>(path: string, method: string, body?: unknown, signal?: AbortSignal) => Promise<T>}>;

function parseNames(raw: unknown): readonly RefusedName[] {
  const names = raw && typeof raw === 'object' && Array.isArray((raw as {names?: unknown}).names) ? (raw as {names: unknown[]}).names : [];
  return Object.freeze(names.slice(0, 256).flatMap(item => {
    if (!item || typeof item !== 'object') return [];
    const v = item as Record<string, unknown>;
    if (typeof v.name !== 'string' || v.name.length > 253 || !['name', 'http', 'certificate', 'proxy'].includes(v.reason as string)
      || typeof v.count !== 'number' || !Number.isInteger(v.count) || v.count < 1 || typeof v.lastSeen !== 'string') return [];
    return [Object.freeze({name: v.name, reason: v.reason as RefusedReason, lastSeen: v.lastSeen, count: v.count})];
  }));
}

/** The owner's refused addresses (O-169): names outside clients asked for and
 * were refused, each with the fix its reason names. Owner-only, remote page. */
export class RefusedAddressesSource {
  private snapshot: RefusedAddressesSnapshot = Object.freeze({loading: false, names: Object.freeze([]), failed: false});
  private readonly listeners = new Set<() => void>();
  private generation = 0;
  constructor(private readonly api: RefusedAddressesApi, private readonly isCurrent: () => boolean, private readonly allowed: () => void) {}
  has = (id: string) => id === 'remote.refused';
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.snapshot;
  private publish(next: Partial<RefusedAddressesSnapshot>) {this.snapshot = Object.freeze({...this.snapshot, ...next});for (const listener of this.listeners) listener();}
  load = async () => {
    const generation = ++this.generation;
    this.publish({loading: true, failed: false});
    try {
      const raw = await this.api.request<unknown>(routePath('get_refused_addresses'), 'GET');
      if (generation === this.generation && this.isCurrent()) this.publish({loading: false, names: parseNames(raw)});
    } catch {
      if (generation === this.generation && this.isCurrent()) this.publish({loading: false, failed: true});
    }
  };
  /** Allow this address: adds the name to Custom addresses, then the page's connectivity form reloads. */
  allow = async (name: string) => {
    if (this.snapshot.allowing || !this.isCurrent()) return;
    this.publish({allowing: name, failed: false});
    try {
      // The settings revision the change is made against: read now, so it's current.
      const policy = await this.api.request<{revision?: unknown}>(routePath('get_connectivity_policy'), 'GET');
      const revision = typeof policy?.revision === 'number' ? policy.revision : Number(policy?.revision);
      if (!Number.isInteger(revision) || !this.isCurrent()) throw new Error('No settings revision');
      const raw = await this.api.request<unknown>(routePath('allow_refused_address'), 'POST',
        {name, expectedRevision: revision, operationId: `allow-${revision}-${name}`.slice(0, 120)});
      if (!this.isCurrent()) return;
      this.publish({allowing: undefined, names: parseNames(raw)});
      this.allowed();
    } catch {
      if (this.isCurrent()) this.publish({allowing: undefined, failed: true});
    }
  };
}

