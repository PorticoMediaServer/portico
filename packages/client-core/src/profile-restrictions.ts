import type {Restrictions} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {unreadableServerResponse} from './server-messages.ts';

/** One choice for a profile's highest rating. The server keeps the limit as an age; these are the
 * choices the screens offer, each the age its rating admits (13 shows PG-13 and below). */
export type RatingValue = Readonly<{code: string; label: string; minimumAge: number}>;
export type RatingSystem = Readonly<{id: string; name: string; region: string; values: readonly RatingValue[]; screens: readonly string[]}>;
export const RATING_LIMITS: RatingSystem = Object.freeze({id: 'age', name: 'Age', region: '', screens: Object.freeze([]), values: Object.freeze([
  Object.freeze({code: 'G', label: 'G', minimumAge: 0}),
  Object.freeze({code: 'PG', label: 'PG', minimumAge: 8}),
  Object.freeze({code: 'PG-13', label: 'PG-13', minimumAge: 13}),
  Object.freeze({code: 'R', label: 'R', minimumAge: 17}),
])});

/**
 * What a profile is kept from seeing (`Restrictions` on the wire). The server decides what is shown;
 * a screen only shows and edits this. `maximumAgeRating` is '' for no limit; an age the choices do not
 * name shows as the highest choice below it. `blockedLabels` are Tag ids (the wire still says labels).
 * `libraries` and `allLibraries` are sent back unchanged with every save, since a save replaces all of it.
 * Downloads are read as the server sends them; `accountAllowDownloads` is the account's own switch, sent back unchanged.
 * Live TV, recordings and Watch Together are not read here yet and read as allowed.
 */
export type ProfileRestrictions = Readonly<{profileId: string; ratingSystem: string; maximumAgeRating: string; maximumAge: number; allowUnrated: boolean;
  blockedLabels: readonly string[]; allLibraries: boolean; libraries: readonly string[]; accountAllLibraries: boolean; accountLibraries: readonly string[];
  allowDownloads: boolean; accountAllowDownloads: boolean; allowLiveTv: boolean; allowDvr: boolean; allowWatchTogether: boolean}>;
/** Allow unrated titles, downloads, Live TV, recordings and Watch Together are not sent: the server does not keep them yet. */
export type RestrictionChanges = Partial<Omit<ProfileRestrictions, 'profileId' | 'maximumAge' | 'accountAllLibraries' | 'accountLibraries' | 'accountAllowDownloads'>>;

const ids = (value: unknown): string[] => Array.isArray(value) ? value.filter((v): v is string => typeof v === 'string' && v.length > 0) : [];
/** The choice that shows a stored age: the highest whose age is at or below it. */
export function ratingCode(age: number | null | undefined): string {
  if (age === null || age === undefined) return '';
  return [...RATING_LIMITS.values].reverse().find(value => value.minimumAge <= age)?.code ?? RATING_LIMITS.values[0]!.code;
}

export function parseRestrictions(profileId: string, raw: unknown): ProfileRestrictions {
  const v = raw as Partial<Restrictions> | null;
  if (!v || typeof v !== 'object' || (v.maxRatingAge !== null && v.maxRatingAge !== undefined && !Number.isInteger(v.maxRatingAge))) throw new Error(unreadableServerResponse);
  const age = v.maxRatingAge ?? null, code = ratingCode(age);
  return Object.freeze({profileId, ratingSystem: age === null ? '' : RATING_LIMITS.id, maximumAgeRating: code, maximumAge: age ?? -1, allowUnrated: false,
    blockedLabels: Object.freeze(ids(v.blockedLabels)), allLibraries: v.allLibraries !== false, libraries: Object.freeze(ids(v.libraries)),
    accountAllLibraries: v.accountAllLibraries !== false, accountLibraries: Object.freeze(ids(v.accountLibraries)),
    allowDownloads: v.allowDownloads !== false, accountAllowDownloads: v.accountAllowDownloads !== false, allowLiveTv: true, allowDvr: true, allowWatchTogether: true});
}

/** `member` is the account id when an owner or administrator manages someone else's profile. */
export async function readRestrictions(api: ServerRequester, profileId: string, member?: string, signal?: AbortSignal): Promise<ProfileRestrictions> {
  return parseRestrictions(profileId, await callServer(api, 'GET /v1/profiles/{id}/restrictions', {id: profileId, query: {member}, signal}));
}
/** Saves the limits read in `current` with `changes`; the later save wins. */
export async function saveRestrictions(api: ServerRequester, current: ProfileRestrictions, changes: RestrictionChanges, member?: string, signal?: AbortSignal): Promise<ProfileRestrictions> {
  const next = {...current, ...changes};
  const value = next.maximumAgeRating === '' ? undefined : RATING_LIMITS.values.find(option => option.code === next.maximumAgeRating);
  if (next.maximumAgeRating !== '' && !value) throw new Error('Choose one of the ratings offered.');
  const body: Restrictions = {maxRatingAge: value ? value.minimumAge : null, blockedLabels: [...next.blockedLabels], allLibraries: next.allLibraries,
    libraries: [...next.libraries], accountAllLibraries: current.accountAllLibraries, accountLibraries: [...current.accountLibraries],
    accountAllowDownloads: current.accountAllowDownloads};
  return parseRestrictions(current.profileId, await callServer(api, 'PUT /v1/profiles/{id}/restrictions', {id: current.profileId, query: {member}, body, signal}));
}

export type TagChoice = Readonly<{id: string; name: string}>;
/** The Tags a profile can have blocked: every Tag in the libraries the main profile sees, by name. */
export async function tagChoices(api: ServerRequester, signal?: AbortSignal): Promise<readonly TagChoice[]> {
  const libraries = (await callServer(api, 'GET /v1/libraries', {signal}))?.items ?? [];
  const pages = await Promise.all(libraries.map(library => callServer(api, 'GET /v1/facets', {query: {facet: 'label', library: library.id}, signal})));
  const byId = new Map<string, string>();
  for (const page of pages) for (const value of page?.values ?? []) if (typeof value.value === 'string' && typeof value.label === 'string') byId.set(value.value, value.label);
  return Object.freeze([...byId].map(([id, name]) => Object.freeze({id, name})).sort((a, b) => a.name.localeCompare(b.name)));
}
