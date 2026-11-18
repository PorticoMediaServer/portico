import {serverNow} from './server-clock.ts';

/** The choice belongs to one profile of one saved sign-in. A PIN grant stays with the
 * sign-in credentials. For non-PIN profiles only, the client tracks any owner-set idle limit. */
export type RememberMarker = Readonly<{familyId: string; profileId: string} & (
  {pinToken: string; lastUsedAt?: never; pinRememberDays?: never} |
  {pinToken?: never; lastUsedAt: number; pinRememberDays: number}
)>;

const text = (value: unknown, max: number): value is string =>
  typeof value === 'string' && value.length > 0 && value.length <= max && !/[\p{Cc}\p{Cf}]/u.test(value);

export function parseRememberMarker(raw: unknown): RememberMarker | undefined {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return undefined;
  const {familyId, profileId, pinToken, lastUsedAt, pinRememberDays} = raw as Record<string, unknown>;
  if (!text(familyId, 128) || !text(profileId, 256)) return undefined;
  if (pinToken !== undefined) return text(pinToken, 16384) ? Object.freeze({familyId, profileId, pinToken}) : undefined;
  if (typeof lastUsedAt !== 'number' || !Number.isSafeInteger(lastUsedAt) || lastUsedAt <= 0 ||
      typeof pinRememberDays !== 'number' || !Number.isSafeInteger(pinRememberDays) || pinRememberDays === 0 || pinRememberDays < -1) return undefined;
  return Object.freeze({familyId, profileId, lastUsedAt, pinRememberDays});
}

/** Timed choices count from successful use. The default has no client-side deadline. */
export function rememberedFor(marker: RememberMarker | undefined, held: Readonly<{familyId: string; profileId: string}>,
  days = marker?.pinRememberDays ?? -1, now = serverNow()): marker is RememberMarker {
  return !!marker && marker.familyId === held.familyId && marker.profileId === held.profileId && days !== 0
    && (marker.pinToken !== undefined || days === -1 || now < marker.lastUsedAt + days * 86_400_000);
}
