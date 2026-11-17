import type {Room as WireRoom, GroupSummaryPage as WirePage} from '../../contracts/src/next/api.ts';
import {unreadableServerResponse} from './server-messages.ts';

export type Room = Readonly<Omit<WireRoom, 'members' | 'queue'>> & Readonly<{
  members: readonly Readonly<WireRoom['members'][number]>[];
  queue: readonly Readonly<WireRoom['queue'][number]>[];
}>;
export type GroupSummaryPage = Readonly<Omit<WirePage, 'items'>> & {readonly items: readonly Readonly<WirePage['items'][number]>[]};
export type GroupControl = 'host' | 'everyone';
export type GroupAction = 'play' | 'pause' | 'seek' | 'watch' | 'goAhead';
export type GroupReportState = 'ready' | 'playing' | 'paused' | 'buffering';
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown): v is string => typeof v === 'string';
const number = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) >= 0;
const stamp = (v: unknown) => text(v) && Number.isFinite(Date.parse(v));
const optional = (v: unknown, check: (v: unknown) => boolean) => v === undefined || check(v);
function requireValue(ok: unknown): asserts ok {if (!ok) throw new Error(unreadableServerResponse);}

export function parseGroupSummaryPage(raw: unknown): GroupSummaryPage {
  requireValue(object(raw) && Array.isArray(raw.items) && optional(raw.nextCursor, text));
  const items = raw.items.map(row => {
    requireValue(object(row) && text(row.id) && !!row.id && text(row.name) && number(row.members) && typeof row.host === 'boolean');
    return Object.freeze({id: row.id, name: row.name, members: row.members, host: row.host});
  });
  return Object.freeze({items: Object.freeze(items), ...(raw.nextCursor ? {nextCursor: raw.nextCursor as string} : {})});
}

/** Current Room only. Redacted entries and private members lose metadata even if an
 * upstream accidentally includes it; clients never fetch a title behind a placeholder. */
export function parseRoom(raw: unknown): Room {
  requireValue(object(raw) && text(raw.id) && !!raw.id && text(raw.name) && ['host', 'everyone'].includes(String(raw.control)));
  const you = raw.you, clock = raw.clock, host = raw.host;
  requireValue(object(you) && text(you.memberId) && typeof you.host === 'boolean' && typeof you.mayControl === 'boolean' && typeof you.mayBecomeHost === 'boolean');
  requireValue(object(clock) && optional(clock.entryId, text) && number(clock.positionMs) && stamp(clock.at) && typeof clock.playing === 'boolean' && optional(clock.waitUntil, stamp) && number(clock.ready) && number(clock.waitingFor));
  requireValue(object(host) && typeof host.present === 'boolean' && [host.goneSince, host.pausesAt, host.endsAt].every(v => optional(v, stamp)));
  requireValue(number(raw.inStepMs) && number(raw.jumpMs) && raw.inStepMs <= raw.jumpMs && number(raw.reportEveryMs) && raw.reportEveryMs > 0 && optional(raw.cantSee, number));
  requireValue(Array.isArray(raw.members) && Array.isArray(raw.queue));
  const members = raw.members.map(member => {
    requireValue(object(member) && text(member.id) && typeof member.host === 'boolean' && typeof member.you === 'boolean' && optional(member.private, v => typeof v === 'boolean') && text(member.status));
    requireValue(optional(member.username, text) && optional(member.name, text));
    return Object.freeze({id: member.id, host: member.host, you: member.you, status: member.status, ...(member.private ? {private: true} : {username: member.username as string | undefined, name: member.name as string | undefined})});
  });
  const ids = new Set<string>();
  const queue = raw.queue.map(entry => {
    requireValue(object(entry) && text(entry.entryId) && !!entry.entryId && !ids.has(entry.entryId) && typeof entry.available === 'boolean');
    ids.add(entry.entryId);
    if (!entry.available) return Object.freeze({entryId: entry.entryId, available: false});
    requireValue(text(entry.itemId) && !!entry.itemId && optional(entry.title, text) && optional(entry.kind, text));
    return Object.freeze({entryId: entry.entryId, available: true, itemId: entry.itemId, title: entry.title as string | undefined, kind: entry.kind as string | undefined});
  });
  const invitation = raw.invitation, ended = raw.ended;
  requireValue(invitation === undefined || object(invitation) && text(invitation.code) && stamp(invitation.expiresAt) && number(invitation.joinsLeft));
  requireValue(ended === undefined || object(ended) && text(ended.reason) && stamp(ended.at));
  return Object.freeze({id: raw.id, name: raw.name, control: raw.control as string,
    you: Object.freeze({...you}) as WireRoom['you'], clock: Object.freeze({...clock}) as WireRoom['clock'], host: Object.freeze({...host}) as WireRoom['host'],
    members: Object.freeze(members), queue: Object.freeze(queue), inStepMs: raw.inStepMs, jumpMs: raw.jumpMs, reportEveryMs: raw.reportEveryMs,
    ...(raw.cantSee === undefined ? {} : {cantSee: raw.cantSee as number}),
    ...(invitation === undefined ? {} : {invitation: Object.freeze({...invitation}) as WireRoom['invitation']}),
    ...(ended === undefined ? {} : {ended: Object.freeze({...ended}) as WireRoom['ended']}),
  });
}

/** A waiting clock is stationary until a fresh Room says everyone may start. */
export function roomPositionMs(room: Room, now: number): number {
  return room.clock.positionMs + (room.clock.playing && !room.clock.waitUntil ? Math.max(0, now - Date.parse(room.clock.at)) : 0);
}
export function roomEntry(room: Room) {return room.queue.find(entry => entry.entryId === room.clock.entryId);}
