import type {ServerPin} from '@core/route-identity.ts';

export type RememberedLocalAccount = Readonly<{accountId: string; username: string}>;
type LocalStorage = Pick<Storage, 'getItem' | 'setItem'>;
const limit = 10;

/** Hints belong to one verified local server identity, including its signing key. */
export function rememberedLocalKey(pin: ServerPin): string {
  return `portico.local-usernames.v1:${JSON.stringify([pin.serverId, pin.publicKey, pin.fingerprint])}`;
}
function account(value: unknown): RememberedLocalAccount | undefined {
  if (!value || typeof value !== 'object') return;
  const row = value as Record<string, unknown>;
  if (typeof row.accountId !== 'string' || !row.accountId || row.accountId.length > 256
    || typeof row.username !== 'string' || !row.username.trim() || row.username.length > 256) return;
  // Whitelist the hint fields; credentials or other stored properties are never retained.
  return {accountId: row.accountId, username: row.username};
}
export function readRememberedLocalAccounts(storage: LocalStorage | undefined, key: string): readonly RememberedLocalAccount[] {
  try {
    const raw: unknown = JSON.parse(storage?.getItem(key) ?? '[]');
    if (!Array.isArray(raw)) return [];
    const rows: RememberedLocalAccount[] = [];
    for (const value of raw.slice(0, limit)) {
      const next = account(value);
      if (next && !rows.some(row => row.accountId === next.accountId || row.username === next.username)) rows.push(next);
    }
    return rows;
  } catch { return []; } // A damaged or denied optional hint store must not block sign-in.
}
export function rememberLocalAccount(storage: LocalStorage | undefined, key: string, value: RememberedLocalAccount): boolean {
  const next = account(value);
  if (!storage || !next) return false;
  const rows = readRememberedLocalAccounts(storage, key).filter(row => row.accountId !== next.accountId && row.username !== next.username);
  try { storage.setItem(key, JSON.stringify([next, ...rows].slice(0, limit)));return true; }
  catch { return false; } // Private browsing/quota refusal leaves ordinary sign-in usable.
}
export function localAccountStorage(): LocalStorage | undefined {
  try { return window.localStorage; } catch { return undefined; }
}
