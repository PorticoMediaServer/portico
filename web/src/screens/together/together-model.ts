import type {MessageId} from '@i18n';
import type {Group, GroupMember} from '@core/social-playback.ts';

/** The drawn room state: host reconnecting first, then the real timeline, then the group state. */
export function togetherStateKey(group: Group): MessageId {
  if (group.state.startsWith('host-reconnecting')) return 'web.together.state.hostReconnecting';
  if (group.timeline.state === 'playing') return 'web.together.state.playing';
  switch (group.state) {
    case 'preparing': return 'web.together.state.preparing';
    case 'waiting': return 'web.together.state.waiting';
    case 'ready': return 'web.together.state.ready';
    case 'ending': return 'web.together.state.ending';
    case 'ended': return 'web.together.state.ended';
    case 'paused': return 'next.dock.paused';
    default: return 'web.together.state.lobby';
  }
}
export type TogetherHostNotice = Readonly<{key: MessageId; seconds: number; takeOver: boolean; canClaim: boolean}>;
/** The drawn warning strip: pauses while the host may still return, ends once the wait has passed.
 * The server owns self-claim eligibility; a missing canClaimHost is false. */
export function togetherHostNotice(group: Group, nowMs: number): TogetherHostNotice | undefined {
  if (group.host.presence === 'connected') return undefined;
  const reconnecting = group.host.presence === 'reconnecting';
  const at = Date.parse((reconnecting ? group.host.pauseAt : group.host.endAt) ?? '');
  if (!Number.isFinite(at)) return undefined;
  const canClaim = !reconnecting && !group.permissions.isHost && group.permissions.canClaimHost === true;
  return {key: reconnecting ? 'web.together.pausesIn' : 'web.together.endsIn', seconds: Math.max(0, (at - nowMs) / 1000), takeOver: canClaim, canClaim};
}
/** The frame draws Make host only on someone who is in step. */
export function togetherCanMakeHost(group: Group, member: GroupMember): boolean {
  return group.permissions.isHost && member.id !== group.viewerMemberId && member.state === 'joined'
    && member.presence === 'connected' && member.readiness === 'ready';
}
/** Pause while playing, Play anyway while anyone is still catching up, otherwise Play for everyone. */
export function togetherPlayLabel(group: Group): MessageId {
  if (group.timeline.state === 'playing') return 'web.together.pauseAll';
  return group.readiness.ready < group.readiness.memberCount ? 'web.together.playAnyway' : 'web.together.playAll';
}
/** A member sees the room without the Invite section. */
export function togetherInviteVisible(group: Group): boolean {
  return group.permissions.isHost;
}

type NamedMember = Readonly<{displayName: string; username?: string; private: boolean}>;
/** G38: a member is named by username first; private members keep their masked name. */
export const memberName = (member: NamedMember): string => !member.private && member.username ? member.username : member.displayName;
/** The "Your name in the group" name, shown quieter only when it differs from the username. */
export const memberAlias = (member: NamedMember): string | undefined =>
  !member.private && member.username && member.displayName.trim() && member.displayName !== member.username ? member.displayName : undefined;
