import type {PreferenceValues} from '../../../../packages/client-core/src/preferences';
import type {LocalPlaybackScope} from '../../../../packages/player-core/src/local-playback-identity';
import {NativeQueueStore} from '../platform/queue-storage';
import type {NativeShellBootstrapContext} from './bootstrap';
import type {RememberedDownloadsViewer} from './remembered-downloads-viewer';
import {nativePlaybackSettings} from './playback-settings';

const keys = ['playback.defaultSpeed', 'audiobooks.defaultSpeed', 'playback.introSkip', 'playback.recapSkip', 'playback.creditsSkip',
  'playback.subtitleSize', 'playback.subtitleColor', 'playback.subtitleBackground', 'playback.subtitlePosition', 'playback.subtitleFont',
  'playback.keepSubtitleStyling', 'playback.useSystemCaptionStyle'] as const;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const same = (owner: LocalPlaybackScope, raw: unknown) => object(raw)
  && (['authority', 'serverId', 'accountId', 'profileId', 'sessionFamilyId'] as const).every(name => raw[name] === owner[name]);
// One current-family settings record, never a growing family history.
const key = 'remembered-playback-settings:v1';
const cancelled = () => Object.assign(new Error('remembered_playback_settings_closed'), {name: 'AbortError', code: 'cancelled'});
const records = new NativeQueueStore();

/** Preserve only actual registry-effective playback fields, never registry
 * defaults invented by an offline client, credentials or Home authority. */
export async function rememberNativePlaybackPreferences(context: NativeShellBootstrapContext, values: PreferenceValues, installation: unknown): Promise<void> {
  const session = context.session(), viewer = session?.viewer;
  if (!session || !viewer || viewer.authority !== 'local') return;
  const owner: LocalPlaybackScope = Object.freeze({authority: viewer.authority, serverId: viewer.serverId,
    accountId: viewer.accountId, profileId: viewer.profileId, sessionFamilyId: session.sessionFamilyId});
  const current = () => {const actual = context.session(), authority = context.authority();
    return context.current() && actual?.sessionFamilyId === owner.sessionFamilyId && actual.viewer.authority === owner.authority
      && actual.viewer.serverId === owner.serverId && actual.viewer.accountId === owner.accountId && actual.viewer.profileId === owner.profileId
      && authority.serverId === owner.serverId && authority.accountId === owner.accountId && authority.profileId === owner.profileId;};
  const check = () => {if (!current()) throw cancelled();}; check();
  const effective = Object.freeze(Object.fromEntries(keys.map(name => [name, values[name]])));
  nativePlaybackSettings(effective, installation); // Same strict canonical parser; missing values refuse.
  check();
  await records.change(key, () => {check(); return {record: {version: 1, owner, values: effective}, value: undefined};}, undefined, check);
  check();
}

/** Settings only. This neither admits an artifact nor creates a server session.
 * No cache means unavailable; it never substitutes guessed profile defaults. */
export async function readRememberedPlaybackSettings(identity: RememberedDownloadsViewer, isCurrent: () => boolean) {
  const owner = identity.owner;
  const check = () => {if (!identity.current() || !isCurrent()) throw cancelled();}; check();
  const raw = await records.change(key, record => {check(); return {record, value: record};}, undefined, check); check();
  if (!object(raw) || raw.version !== 1 || !same(owner, raw.owner) || !object(raw.values)) return undefined;
  const saved = raw.values;
  if (Object.keys(saved).length !== keys.length || keys.some(name => !Object.prototype.hasOwnProperty.call(saved, name))) return undefined;
  const effective: Record<string, string | number | boolean> = {};
  for (const name of keys) {const field = saved[name];
    if (typeof field !== 'boolean' && !(typeof field === 'number' && Number.isFinite(field)) && !(typeof field === 'string' && field.length <= 32)) return undefined;
    effective[name] = field;
  }
  const installation = await records.change('preferences:v1', record => {check(); return {record, value: record};}, undefined, check); check();
  const settings = nativePlaybackSettings(effective, installation); check();
  return Object.freeze({...settings, skips: Object.freeze({...settings.skips})});
}
