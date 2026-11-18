/**
 * A page load follows later sign-ins to the profile it already opened. Across a
 * reload the tab can reopen a non-PIN profile it had open. The protected sign-in
 * record holds the device-wide remember choice, including any server PIN grant.
 */
const admitted = new Map<string, string>();
const TAB_KEY = 'portico.tab-profiles.v1';
function readTab(): Record<string, unknown> {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(TAB_KEY) ?? '{}');
    return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
  } catch {return {};}
}
function writeTab(serverId: string, profileId: string | undefined) {
  try {
    const all = readTab();
    if (profileId) all[serverId] = profileId; else delete all[serverId];
    sessionStorage.setItem(TAB_KEY, JSON.stringify(all));
  } catch { /* Storage can be refused: the picker shows at the next reload. */ }
}

type Held = Readonly<{serverId: string; profileId: string}>;

/** True when this page load has already opened the held profile. */
export function tabAdmits(held: Held): boolean {return admitted.get(held.serverId) === held.profileId;}

/** This page load has opened the profile on the server. */
export function admitProfile(serverId: string, profileId: string): void {admitted.set(serverId, profileId); writeTab(serverId, profileId);}

/** The caller opens this profile again after a reload when it has no PIN. */
export function tabHadOpen(held: Held): boolean {return readTab()[held.serverId] === held.profileId;}

/** The profile this page load last opened on the server, for the picker's focus. */
export function admittedProfile(serverId: string): string | undefined {return admitted.get(serverId);}

/** Sign-out, forgetting a server, or a PIN asked for again. */
export function forgetTabServer(serverId: string): void {admitted.delete(serverId); writeTab(serverId, undefined);}

/** Tests only. */
export function resetTabSessionGate(): void {admitted.clear();}
