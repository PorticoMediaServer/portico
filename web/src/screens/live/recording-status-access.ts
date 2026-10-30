/** Tuner allocations are an owner administration read, even when presented in
 * Recordings. Storage access alone never grants access to other profiles' use. */
export function canReadRecordingTuners(identity: Readonly<{
  viewer: Readonly<{role: string; serverId: string}>;
  primaryProfile?: boolean;
  capabilities?: Readonly<{manageServer?: boolean}>;
}> | undefined, serverId: string): boolean {
  return !!identity && identity.viewer.serverId === serverId && identity.viewer.role === 'owner' &&
    identity.primaryProfile === true && identity.capabilities?.manageServer === true;
}
