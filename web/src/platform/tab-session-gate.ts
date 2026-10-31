import {parseRememberMarker, rememberedFor, type RememberMarker} from '@core/remember-marker.ts';

/**
 * Which profile this browser tab may open without asking (C-DESIGN §2, the web half).
 *
 * - "Remember this profile in this browser tab" (auth.html) is a marker in sessionStorage, so it
 *   lasts for this tab, reloads included, and never reaches another tab. It counts only for the
 *   exact sign-in that remembered it (server, family and profile), until it expires.
 * - A page load that has passed the gate for a server follows later sign-ins on that server (another
 *   tab picking a profile, a renewal, a switch made here): that tab is never signed out or asked
 *   again until its next reload.
 *
 * The marker is not a secret; the server enforces PINs (profile_pin_reentry, profile_pin_manage).
 */
const KEY = 'portico.remembered-profiles.v1';
const admitted = new Set<string>();

type Held = Readonly<{serverId: string; familyId: string; profileId: string}>;

function readAll(): Record<string, unknown> {
  try {
    const raw = sessionStorage.getItem(KEY);
    const value: unknown = raw ? JSON.parse(raw) : {};
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
  } catch {return {};}
}
function writeAll(next: Record<string, unknown>) {
  try {
    if (Object.keys(next).length) sessionStorage.setItem(KEY, JSON.stringify(next));
    else sessionStorage.removeItem(KEY);
  } catch { /* Private storage can be refused: the picker shows at the next reload. */ }
}

/** True when this tab may open `held` with no picker or PIN. */
export function tabAdmits(held: Held, wallNowMs = Date.now()): boolean {
  if (admitted.has(held.serverId)) return true;
  // An unreadable marker is simply not a marker (R-23): never thrown, never erased here.
  return rememberedFor(parseRememberMarker(readAll()[held.serverId]), held, wallNowMs);
}

/** This page load has opened a profile on `serverId` (a pick, or a launch that passed the gate). */
export function admitServer(serverId: string): void {admitted.add(serverId);}

/** A profile chosen here: remembered for this tab when the server said so, otherwise not remembered. */
export function recordTabPick(serverId: string, remembered: RememberMarker | undefined): void {
  admitted.add(serverId);
  const all = readAll();
  if (remembered) all[serverId] = {...remembered};
  else delete all[serverId];
  writeAll(all);
}

/** Sign-out, forgetting a server, or a PIN asked for again: this tab no longer opens it unasked. */
export function forgetTabServer(serverId: string): void {
  admitted.delete(serverId);
  const all = readAll();
  if (serverId in all) {delete all[serverId]; writeAll(all);}
}

/** Tests only. */
export function resetTabSessionGate(): void {admitted.clear();}
