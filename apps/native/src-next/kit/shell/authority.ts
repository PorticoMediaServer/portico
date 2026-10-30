export type ServerRole = 'owner' | 'administrator' | 'member';
export type ServerAudience = 'owner' | 'administrators';

/** Presentation gating only. The caller still applies real server authorization to every read. */
export function canSeeServerPage(role: ServerRole, audience: ServerAudience): boolean {
  return role === 'owner' || (role === 'administrator' && audience === 'administrators');
}
export function serverVisibilityKey(role: ServerRole, audience: ServerAudience) {
  if (!canSeeServerPage(role, audience)) return undefined;
  return role === 'administrator' ? 'next.shell.serverVisibility.administrator' as const
    : audience === 'administrators' ? 'next.shell.serverVisibility.ownerShared' as const
      : 'next.shell.serverVisibility.owner' as const;
}
export function serverRoleKey(role: ServerRole) {
  return role === 'owner' ? 'next.shell.owner' as const : role === 'administrator' ? 'next.shell.administrator' as const : 'next.shell.member' as const;
}
