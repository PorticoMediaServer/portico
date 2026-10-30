import {encodedRoutePath} from "../../../contracts/src/route-paths.ts";
/**
 * Film and TV metadata lookups per library (`GET`/`PUT /v1/libraries/{id}/metadata/screen`).
 * Remote lookups are on by default with a per-library opt-out; the console
 * shows one switch, "Look up titles online".
 *
 * The server reports `status`: `enabled`, `needs_consent` (server-wide consent never given),
 * `declined` (an owner withdrew it server-wide) or `disabled` (this library is off); consent
 * takes precedence. The switch sends `enabled` with the library's providers, language, region and
 * refresh mode as they are (an empty provider list means the server's per-kind defaults). It adds
 * `confirmRemote: true` only to re-grant from `needs_consent`, and never sends
 * `confirmRemote: false` (that is the server-wide withdrawal). A conflict keeps the original request for review.
 */
export type ScreenLookupPolicy = Readonly<{
  libraryKind: string;
  revision: number;
  consentRevision: number;
  confirmed: boolean;
  enabled: boolean;
  providers: readonly string[];
  availableProviders: readonly string[];
  language: string;
  region: string;
  refreshMode: 'replace_unlocked' | 'fill_missing';
  disclosureVersion: string;
  /** The server's effective state (absent from servers before bf6a87f8). */
  status?: string;
  /** The library's metadata source, `online` or `local`. */
  agent?: string;
}>;

export type LookupStatus = 'on' | 'off' | 'needs_consent' | 'declined';
export const SCREEN_DISCLOSURE_VERSION = 'screen-metadata-v1';

const strings = (v: unknown, max = 8): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string' && x.length <= 32).slice(0, max) : []);
const positive = (v: unknown) => typeof v === 'number' && Number.isSafeInteger(v) && v >= 1;

/** Reads the library's policy; throws on a reply without the fields a save needs. */
export function parseScreenLookup(raw: unknown): ScreenLookupPolicy {
  const o = raw as Record<string, unknown> | null;
  if (!o || typeof o !== 'object' || !positive(o.revision) || !positive(o.consentRevision) || typeof o.confirmed !== 'boolean' || typeof o.enabled !== 'boolean') throw new Error('Invalid metadata policy.');
  const status = typeof o.status === 'string' ? o.status : undefined;
  return Object.freeze({
    libraryKind: typeof o.libraryKind === 'string' ? o.libraryKind : '',
    revision: o.revision as number,
    consentRevision: o.consentRevision as number,
    confirmed: o.confirmed,
    enabled: o.enabled,
    providers: Object.freeze(strings(o.providers)),
    availableProviders: Object.freeze(strings(o.availableProviders)),
    language: typeof o.language === 'string' ? o.language : '',
    region: typeof o.region === 'string' && /^[A-Z]{2}$/.test(o.region) ? o.region : '',
    refreshMode: o.refreshMode === 'fill_missing' ? 'fill_missing' : 'replace_unlocked',
    disclosureVersion: typeof o.disclosureVersion === 'string' ? o.disclosureVersion : '',
    ...(status ? {status: status.slice(0, 64)} : {}),
    ...(typeof o.agent === 'string' && o.agent.length <= 64 ? {agent: o.agent} : {}),
  });
}

/** Where lookups stand for this library, from the server's `status` (derived on older servers). */
export function lookupStatus(p: ScreenLookupPolicy): LookupStatus {
  switch (p.status) {
    case 'enabled': return 'on';
    case 'disabled': return 'off';
    case 'needs_consent': return 'needs_consent';
    case 'declined': return 'declined';
  }
  if (!p.confirmed) return 'needs_consent';
  return p.enabled ? 'on' : 'off';
}

/** The switch shows this library's own choice. Waiting for consent shows off, so turning it on
 * grants it; after a server-wide withdrawal it still shows the library's preference. */
export function lookupsOn(p: ScreenLookupPolicy): boolean {
  const s = lookupStatus(p);
  return s === 'on' || (s === 'declined' && p.enabled);
}

/** The `PUT` body that turns this library's lookups on or off. */
export function lookupUpdate(p: ScreenLookupPolicy, on: boolean): Record<string, unknown> {
  return {
    expectedRevision: p.revision,
    expectedConsentRevision: p.consentRevision,
    enabled: on,
    providers: [...p.providers],
    language: p.language || 'en',
    region: p.region,
    refreshMode: p.refreshMode,
    disclosureVersion: p.disclosureVersion || SCREEN_DISCLOSURE_VERSION,
    ...(on && lookupStatus(p) === 'needs_consent' ? {confirmRemote: true} : {}),
  };
}

type Request = <T>(path: string, method?: string, body?: unknown) => Promise<T>;
/** Saves the authored switch against its original policy revision. */
export async function saveLookup(request: Request, libraryId: string, policy: ScreenLookupPolicy, on: boolean): Promise<void> {
  const path = encodedRoutePath("get_libraries_id_metadata_screen", {"id": encodeURIComponent(libraryId)});
  await request(path, 'PUT', lookupUpdate(policy, on));
}

/** Catalogue id for the library's metadata status line. */
export const lookupStatusId = (s: LookupStatus) => `web.metadataLookup.status.${s === 'needs_consent' ? 'needsConsent' : s}`;
