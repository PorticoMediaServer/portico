/**
 * "Remember this profile": the device opens this profile at launch, with no picker or PIN, while it
 * still holds the sign-in that remembered it. The marker is not a secret. The server decides what
 * a sign-in may do; this only says which launch the person chose.
 *
 * Native keeps one marker per server inside the protected session record. Web keeps it in
 * sessionStorage, so it lasts for this browser tab, as drawn ("Remember this profile in this
 * browser tab").
 */
export type RememberMarker = Readonly<{serverId: string; familyId: string; profileId: string; expiresAt: string}>;

export interface RememberStore {
  read(serverId: string): Promise<RememberMarker | undefined>;
  write(marker: RememberMarker): Promise<void>;
  clear(serverId: string): Promise<void>;
}

const text = (value: unknown, max = 256): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= max && !/[\p{Cc}\p{Cf}]/u.test(value);

/** Reads a saved marker. Fields another version added are set aside; anything unreadable is
 *  simply not a marker (the picker shows), and is never thrown or erased here (R-23). */
export function parseRememberMarker(raw: unknown): RememberMarker | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined;
  const value = raw as Record<string, unknown>;
  if (!text(value.serverId) || !text(value.familyId, 128) || !text(value.profileId) || !text(value.expiresAt, 64)
    || !Number.isFinite(Date.parse(value.expiresAt))) return undefined;
  return Object.freeze({serverId: value.serverId, familyId: value.familyId, profileId: value.profileId, expiresAt: value.expiresAt});
}

/** The marker counts only for the exact sign-in that remembered it, and only until it expires.
 *  A pick by another tab or a successor family makes it stale, never wrong. */
export function rememberedFor(marker: RememberMarker | undefined, held: Readonly<{serverId: string; familyId: string; profileId: string}>,
  wallNowMs: number = Date.now()): boolean {
  return !!marker && marker.serverId === held.serverId && marker.familyId === held.familyId && marker.profileId === held.profileId
    && Date.parse(marker.expiresAt) > wallNowMs;
}
