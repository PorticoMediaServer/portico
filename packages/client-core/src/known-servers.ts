/**
 * Remembered direct servers: every server this device has signed in to directly (by address or
 * on the local network), so a server switcher can list them. It holds no credentials: the active
 * sign-in lives in the protected connection store (`server-connections.ts`), and switching to a
 * listed server signs in again (or restores that server's own protected record).
 *
 * Bounded (most recent first), tolerant of what it reads, and atomic through the storage port's
 * `change`, so two tabs or a fast double sign-in never lose an entry.
 */

export type KnownDirectServer = Readonly<{
  serverId: string;
  name: string;
  /** The address the viewer used: an http(s) origin, no path, no credentials. */
  address: string;
  lastProfileId?: string;
  lastProfileName?: string;
  /** ISO time of the last successful sign-in. */
  lastUsedAt: string;
}>;

export interface KnownServerStorage {
  read(): Promise<unknown>;
  /** Atomic read-modify-write of the stored value. */
  change(update: (current: unknown) => unknown): Promise<void>;
}

export const MAX_KNOWN_SERVERS = 20;

const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_.:-]{1,128}$/.test(v);
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length <= max && !/[\u0000-\u001f\u007f]/.test(v);

/** An http(s) origin, or undefined. Paths, credentials, queries and fragments are refused. */
export function knownServerAddress(value: unknown): string | undefined {
  if (typeof value !== 'string' || value.length > 512) return undefined;
  let url: URL;
  try { url = new URL(value); } catch { return undefined; }
  if ((url.protocol !== 'http:' && url.protocol !== 'https:') || url.username || url.password || url.search || url.hash || (url.pathname !== '/' && url.pathname !== '')) return undefined;
  return url.origin;
}

function parseEntry(v: unknown): KnownDirectServer | undefined {
  if (!v || typeof v !== 'object' || Array.isArray(v)) return undefined;
  const o = v as Record<string, unknown>;
  const address = knownServerAddress(o.address);
  if (!id(o.serverId) || !text(o.name, 120) || !address || typeof o.lastUsedAt !== 'string' || !Number.isFinite(Date.parse(o.lastUsedAt))) return undefined;
  return Object.freeze({
    serverId: o.serverId, name: o.name, address, lastUsedAt: o.lastUsedAt,
    ...(id(o.lastProfileId) ? {lastProfileId: o.lastProfileId} : {}),
    ...(text(o.lastProfileName, 80) && o.lastProfileName ? {lastProfileName: o.lastProfileName} : {}),
  });
}

/** The stored list, newest first, without duplicates or malformed entries. */
export function parseKnownServers(value: unknown): readonly KnownDirectServer[] {
  if (!value || typeof value !== 'object' || (value as {version?: unknown}).version !== '1') return [];
  const items = (value as {items?: unknown}).items;
  if (!Array.isArray(items)) return [];
  const seen = new Set<string>();
  const out: KnownDirectServer[] = [];
  for (const raw of items) {
    const entry = parseEntry(raw);
    if (!entry || seen.has(entry.serverId)) continue;
    seen.add(entry.serverId);
    out.push(entry);
  }
  out.sort((a, b) => Date.parse(b.lastUsedAt) - Date.parse(a.lastUsedAt));
  return Object.freeze(out.slice(0, MAX_KNOWN_SERVERS));
}

export class KnownDirectServers {
  private storage: KnownServerStorage;
  private now: () => number;
  constructor(storage: KnownServerStorage, now: () => number = Date.now) {
    this.storage = storage;
    this.now = now;
  }

  async list(): Promise<readonly KnownDirectServer[]> {
    return parseKnownServers(await this.storage.read());
  }

  /** Record a successful direct sign-in (moves the server to the top). */
  async remember(server: Readonly<{serverId: string; name: string; address: string; lastProfileId?: string; lastProfileName?: string}>): Promise<void> {
    const entry = parseEntry({...server, lastUsedAt: new Date(this.now()).toISOString()});
    if (!entry) throw new Error('A remembered server needs an id, a name and an http(s) address.');
    await this.storage.change(current => ({version: '1', items: [entry, ...parseKnownServers(current).filter(s => s.serverId !== entry.serverId)].slice(0, MAX_KNOWN_SERVERS)}));
  }

  /** Update the last profile used on a server (profile switch) without reordering. */
  async setProfile(serverId: string, profile: Readonly<{id: string; name: string}> | undefined): Promise<void> {
    await this.storage.change(current => ({version: '1', items: parseKnownServers(current).map(s => {
      if (s.serverId !== serverId) return s;
      const {lastProfileId: _id, lastProfileName: _name, ...rest} = s;
      return profile ? {...rest, lastProfileId: profile.id, lastProfileName: profile.name} : rest;
    })}));
  }

  /**
   * The address the server was last proven at (its identity was verified there). A server that
   * moved keeps working for the sign-in in use, because that follows the verified route; this
   * keeps the remembered entry, which a later switch back starts from, pointing at the same
   * place. Order and profile are untouched; an address that is not a plain http(s) origin is ignored.
   */
  async setAddress(serverId: string, address: string): Promise<void> {
    const verified = knownServerAddress(address);
    if (!verified) return;
    await this.storage.change(current => {
      const items = parseKnownServers(current);
      // Nothing to write when it is unknown or unchanged: no storage churn on every reconnect.
      return items.some(s => s.serverId === serverId && s.address !== verified) ? {version: '1', items: items.map(s => (s.serverId === serverId ? {...s, address: verified} : s))} : current;
    });
  }

  async forget(serverId: string): Promise<void> {
    await this.storage.change(current => ({version: '1', items: parseKnownServers(current).filter(s => s.serverId !== serverId)}));
  }
}

/** A storage port over a synchronous key-value store (web `localStorage`, tests). */
export function keyValueKnownServerStorage(store: Readonly<{getItem(key: string): string | null; setItem(key: string, value: string): void}>, key = 'portico.knownServers'): KnownServerStorage {
  const read = () => { try { const raw = store.getItem(key); return raw ? JSON.parse(raw) as unknown : undefined; } catch { return undefined; } };
  return {
    read: async () => read(),
    change: async update => { store.setItem(key, JSON.stringify(update(read()))); },
  };
}
