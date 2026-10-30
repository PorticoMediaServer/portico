import {routes} from '../../contracts/src/index.ts';

export type ReadingSavedGroupKind = 'series' | 'magazine' | 'newspaper';
export type ReadingSavedGroupTarget = Readonly<{libraryId: string; groupKind: ReadingSavedGroupKind; seriesId: string}>;
export type ReadingSavedGroupMembership = ReadingSavedGroupTarget & Readonly<{listed: boolean; revision: number; referenceId?: string; viewerFence: string; operationId?: string}>;
export type ReadingSavedGroupCommand = Readonly<{operationId: string; expectedRevision: number; listed: boolean}>;
export type ReadingSavedReferenceCommand = Readonly<{operationId: string; expectedRevision: number}>;
export type ReadingSavedReferenceReceipt = Readonly<{referenceId: string; revision: number; removed: true; operationId: string}>;
export type SavedReadingGroupCard = Readonly<{id: string; kind: 'reading_group'; revision: number; removeMyList: boolean} & (
  {hidden: true} | {hidden: false; libraryId: string; readingGroup: Readonly<{groupKind: ReadingSavedGroupKind; seriesId: string}>; title: string; count: number; posterUrl?: string; navigation: Readonly<{view: 'series'; entityId: string; pivot?: 'magazines' | 'newspapers'}>})>;
const record = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value);
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);
const series = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{43}$/.test(value);
const revision = (value: unknown): value is number => typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
const kind = (value: unknown): value is ReadingSavedGroupKind => value === 'series' || value === 'magazine' || value === 'newspaper';
const bad = (): never => {throw new Error('invalid_reading_saved_group');};
export function validReadingSavedGroupTarget(value: ReadingSavedGroupTarget): boolean {return identifier(value.libraryId) && kind(value.groupKind) && series(value.seriesId);}

/** Official descriptors alone supply paths. Until the checkpoint publishes
 * these operations, callers withhold controls instead of inventing routes. */
export function readingSavedGroupPath(method: 'GET' | 'PUT', target: ReadingSavedGroupTarget): string | undefined {
  if (!validReadingSavedGroupTarget(target)) return undefined;
  const operation: string = method === 'GET' ? 'get_reading_group_my_list' : 'put_reading_group_my_list';
  const descriptor = routes.find(route => route.id === operation);
  if (!descriptor || descriptor.method !== method || descriptor.path[0] !== '/' || /[?#]/.test(descriptor.path)) return undefined;
  const parameters = descriptor.path.match(/\{[^}]+\}/g);
  if (parameters?.length !== 3 || parameters[0] !== '{libraryId}' || parameters[1] !== '{groupKind}' || parameters[2] !== '{seriesId}') return undefined;
  return descriptor.path.replace('{libraryId}', encodeURIComponent(target.libraryId)).replace('{groupKind}', target.groupKind).replace('{seriesId}', target.seriesId);
}
export function readingSavedReferencePath(referenceId: string): string | undefined {
  if (!identifier(referenceId)) return undefined;
  const operation: string = 'delete_reading_saved_group_reference';
  const descriptor = routes.find(route => route.id === operation);
  const parameters = descriptor?.path.match(/\{[^}]+\}/g);
  if (!descriptor || descriptor.method !== 'DELETE' || descriptor.path[0] !== '/' || /[?#]/.test(descriptor.path) || parameters?.length !== 1 || parameters[0] !== '{referenceId}') return undefined;
  return descriptor.path.replace('{referenceId}', encodeURIComponent(referenceId));
}
export function parseReadingSavedGroupMembership(value: unknown, serverId: string, target: ReadingSavedGroupTarget, operationId?: string): ReadingSavedGroupMembership {
  if (!record(value) || !validReadingSavedGroupTarget(target) || value.serverId !== serverId || value.libraryId !== target.libraryId || value.groupKind !== target.groupKind || value.seriesId !== target.seriesId
    || typeof value.listed !== 'boolean' || !revision(value.revision) || typeof value.viewerFence !== 'string' || !/^[a-f0-9]{64}$/.test(value.viewerFence) || value.operationId !== operationId
    || (value.revision === 0 ? value.listed || value.referenceId !== undefined : !identifier(value.referenceId))) return bad();
  return Object.freeze({...target, listed: value.listed, revision: value.revision, viewerFence: value.viewerFence, ...(value.referenceId === undefined ? {} : {referenceId: value.referenceId as string}), ...(operationId === undefined ? {} : {operationId})});
}
export function validReadingSavedReferenceCommand(value: ReadingSavedReferenceCommand): boolean {return identifier(value.operationId) && revision(value.expectedRevision) && value.expectedRevision > 0;}
export function validReadingSavedGroupCommand(value: ReadingSavedGroupCommand): boolean {return identifier(value.operationId) && revision(value.expectedRevision) && typeof value.listed === 'boolean';}
export function parseReadingSavedReferenceReceipt(value: unknown, serverId: string, referenceId: string, command: ReadingSavedReferenceCommand): ReadingSavedReferenceReceipt {
  if (!record(value) || value.serverId !== serverId || value.referenceId !== referenceId || !identifier(referenceId) || value.operationId !== command.operationId || value.removed !== true || !revision(value.revision) || value.revision < command.expectedRevision) return bad();
  return Object.freeze({referenceId, operationId: command.operationId, removed: true, revision: value.revision});
}

export function parseSavedReadingGroupCard(value: unknown): SavedReadingGroupCard {
  if (!record(value) || value.kind !== 'reading_group' || !identifier(value.id) || !revision(value.revision) || value.revision < 1 || typeof value.hidden !== 'boolean' || typeof value.removeMyList !== 'boolean') return bad();
  const base = {id: value.id, kind: 'reading_group' as const, revision: value.revision, removeMyList: value.removeMyList};
  // A private unavailable reference never retains any supplied target or art.
  if (value.hidden) return Object.freeze({...base, hidden: true});
  const group = value.readingGroup, navigation = value.navigation;
  if (!identifier(value.libraryId) || !record(group) || !kind(group.groupKind) || !series(group.seriesId) || !record(navigation) || navigation.view !== 'series' || navigation.entityId !== group.seriesId
    || typeof value.title !== 'string' || !value.title.trim() || value.title.length > 4096 || /[\u0000-\u001f\u007f]/.test(value.title) || !revision(value.count) || value.count < 1) return bad();
  const pivot = group.groupKind === 'magazine' ? 'magazines' : group.groupKind === 'newspaper' ? 'newspapers' : undefined;
  if (navigation.pivot !== pivot) return bad();
  if (value.posterUrl !== undefined && (typeof value.posterUrl !== 'string' || !/^\/v1\/items\/[A-Za-z0-9_-]{1,128}\/art\/cover\?v=[a-f0-9]{64}$/.test(value.posterUrl))) return bad();
  return Object.freeze({...base, hidden: false, libraryId: value.libraryId, title: value.title, count: value.count,
    readingGroup: Object.freeze({groupKind: group.groupKind, seriesId: group.seriesId}), navigation: Object.freeze({view: 'series', entityId: group.seriesId, ...(pivot ? {pivot} : {})}),
    ...(value.posterUrl === undefined ? {} : {posterUrl: value.posterUrl as string})});
}
