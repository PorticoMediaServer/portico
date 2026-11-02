import {serverNow} from '@core/server-clock.ts';
import {parseRememberMarker, rememberedFor, type RememberMarker} from '@core/remember-marker.ts';

/**
 * Which profile this browser may open without asking (C-DESIGN §2, the web half).
 *
 * - "Remember this profile on this device" (auth.html section 8, DECISIONS 2 Nov) is a marker in
 *   localStorage, so it lasts across tabs and restarts of this browser. It counts only for the
 *   exact sign-in that remembered it (server, family and profile), until it expires, and is cleared
 *   from the avatar menu, by signing out or by forgetting the server.
 * - A page load that has opened a profile on a server follows later sign-ins to that same profile
 *   (a renewal, or another tab choosing it again): that tab is never signed out or asked again until
 *   its next reload. Another tab choosing a different profile sends this tab to Who's watching,
 *   so a tab never turns into someone else's profile without its picker and PIN.
 *
 * The marker is not a secret; the server enforces PINs (profile_pin_reentry, profile_pin_manage).
 */
const KEY = 'portico.remembered-profiles.v1';
/** Server → the profile this page load opened there. */
const admitted = new Map<string, string>();

type Held = Readonly<{serverId: string; familyId: string; profileId: string}>;

function readAll(): Record<string, unknown> {
  try {
    const raw = localStorage.getItem(KEY);
    const value: unknown = raw ? JSON.parse(raw) : {};
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
  } catch {return {};}
}
function writeAll(next: Record<string, unknown>) {
  try {
    if (Object.keys(next).length) localStorage.setItem(KEY, JSON.stringify(next));
    else localStorage.removeItem(KEY);
  } catch { /* Private storage can be refused: the picker shows at the next reload. */ }
}

/** True when this tab may open `held` with no picker or PIN. */
export function tabAdmits(held: Held, wallNowMs = serverNow()): boolean {
  if (admitted.get(held.serverId) === held.profileId) return true;
  // An unreadable marker is simply not a marker (R-23): never thrown, never erased here.
  return rememberedFor(parseRememberMarker(readAll()[held.serverId]), held, wallNowMs);
}

/** This page load has opened `profileId` on `serverId` (a pick, or a launch that passed the gate). */
export function admitProfile(serverId: string, profileId: string): void {admitted.set(serverId, profileId);}

/** The profile this page load last opened on `serverId`, for the picker's focus. */
export function admittedProfile(serverId: string): string | undefined {return admitted.get(serverId);}

/** True when this browser remembers `profileId` on `serverId` (the avatar menu then offers Forget remembered profile). */
export function deviceRemembers(serverId: string, profileId: string, wallNowMs = Date.now()): boolean {
  const marker = parseRememberMarker(readAll()[serverId]);
  return !!marker && marker.serverId === serverId && marker.profileId === profileId && Date.parse(marker.expiresAt) > wallNowMs;
}

/** Forget remembered profile: the next visit shows the picker; the profile open now stays open. */
export function forgetRememberedProfile(serverId: string): void {
  const all = readAll();
  if (serverId in all) {delete all[serverId]; writeAll(all);}
}

/** A profile chosen here: remembered on this device when the server said so, otherwise not remembered. */
export function recordTabPick(serverId: string, profileId: string, remembered: RememberMarker | undefined): void {
  admitted.set(serverId, profileId);
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
