import {parsePreferenceSnapshot, type PreferenceSnapshot} from '../../../../packages/client-core/src/preferences';
import {readingPreferences} from '../../../../packages/client-core/src/preference-effects';
import type {LocalPlaybackScope} from '../../../../packages/player-core/src/local-playback-identity';
import {NativeQueueStore} from '../platform/queue-storage';
import {nativeReadingPreferencesAvailable} from '../screens/settings/reading-preferences';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {RememberedDownloadsViewer} from './remembered-downloads-viewer';

const key = 'remembered-reading-settings:v1';
const records = new NativeQueueStore();
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const same = (owner: LocalPlaybackScope, value: unknown) => object(value)
  && (['authority', 'serverId', 'accountId', 'profileId', 'sessionFamilyId'] as const).every(name => value[name] === owner[name]);
const cancelled = () => Object.assign(new Error('remembered_reading_settings_closed'), {name: 'AbortError', code: 'cancelled'});

function validate(raw: unknown, deviceClass: PreferenceSnapshot['deviceClass']): PreferenceSnapshot {
  const snapshot = parsePreferenceSnapshot(raw);
  if (snapshot.deviceClass !== deviceClass || !nativeReadingPreferencesAvailable(snapshot)) throw new Error('reading_preferences_unavailable');
  readingPreferences(snapshot);
  return snapshot;
}

/** Store the confirmed complete snapshot, never credentials or a fabricated registry. */
export async function rememberNativeReadingPreferences(context: NativeShellBootstrapContext, snapshot: PreferenceSnapshot): Promise<void> {
  const session = context.session(), viewer = session?.viewer;
  if (!session || !viewer || viewer.authority !== 'local') return;
  const owner: LocalPlaybackScope = Object.freeze({authority: viewer.authority, serverId: viewer.serverId,
    accountId: viewer.accountId, profileId: viewer.profileId, sessionFamilyId: session.sessionFamilyId});
  const check = () => {const actual = context.session(), authority = context.authority();
    if (!context.current() || actual?.sessionFamilyId !== owner.sessionFamilyId || actual.viewer.authority !== owner.authority
      || actual.viewer.serverId !== owner.serverId || actual.viewer.accountId !== owner.accountId || actual.viewer.profileId !== owner.profileId
      || authority.serverId !== owner.serverId || authority.accountId !== owner.accountId || authority.profileId !== owner.profileId) throw cancelled();};
  check(); validate(snapshot, 'mobile'); check();
  await records.change(key, () => {check(); return {record: {version: 1, owner, snapshot}, value: undefined};}, undefined, check);
  check();
}

/** Missing or invalid settings never substitute defaults or affect AV admission. */
export async function readRememberedReadingPreferences(identity: RememberedDownloadsViewer, isCurrent: () => boolean, deviceClass: PreferenceSnapshot['deviceClass']) {
  const check = () => {if (!identity.current() || !isCurrent()) throw cancelled();}; check();
  const raw = await records.change(key, record => {check(); return {record, value: record};}, undefined, check); check();
  if (!object(raw) || raw.version !== 1 || !same(identity.owner, raw.owner)) return undefined;
  try {const snapshot = validate(raw.snapshot, deviceClass); check(); return snapshot;} catch (error) {check(); return undefined;}
}

/** Unknown or malformed history permission must never enable offline writes. */
export function rememberedReadingHistoryPaused(snapshot: PreferenceSnapshot): boolean {
  return snapshot.effective['privacy.pauseWatchHistory'] !== false;
}
