import type {ChannelGuide} from '../channel-guide.ts';
import {keepReadable} from '../tolerant.ts';

export type RecordingOwner = {authority: 'local' | 'hosted'; accountId: string; profileId: string};
/** A current profile, with its Recordings switch (Who can record) and that switch's revision. */
export type RecordingOwnerChoice = {owner: RecordingOwner; accountName: string; profileName: string; canRecord: boolean; revision: number};
/** One profile's Recordings switch. There is no account-wide inheritance: each profile is its own answer. */
export type RecordingGrant = {owner: RecordingOwner; enabled: boolean; revision: number};
export type RecordingAnchor = {sourceId: string; channelId: string; generation: string; programmeId: string};
export type RecordingSeries = {key: string; title: string; channelName: string; startsAt: string; seriesId: string; anchor: RecordingAnchor; recordAvailable: boolean; newEvidence: string};
export const recordingOwnerKey = (owner: RecordingOwner) => JSON.stringify([owner.authority, owner.accountId, owner.profileId]);
export const recordingOwnerLabel = (choice: RecordingOwnerChoice) => `${choice.accountName} · ${choice.profileName}`;
/** Whether this exact profile may record. The server lists only the profiles that may. */
export function recordingAllowed(owner: RecordingOwner, grants: readonly RecordingGrant[]): boolean {
  return grants.some(g => g.enabled && recordingOwnerKey(g.owner) === recordingOwnerKey(owner));
}
export function recordingSeries(guide: ChannelGuide, includeTitleMatches = false): RecordingSeries[] {
  return guide.channels.flatMap(c => c.provenance !== 'live-source' ? [] : c.programmes.filter(p => p.seriesId || includeTitleMatches && p.title.trim()).map(p => ({
    key: JSON.stringify([c.sourceId, c.id, c.generation, p.id]), title: p.title, channelName: c.name, startsAt: p.start, seriesId: p.seriesId,
    anchor: {sourceId: c.sourceId, channelId: c.id, generation: c.generation, programmeId: p.id}, recordAvailable: c.recordAvailable, newEvidence: p.newEvidence,
  })));
}
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
function owner(v: unknown): RecordingOwner {
  if (!object(v) || !['local', 'hosted'].includes(String(v.authority)) || typeof v.accountId !== 'string' || !v.accountId || typeof v.profileId !== 'string' || !v.profileId) throw new Error('Recording profiles could not be read. Refresh and try again.');
  return {authority: v.authority as RecordingOwner['authority'], accountId: v.accountId, profileId: v.profileId};
}
export function parseRecordingOwners(raw: unknown): {items: RecordingOwnerChoice[]; nextCursor: string} {
  if (!object(raw) || !Array.isArray(raw.items) || raw.nextCursor !== undefined && typeof raw.nextCursor !== 'string') throw new Error('Recording profiles could not be read. Refresh and try again.');
  // A profile whose names cannot be read is left out alone; one whose identity cannot be read still refuses the list.
  const rows = keepReadable('recording-policy', 'owners', raw.items, v => (object(v) && typeof v.accountName === 'string' && typeof v.profileName === 'string' && typeof v.canRecord === 'boolean' && Number.isSafeInteger(v.revision) && Number(v.revision) >= 1 ? v : undefined));
  return {items: rows.map(v => ({owner: owner(v.owner), accountName: v.accountName as string, profileName: v.profileName as string, canRecord: v.canRecord as boolean, revision: Number(v.revision)})), nextCursor: String(raw.nextCursor ?? '')};
}
export function parseRecordingGrants(raw: unknown): RecordingGrant[] {
  if (!Array.isArray(raw)) throw new Error('Recording permissions could not be read. Refresh and try again.');
  return raw.map(v => {
    if (!object(v) || typeof v.enabled !== 'boolean' || !Number.isSafeInteger(v.revision) || Number(v.revision) < 1) throw new Error('Recording permissions could not be read. Refresh and try again.');
    return {owner: owner(v.owner), enabled: v.enabled, revision: Number(v.revision)};
  });
}

/** Use whole-second UTC boundaries, matching the guide response contract. */
export function recordingGuideRoute(day: string, sourceId: string): import('../channel-guide.ts').GuideRoute | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return null;
  const start = new Date(day + 'T00:00:00Z');
  if (!Number.isFinite(start.getTime()) || start.toISOString().slice(0, 10) !== day) return null;
  return {kind: 'live-source', start: start.toISOString().replace('.000Z', 'Z'), end: new Date(start.getTime() + 86400000).toISOString().replace('.000Z', 'Z'), timezone: 'UTC', search: '', sourceId};
}

type RuleDraft = {id?: string; revision?: number; owner: RecordingOwner; anchor?: RecordingAnchor; kind: string; name: string; match: string; sourceId: string; enabled: boolean; options: object};
export function recordingRulePayload(draft: RuleDraft, grants: readonly RecordingGrant[], operationId: string) {
  owner(draft.owner);
  if (!recordingAllowed(draft.owner, grants)) throw new Error('Allow this profile to record before saving its rule.');
  if (!['series', 'title'].includes(draft.kind) || !draft.name.trim() || !draft.match || !draft.sourceId) throw new Error('Choose a series from the guide.');
  if (!draft.id && (!draft.anchor?.programmeId || !draft.anchor.channelId || !draft.anchor.generation || draft.anchor.sourceId !== draft.sourceId)) throw new Error('Choose a program from the current guide.');
  return {expectedRevision: draft.revision ?? 0, operationId, owner: draft.owner, ...(draft.anchor ? {anchor: draft.anchor} : {}), kind: draft.kind, name: draft.name.trim(), match: draft.match, sourceId: draft.sourceId, enabled: draft.enabled, options: draft.options};
}
