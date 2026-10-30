import {readingPreferences, readingPreferencesPatch, type ReadingPreferences, type ReadingPreferencesChange} from '../../../../../packages/client-core/src/preference-effects';
import type {PreferenceSnapshot, PreferenceValue} from '../../../../../packages/client-core/src/preferences';
import {readingSettingsProperty} from '../../../../../packages/client-core/src/presentation/settings-bindings';
import {settingsSpec, type SettingsRow} from '../../../../../packages/client-core/src/presentation/settings-spec';
import type {NativeShellBootstrapContext} from '../../shell/bootstrap';
import {NativeScreenLifetime} from '../settings-live/lifetime';
import {createBoundNativeSettingsController, type NativeSettingsController} from '../settings-live/settings-controller';

export const nativeReadingSettingsSection = settingsSpec.find(section => section.scope === 'account' && section.id === 'reading')!;
const rows = [...(nativeReadingSettingsSection.groups ?? []), ...(nativeReadingSettingsSection.advanced?.groups ?? [])].flatMap(group => group.rows);
type Owner = Pick<NativeShellBootstrapContext, 'api' | 'authority' | 'current' | 'subscribeAuthority' | 'preferences' | 'subscribePreferences'>;
const sources = new WeakMap<Owner, NativeReadingSettingsPreferences>();
const unavailable = () => Object.assign(new Error('native_reading_preferences_unavailable'), {code: 'unsupported'});

/** Eligibility uses the selected server's actual registry/documents, never spec defaults. */
export function nativeReadingPreferencesAvailable(snapshot: PreferenceSnapshot): boolean {
  if (snapshot.deviceClass === 'television' || !snapshot.documents.some(document => document.scope === 'profile-server')) return false;
  if (rows.some(row => !snapshot.registry.fields.some(field => field.key === row.key && field.scopes.includes('profile-server')))) return false;
  try {readingPreferences(snapshot); return true;} catch {return false;}
}

/** Read and Settings borrow this one canonical preference reader for the authority.
 * Route changes do not retire it or create a second preference store. */
export class NativeReadingSettingsPreferences {
  private readonly controller: NativeSettingsController;
  private readonly lifetime: NativeScreenLifetime;
  private lastPreferences?: PreferenceSnapshot;
  private lastProjection?: ReadingPreferences;
  constructor(context: Owner) {
    if (!context.current() || !nativeReadingPreferencesAvailable(context.preferences())) throw unavailable();
    const lifetime = this.lifetime = new NativeScreenLifetime(context);
    this.controller = createBoundNativeSettingsController(lifetime, context);
    const offAuthority = context.subscribeAuthority(() => {
      if (lifetime.current()) return;
      offAuthority(); this.controller.dispose(); this.lastProjection = undefined; this.lastPreferences = undefined;
    });
  }
  current = (): boolean => this.lifetime.current();
  subscribe = (listener: () => void): (() => void) => {this.lifetime.assert(); return this.controller.subscribe(listener);};
  getState = () => {this.lifetime.assert(); return this.controller.getSnapshot();};
  /** Core receives the confirmed canonical snapshot unchanged, including registry and revisions. */
  getPreferenceSnapshot = (): PreferenceSnapshot => this.getState().preferences;
  getSnapshot = (): ReadingPreferences => {
    const preferences = this.getPreferenceSnapshot();
    if (preferences !== this.lastPreferences) {this.lastPreferences = preferences; this.lastProjection = readingPreferences(preferences);}
    return this.lastProjection!;
  };
  refresh = (): Promise<void> => {this.lifetime.assert(); return this.controller.refresh();};
  patch = async (changes: ReadingPreferencesChange): Promise<ReadingPreferences> => {
    this.lifetime.assert();
    const patch = readingPreferencesPatch(this.getState().preferences, changes);
    const accepted = await this.controller.apply('profile-server', patch);
    this.lifetime.assert();
    if (!accepted) throw this.getState().error ?? Object.assign(new Error('native_reading_preferences_busy'), {code: 'cancelled'});
    return this.getSnapshot();
  };
  rowEnabled = (row: SettingsRow): boolean => {
    const condition = row.enableWhen;
    if (!condition) return true;
    const property = readingSettingsProperty(condition.id);
    if (!property) return false;
    const current = this.getSnapshot()[property];
    return (!condition.in || condition.in.includes(current)) && (!condition.notIn || !condition.notIn.includes(current));
  };
  setRow = (id: string, value: PreferenceValue | null): Promise<ReadingPreferences> => {
    this.lifetime.assert();
    const row = rows.find(row => row.id === id), property = readingSettingsProperty(id);
    if (!row || !property || !this.rowEnabled(row)) return Promise.reject(unavailable());
    return this.patch({[property]: value});
  };
}

export function nativeReadingSettingsPreferences(context: Owner): NativeReadingSettingsPreferences {
  if (!context.current()) throw unavailable();
  let source = sources.get(context);
  if (!source) {source = new NativeReadingSettingsPreferences(context); sources.set(context, source);}
  if (!source.current()) throw unavailable();
  return source;
}
