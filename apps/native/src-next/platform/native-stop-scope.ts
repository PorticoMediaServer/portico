import type {LocalSession} from '../../../../packages/client-core/src/index';

/** The sign-in the system audio player's own stop belongs to (see `user-mutations.ts`). */
export type NativeStopScope = Readonly<{serverId: string; origin: string; authority: string; accountId: string; profileId: string; familyId: string; deviceId?: string}>;
export const sameNativeStopScope = (a: NativeStopScope, b: NativeStopScope) =>
  (['serverId', 'origin', 'authority', 'accountId', 'profileId', 'familyId', 'deviceId'] as const).every(key => a[key] === b[key]);
export function nativeStopScope(origin: string, session: LocalSession | undefined): NativeStopScope | undefined {
  return session && {serverId: session.viewer.serverId, origin, authority: session.viewer.authority, accountId: session.viewer.accountId,
    profileId: session.viewer.profileId, familyId: session.sessionFamilyId, ...(session.deviceId ? {deviceId: session.deviceId} : {})};
}
