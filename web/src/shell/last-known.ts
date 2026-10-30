/** The server name a launch may still show when the server stops answering.
 *
 * Scoped to one retained viewer family: the key is the opaque session family, and
 * the record is written only after that family's own assembly succeeded. A signed
 * out browser, another profile or a replaced family has no key to read, and every
 * sign-out or family end clears it. The name only — no library names, ids, kinds,
 * counts, items, credentials or tokens. A record written by an older build may
 * still carry a library array; it is read for the name and the array is discarded
 * before it can reach a surface. A hostile stored value is bounded before parsing. */

export type LastKnownShell = Readonly<{serverName: string}>;

const PREFIX = 'portico.shell.lastKnown.v2:';
const MAX_RAW = 16 * 1024;
const MAX_TEXT = 120;

function storage(): Storage | undefined {
  try { return globalThis.localStorage; } catch { return undefined; }
}

function text(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= MAX_TEXT ? value : undefined;
}

function familyKey(family: unknown): string | undefined {
  return text(family);
}

/** Remembers this family's last assembled server name. */
export function rememberShell(family: string, serverName: string): void {
  const key = familyKey(family), name = text(serverName), store = storage();
  if (!key || !name || !store) return;
  try { store.setItem(PREFIX + key, JSON.stringify({serverName: name})); } catch { /* private or full storage */ }
}

/** The last assembled shell for this family, or undefined when none was stored. */
export function lastKnownShell(family: string): LastKnownShell | undefined {
  const key = familyKey(family), store = storage();
  if (!key || !store) return undefined;
  let raw: string | null;
  try { raw = store.getItem(PREFIX + key); } catch { return undefined; }
  // Bound the value before parsing: a hostile record must not cost unbounded work.
  if (typeof raw !== 'string' || raw.length === 0 || raw.length > MAX_RAW) return undefined;
  let parsed: unknown;
  try { parsed = JSON.parse(raw); } catch { return undefined; }
  if (!parsed || typeof parsed !== 'object') return undefined;
  // A legacy record's library array is deliberately ignored: only the name survives.
  const {serverName} = parsed as {serverName?: unknown};
  const name = text(serverName);
  return name ? {serverName: name} : undefined;
}

/** Drops this family's record (sign-out, family end or profile switch). */
export function forgetShell(family: string): void {
  const key = familyKey(family), store = storage();
  if (!key || !store) return;
  try { store.removeItem(PREFIX + key); } catch { /* nothing to clear */ }
}

/** Drops every recorded family (a sign-out of the selected server). */
export function forgetAllShells(): void {
  const store = storage();
  if (!store) return;
  try {
    const keys: string[] = [];
    for (let index = 0; index < store.length; ++index) {
      const key = store.key(index);
      if (key && key.startsWith(PREFIX)) keys.push(key);
    }
    for (const key of keys) store.removeItem(key);
  } catch { /* nothing to clear */ }
}
