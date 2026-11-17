import {defaultI18n, type MessageId} from '@i18n';
import type {Room} from '@core/room-protocol.ts';

type Member = Room['members'][number];
export function togetherStateKey(group: Room): MessageId {
  if (group.ended) return 'web.together.state.ended';
  if (!group.host.present) return 'web.together.state.hostReconnecting';
  if (group.clock.waitUntil) return 'web.together.state.waiting';
  if (group.clock.playing) return 'web.together.state.playing';
  return group.clock.entryId ? 'next.dock.paused' : 'web.together.state.lobby';
}
export type TogetherHostNotice = Readonly<{key: MessageId; seconds: number; takeOver: boolean; canClaim: boolean}>;
export function togetherHostNotice(group: Room, nowMs: number): TogetherHostNotice | undefined {
  if (group.host.present) return undefined;
  const pauseAt = Date.parse(group.host.pausesAt ?? '');
  const reconnecting = Number.isFinite(pauseAt) && nowMs < pauseAt;
  const at = Date.parse((reconnecting ? group.host.pausesAt : group.host.endsAt) ?? '');
  if (!Number.isFinite(at)) return undefined;
  const canClaim = group.you.mayBecomeHost;
  return {key: reconnecting ? 'web.together.pausesIn' : 'web.together.endsIn', seconds: Math.max(0, (at - nowMs) / 1000), takeOver: canClaim, canClaim};
}
export function togetherCanMakeHost(group: Room, member: Member): boolean {
  return group.you.host && !member.you && member.status === 'in_step';
}
export function togetherPlayLabel(group: Room): MessageId {
  if (group.clock.waitUntil) return 'web.together.playAnyway';
  return group.clock.playing ? 'web.together.pauseAll' : 'web.together.playAll';
}
export function togetherInviteVisible(group: Room): boolean {return group.you.host;}
export const memberName = (member: Member, privateName = defaultI18n.t('together.privateViewer')): string =>
  member.private ? privateName : member.username || member.name || '';
export const memberAlias = (member: Member): string | undefined =>
  !member.private && member.username && member.name?.trim() && member.name !== member.username ? member.name : undefined;
export function memberStatusKey(member: Member): MessageId {
  return member.status === 'in_step' ? 'web.together.inStep' : member.status === 'buffering' ? 'web.together.loading'
    : member.status === 'catching_up' ? 'web.together.catchingUp' : 'web.together.away';
}
