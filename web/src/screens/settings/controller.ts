import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import type {QuietInput} from './quiet-refresh';
import {LOG_DETAIL_MINUTES, type LogDetailMinutes} from '@core/server-administration.ts';
import {ConsoleClient, type Measurement} from '@core/console.ts';
import {PreferencesReader} from '@core/preferences-reader.ts';
import {validatePreferencePatch, type PreferencePatch, type PreferenceScope, type PreferenceSnapshot} from '@core/preferences.ts';
import {searchConfiguration, type SettingsConfiguration} from '@core/settings-configuration.ts';
import {applySettingsBindingPatch, changedFromDefault, exactPresetValue, presetPatch, readSettingsBinding, settingsBinding, settingsBindingPatch, type SettingsBindingForm, type SettingsBindingSources, type SettingsBindingValue} from '@core/presentation/settings-bindings.ts';
import {settingsRowIndex, settingsSpec, type SettingsRow, type SettingsSection, type SettingsValue} from '@core/presentation/settings-spec.ts';
import {runtimeForm, connectivityForm, maintenanceForm, alertsForm, logsForm, type ServerFormDeps} from '@core/server-admin/server-forms.ts';
import type {DraftForm} from '@core/server-admin/draft-form.ts';
import {randomId} from '@core/random-id.ts';
import {defaultI18n} from '@i18n';
import type {HttpLocalApi} from '@core/index.ts';
import type {SettingPresentation} from '../../kit/settings';
import type {SettingsContext, SettingsPageSnapshot} from './Settings';
import {settingsServerHeader} from './server-header';
import {configurationFieldEligible, settingsRowEligible, type SettingsEligibility} from './eligibility';
import {settingsRowVisible, settingsSectionVisible, type SettingsEnvironment} from '@core/presentation/settings-visibility.ts';
import type {SettingsDevicePort} from '../../platform/settings-platform';
import {canWriteDeviceSettings, readDeviceSettingsBinding, writeDeviceSettingsBinding} from './device-bindings';
import {settingsPresetOptions} from './preset-options';
import {SettingsSectionReads} from './section-loading';
import {SettingsLibrarySource} from './library-source';
import {SettingsLibraryFoldersSource} from './library-folders-source';
import {SettingsBackupSource} from './backup-source';

type DocumentForm = Readonly<{
  invalidateQuiet: () => void; refresh: () => Promise<QuietRefreshOutcome>; subscribe: (listener: () => void) => () => void; blocked: () => boolean; settlement: () => unknown;
  load: () => Promise<void>; dispose: () => void;
  value: () => Readonly<Record<string, unknown>> | undefined;
  status: () => Readonly<Record<string, unknown>> | undefined;
  error: () => unknown;
  patch: (values: Readonly<Record<string, unknown>>) => Promise<void>;
}>;
function settingValue(value: unknown): SettingsValue {
  if (value === null || typeof value === 'string' || typeof value === 'boolean' || typeof value === 'number' && Number.isFinite(value)) return value;
  if (Array.isArray(value)) return value.map(settingValue);
  if (typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype) return Object.fromEntries(Object.entries(value).map(([key, next]) => [key, settingValue(next)]));
  throw new Error('settings_value_unavailable');
}
function documentForm<T extends object, S>(form: DraftForm<T, S>): DocumentForm {
  return {
    invalidateQuiet: form.invalidateQuiet, refresh: form.refresh.bind(form), subscribe: form.subscribe,
    blocked: () => form.getSnapshot().saving || form.getSnapshot().loading || form.getSnapshot().saveError !== undefined,
    settlement: () => form.getSnapshot().draft,
    load: form.load.bind(form), dispose: form.dispose.bind(form),
    value: () => form.getSnapshot().saved as Readonly<Record<string, unknown>> | undefined,
    status: () => {
      const status = form.getSnapshot().status;
      return status && typeof status === 'object' && !Array.isArray(status) ? status as Readonly<Record<string, unknown>> : undefined;
    },
    error: () => form.getSnapshot().loadError,
    patch: async values => {
      const state = form.getSnapshot();
      if (!state.saved || state.saving) throw new Error('settings_document_unavailable');
      form.set(draft => applySettingsBindingPatch(draft as Record<string, unknown>, {form: 'runtime', values}) as T);
      await form.save();
    },
  };
}
/** Supplied by the checked Reading facade adapter; the UI owns no row/key map. */
export type ReadingSettingsAdapter = Readonly<{
  read: (rowId: string, snapshot: PreferenceSnapshot) => SettingsBindingValue;
  patch: (rowId: string, value: SettingsValue | null, snapshot: PreferenceSnapshot) => PreferencePatch;
}>;
export type SettingsControllerOptions = Readonly<{
  api: Pick<HttpLocalApi, 'request'>; serverId: string; viewerId: string;
  role: 'owner' | 'admin' | 'member'; mainProfile: boolean;
  capabilities: Readonly<{manageServer: boolean; managePeople: boolean; manageSignIn: boolean}>;
  preferences: PreferenceSnapshot; reader: PreferencesReader; readConfiguration?: (signal: AbortSignal) => Promise<SettingsConfiguration>; isCurrent: () => boolean; reportError: (error: unknown) => void;
  eligibility: SettingsEligibility; reading: ReadingSettingsAdapter; environment: SettingsEnvironment;
  device?: SettingsDevicePort;
}>;

/** One selected session owns every document and pending write. No constructor fetches. */
export class SettingsController {
  private readonly options: SettingsControllerOptions;
  private readonly console: ConsoleClient;
  private readonly preferencesReader: PreferencesReader;
  private readonly forms: Partial<Record<SettingsBindingForm, DocumentForm>> = {};
  private preferences: PreferenceSnapshot;
  private configuration?: SettingsConfiguration;
  private readonly sectionReads: SettingsSectionReads;
  private readonly library: SettingsLibrarySource;
  /** The one folder source for the selected library; its headless port is `folders`. */
  readonly folders: SettingsLibraryFoldersSource;
  readonly backups: SettingsBackupSource;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private readonly rowErrors = new Map<string, unknown>();
  private readonly offDevice?: () => void;
  private readonly offPreferences: () => void;
  private closed = false;
  private headerLoading?: Promise<void>;
  private headerLoaded = false;
  private headerHealth?: Measurement;
  private headerStorage?: Measurement;
  private readonly quietClients = new Map<string,{client:ConsoleClient;abort:AbortController}>();
  private cancelQuietRead = (id: string) => {const read=this.quietClients.get(id);this.quietClients.delete(id);read?.abort.abort();read?.client.dispose();};
  private async quietRead<T>(id:string,read:(client:ConsoleClient,signal:AbortSignal)=>Promise<T>,install:(value:T)=>void,serial?:number):Promise<QuietRefreshOutcome> {
    this.cancelQuietRead(id);
    const owner={client:new ConsoleClient(this.options.api,{serverId:this.options.serverId,viewerId:this.options.viewerId}),abort:new AbortController()};this.quietClients.set(id,owner);
    const owns=()=>this.current() && !owner.abort.signal.aborted && this.quietClients.get(id)===owner && (serial===undefined || serial===this.writeSerial && !this.writing && !this.inputActive);
    try {const value=await read(owner.client,AbortSignal.any([this.lifetime.signal,owner.abort.signal]));if(!owns())return {kind:'deferred'};install(value);this.publish({});return {kind:'adopted'};}
    catch(error){return owns()?{kind:'unreadable',error}:{kind:'deferred'};}
    finally{if(this.quietClients.get(id)===owner)this.quietClients.delete(id);owner.abort.abort();owner.client.dispose();}
  }
  private writing = false;
  private writeSerial = 0;
  private inputActive = false;
  setQuietInputActive = (active: boolean) => {
    if (!this.current() || active === this.inputActive) return;
    this.inputActive = active;++this.writeSerial;
    if(active)this.cancelQuietRead('configuration');
    if(active)this.library.invalidateQuiet();
    if(active) for(const form of Object.values(this.forms))form.invalidateQuiet();
    this.publish({});
  };
  private readonly offForms: (() => void)[] = [];
  private header: ReturnType<typeof settingsServerHeader> = {facts: []};
  constructor(options: SettingsControllerOptions) {
    this.options = options; this.preferences = options.preferences;
    this.sectionReads = new SettingsSectionReads(this.current, () => {for (const listener of this.listeners) listener();});
    const libraryCurrent = () => this.current() && options.mainProfile && options.capabilities.manageServer && options.role !== 'member';
    this.library = new SettingsLibrarySource(options.api, libraryCurrent, () => this.publish({}), options.reportError, {serverId: options.serverId, viewerId: options.viewerId});
    const scope = {serverId: options.serverId, viewerId: options.viewerId};
    // A folder read is tab-scoped: its headless port carries the error, so it never fails the whole section.
    this.folders = new SettingsLibraryFoldersSource(options.api, scope, libraryCurrent, () => this.publish({}), () => {});
    this.console = new ConsoleClient(options.api, scope);
    this.backups = new SettingsBackupSource(options.api, options.serverId, () => this.current() && !!this.forms.runtime, () => this.publish({}));
    this.preferencesReader = options.reader;
    this.offPreferences = options.reader.subscribe(() => {const snapshot = options.reader.getSnapshot('web');if (snapshot && this.current()) {this.preferences = snapshot;this.publish({});}});
    this.offDevice = options.device?.subscribe(() => this.publish({}));
    if (options.role === 'owner' && options.mainProfile && options.capabilities.manageServer) {
      const deps: ServerFormDeps = {api: options.api, console: this.console, serverId: options.serverId, operationId: randomId};
      this.forms.runtime = documentForm(runtimeForm(deps));
      this.forms.connectivity = documentForm(connectivityForm(deps));
      this.forms.maintenance = documentForm(maintenanceForm(deps));
      this.forms.alerts = documentForm(alertsForm(deps));
      this.forms.logs = documentForm(logsForm(deps));
      for (const form of Object.values(this.forms)) this.offForms.push(form.subscribe(() => this.publish({})));
    }
  }
  private current = () => !this.closed && this.options.isCurrent();
  private readConfiguration = async () => {
    const configuration = await (this.options.readConfiguration?.(this.lifetime.signal) ?? this.console.configuration());
    if (this.current()) this.configuration = configuration;
  };
  getSnapshot = (section: SettingsSection) => this.sectionReads.snapshot(`${section.scope}/${section.id}`);
  messageDetailMinutes = (): LogDetailMinutes | undefined => {
    if (!this.current()) return;
    const value = this.forms.logs?.status()?.logDetailMinutes;
    return typeof value === 'number' && (LOG_DETAIL_MINUTES as readonly number[]).includes(value) ? value as LogDetailMinutes : undefined;
  };
  preferencesSnapshot = () => this.current() ? this.preferences : undefined;
  serverHealth = () => this.current() ? this.header.health : undefined;
  serverFacts = () => this.current() ? this.header.facts : [];
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<SettingsPageSnapshot>) {
    if (!this.current()) return;
    this.sectionReads.update(patch);
  }
  sectionVisible = (section: SettingsSection) => {
    if (!settingsSectionVisible(section, this.options.environment)) return false;
    if (section.scope === 'account') return true;
    if (!this.options.mainProfile) return false;
    if (section.id === 'people') return this.options.capabilities.managePeople;
    if (section.id === 'signin') return this.options.capabilities.manageSignIn;
    if (section.id === 'remote-sources') return this.options.capabilities.manageServer && this.options.role === 'owner' && this.options.environment.signIn === 'local';
    return this.options.capabilities.manageServer && (this.options.role === 'owner' || section.id === 'dashboard' || section.id === 'libraries');
  };
  eligible = (row: SettingsRow, context?: SettingsContext): boolean => {
    const location = settingsRowIndex.get(row.id);
    if (!location || !this.current() || !settingsRowVisible(row, {...this.options.environment, role: this.options.role})) return false;
    const selected = context ?? {section: location.section, panelId: location.panelId, tabId: location.tabId};
    return settingsRowEligible(row, selected, {
      library: context => this.library.document(context.recordId) ?? this.options.eligibility.library(context),
      hasEngine: (row, context) => !!this.backupValue(row.id) || this.libraryRow(context) && !!this.library.read(context.recordId, row.id) || this.options.eligibility.hasEngine(row, context),
    });
  };
  private libraryRow = (context?: SettingsContext) => context?.section.scope === 'server' && context.section.id === 'libraries' && context.panelId === 'library' && !!context.recordId;
  renameLibrary = (libraryId: string, name: string) => this.current() && !this.writing ? this.library.change(libraryId, 'libraries.name', name) : Promise.resolve(false);
  loadContext = (context: SettingsContext) => {
    const libraryId = this.libraryRow(context) && this.sectionVisible(context.section) ? context.recordId : undefined;
    return Promise.all([this.library.select(libraryId), this.folders.select(libraryId)]).then(() => undefined);
  };
  private backupValue(rowId: string): SettingsBindingValue | undefined {
    const settings = this.backups.getSnapshot().settings;
    if (!settings) return undefined;
    const current = rowId === 'storage.backupFolder' ? settings.backupFolder : rowId === 'storage.backupArtwork' ? settings.includeArtwork : rowId === 'storage.backupPassphrase' ? settings.passphraseProtected : undefined;
    return current === undefined ? undefined : {current, effective: current, default: undefined, source: 'server', configOnly: false};
  }
  private sources(): SettingsBindingSources {
    return {configuration: this.configuration, preferences: this.preferences,
      device: this.options.device?.getSnapshot().document?.values, deviceDefaults: this.options.device?.getSnapshot().document?.defaults,
      forms: {...Object.fromEntries(Object.entries(this.forms).map(([name, form]) => [name, form.value()])),
        connectivityStatus: this.forms.connectivity?.status()}};
  }
  private read(row: SettingsRow, context?: SettingsContext): SettingsBindingValue | undefined {
    if (!this.eligible(row, context)) return undefined;
    const backup = this.backupValue(row.id);if (backup) return backup;
    if (this.libraryRow(context)) return this.library.read(context?.recordId, row.id);
    const location = settingsRowIndex.get(row.id)!;
    const facts = this.options.device?.getSnapshot().facts;
    const fact = row.id === 'device.name' ? facts?.name?.value : row.id === 'device.appVersion' ? facts?.appVersion : undefined;
    if (fact !== undefined) return {current: fact, effective: fact, default: undefined, source: 'device', configOnly: false};
    if (location.section.scope === 'account' && location.section.id === 'reading') return this.options.reading.read(row.id, this.preferences);
    const options = row.control.options && settingsPresetOptions(row.control.options, {...this.options.environment, role: this.options.role});
    if (options?.some(option => option.sets)) {
      const ids = [...new Set(options.flatMap(option => Object.keys(option.sets ?? {})))];
      const values = new Map(ids.map(id => [id, settingsRowIndex.get(id)?.row]).map(([id, row]) => [id, row ? this.read(row as SettingsRow, context) : undefined]));
      if (!ids.length || ids.some(id => !values.get(id))) return undefined;
      const defaultsKnown = ids.every(id => values.get(id)!.default !== undefined);
      return {current: exactPresetValue(options, id => values.get(id)?.current), effective: exactPresetValue(options, id => values.get(id)?.effective),
        default: defaultsKnown ? exactPresetValue(options, id => values.get(id)?.default) : undefined, source: [...new Set(ids.map(id => values.get(id)!.source))].join(','), configOnly: false};
    }
    const binding = settingsBinding(row.id);
    if (binding?.form === 'device') return readDeviceSettingsBinding(binding, this.options.device, this.sources(), row.id);
    return binding && readSettingsBinding(binding, this.sources(), row.id);
  }
  rowState = (row: SettingsRow, context?: SettingsContext): SettingPresentation => {
    if (row.id === 'troubleshooting.detailLength' && this.forms.logs?.status()?.logDetailMinutes === undefined) return {present: false};
    const location = settingsRowIndex.get(row.id);
    if (!location || !this.sectionVisible(location.section)) return {present: false};
    // The drawn status line above Home networks says whether this connection
    // counts as home, from the same connectivity report the form holds.
    if (row.id === 'remote.thisDevice') {
      const status = this.forms.connectivity?.status(), connection = status?.connection;
      // The connectivity form publishes an untyped status document. Draw this row only from a
      // complete connection row: a real address string and an explicit home/away answer.
      if (typeof connection !== 'object' || connection === null || Array.isArray(connection)
        || !('clientAddress' in connection) || typeof connection.clientAddress !== 'string' || !connection.clientAddress
        || !('remote' in connection) || typeof connection.remote !== 'boolean') return {present: false};
      const address = connection.clientAddress, remote = connection.remote;
      return {present: true, statusTone: remote ? 'warn' : 'ok',
        displayValue: defaultI18n.t(remote ? 'next.remoteStatus.deviceAway' : 'next.remoteStatus.deviceHome', {address})};
    }
    const value = this.read(row, context);
    if (!value || value.configOnly) return {present: false};
    const current = value.current as SettingsValue;
    const option = row.control.options?.find(option => option.value === current);
    const choices = row.control.options?.some(option => option.sets) ? settingsPresetOptions(row.control.options, {...this.options.environment, role: this.options.role}).map(option => ({...option, label: defaultI18n.t(option.label), hint: option.hint && defaultI18n.t(option.hint)})) : undefined;
    return {present: true, value: current, defaultValue: value.default as SettingsValue | undefined,
      choices,
      changed: value.default !== undefined && changedFromDefault(value.effective, value.default),
      displayValue: option ? defaultI18n.t(option.label) : typeof current === 'boolean' ? defaultI18n.t(current ? 'next.settings.on' : 'next.settings.off') : typeof current === 'object' ? JSON.stringify(current) : String(current),
      disabled: this.writing || this.libraryRow(context) && this.library.getSnapshot().saving || !this.current(), saving: this.writing || this.libraryRow(context) && this.library.getSnapshot().saving,
      readOnly: row.id === 'storage.backupPassphrase' || settingsBinding(row.id)?.readOnly === true || settingsBinding(row.id)?.form === 'device' && !canWriteDeviceSettings(this.options.device) || row.id === 'device.appVersion' || row.id === 'device.name' && (!settingsBinding(row.id) || !canWriteDeviceSettings(this.options.device) || this.options.device?.getSnapshot().facts.name?.readOnly === true),
      error: this.rowErrors.has(row.id) ? defaultI18n.t('next.states.pageFailedBody') : undefined};
  };
  configurationMatches = (query: string) => this.configuration && this.options.role === 'owner' && this.options.mainProfile ? searchConfiguration(this.configuration, query).filter(field => configurationFieldEligible(field, this.eligible) && (settingsRowIndex.has(field.id) || settingsSpec.some(section => section.configOnly.some(item => item.key === field.key)))).map(field => ({...field, label: settingsRowIndex.has(field.id) ? defaultI18n.t(settingsRowIndex.get(field.id)!.row.label) : defaultI18n.t(settingsSpec.flatMap(section => section.configOnly).find(item => item.key === field.key)!.label), path: this.configuration!.path})) : [];
  load = (section: SettingsSection): Promise<void> => {
    if (!this.current() || !this.sectionVisible(section)) return Promise.resolve();
    if (section.scope === 'server' && this.forms.runtime && !this.headerLoading) this.headerLoading = (async () => {
      const [health, storage] = await Promise.allSettled([this.console.measurement('health'), this.console.measurement('storage')]);
      if (!this.current()) return;
      this.headerHealth=health.status === 'fulfilled' ? health.value : undefined;this.headerStorage=storage.status === 'fulfilled' ? storage.value : undefined;this.headerLoaded=true;
      this.header = settingsServerHeader(this.headerHealth,this.headerStorage);
      this.publish({});
    })();
    const formIds = new Set([...settingsRowIndex.values()].filter(location => location.section === section && this.eligible(location.row))
      .map(location => settingsBinding(location.row.id)?.form)
      .map(id => id === 'connectivityStatus' ? 'connectivity' : id)
      .filter((id): id is NonNullable<typeof id> => id !== undefined && !!this.forms[id]));
    const reads = [...formIds].map(id => ({id: `form/${id}`, load: async () => {const form = this.forms[id]!;await form.load();if (form.error()) throw form.error();}}));
    if (section.scope === 'server' && this.forms.runtime) reads.push({id: 'configuration', load: this.readConfiguration});
    if (section.scope === 'server' && section.id === 'storage' && this.forms.runtime) reads.push({id: 'backups', load: this.backups.load});
    return this.sectionReads.load(`${section.scope}/${section.id}`, reads);
  };
  quietInputs = (section: SettingsSection): readonly QuietInput[] => {
    if (!this.current() || !this.sectionVisible(section)) return [];
    const formIds = new Set([...settingsRowIndex.values()].filter(location => location.section === section && this.eligible(location.row))
      .map(location => settingsBinding(location.row.id)?.form).map(id => id === 'connectivityStatus' ? 'connectivity' : id)
      .filter((id): id is NonNullable<typeof id> => id !== undefined && !!this.forms[id]));
    const inputs: QuietInput[] = [...formIds].map(id => {
      const form = this.forms[id]!, key = `form/${id}`;
      return {id: key, cancel:form.invalidateQuiet, subscribe: this.subscribe, blocked: () => this.inputActive || this.writing || form.blocked(), settlement: form.settlement,
        refresh: () => this.sectionReads.hasRead(key) ? this.sectionReads.refresh(`${section.scope}/${section.id}`, [{id: key, refresh: form.refresh}]) : Promise.resolve({kind: 'deferred'})};
    });
    if (section.scope === 'server' && this.forms.runtime) {
      inputs.push({id:'configuration',cancel:()=>this.cancelQuietRead('configuration'),subscribe:this.subscribe,
        blocked:()=>this.inputActive || this.writing,settlement:()=>this.writeSerial,
        refresh:async()=>{
          if(!this.current() || this.inputActive || this.writing || !this.sectionReads.hasRead('configuration'))return {kind:'deferred'};
          return this.quietRead('configuration',(client,signal)=>this.options.readConfiguration?.(signal) ?? client.configuration(),configuration=>{this.configuration=configuration;},this.writeSerial);
        }});
      for(const name of ['health','storage'] as const)inputs.push({id:`header/${name}`,cancel:()=>this.cancelQuietRead(`header/${name}`),subscribe:this.subscribe,
        blocked:()=>!this.headerLoaded,
        refresh:async()=>{
          if(!this.current() || !this.headerLoaded)return {kind:'deferred'};
          return this.quietRead(`header/${name}`,client=>client.measurement(name),measurement=>{
            if(name==='health')this.headerHealth=measurement;else this.headerStorage=measurement;
            this.header=settingsServerHeader(this.headerHealth,this.headerStorage);
          });
        }});
    }
    if(section.scope==='server' && section.id==='libraries') {
      for(const [id,refresh] of [['selected-settings',this.library.refreshSettings],['selected-sonic',this.library.refreshSonic]] as const)inputs.push({
        id,refresh,cancel:this.library.invalidateQuiet,subscribe:this.subscribe,
        blocked:()=>this.inputActive || this.writing || this.library.quietBlocked(),settlement:()=>id==='selected-settings'?this.library.getSnapshot().document:this.library.getSnapshot().sonic,
      });
    }
    return inputs;
  };
  private preferenceScope(row: SettingsRow, key: string): PreferenceScope {
    const field = this.preferences.registry.fields.find(field => field.key === key);
    if (!field) throw new Error('settings_preference_unavailable');
    const location = settingsRowIndex.get(row.id);
    const scope = location?.section.id === 'reading' ? 'profile-server' : settingsBinding(row.id)?.scope;
    if (!scope || !field.scopes.includes(scope)) throw new Error('settings_preference_scope_unavailable');
    return scope;
  }
  private write = async (row: SettingsRow, value: SettingsValue, reset: boolean, context?: SettingsContext) => {
    const state = this.rowState(row, context);
    if (this.writing || !this.current() || !state.present || state.readOnly) return;
    ++this.writeSerial;this.cancelQuietRead('configuration');this.writing = true; this.rowErrors.delete(row.id); this.publish({saveState: 'saving'});
    try {
      if (row.id === 'storage.backupFolder' || row.id === 'storage.backupArtwork') {
        if (row.id === 'storage.backupFolder' && typeof value !== 'string' || row.id === 'storage.backupArtwork' && typeof value !== 'boolean') throw new Error('invalid_backup_setting');
        const saved = await this.backups.save(row.id === 'storage.backupFolder' ? {backupFolder: String(value)} : {includeArtwork: value === true});
        if (this.current()) this.publish({saveState: saved ? 'saved' : undefined});
        return;
      }
      if (this.libraryRow(context) && context?.recordId) {
        const saved = reset ? await this.library.reset(context.recordId, row.id) : await this.library.change(context.recordId, row.id, value);
        if (this.current()) this.publish({saveState: saved ? 'saved' : undefined});
        return;
      }
      const sets = row.control.options?.some(option => option.sets) ? presetPatch(settingsPresetOptions(row.control.options, {...this.options.environment, role: this.options.role}), value, id => {const row = settingsRowIndex.get(id)?.row; return row && this.read(row, context)?.current;}) : {[row.id]: value};
      const patches = new Map<SettingsBindingForm, Record<string, unknown>>(), preferencePatches = new Map<PreferenceScope, Record<string, unknown>>();
      const sources = this.sources();
      for (const [id, next] of Object.entries(sets)) {
        const target = settingsRowIndex.get(id)?.row, binding = settingsBinding(id);
        if (!target || !this.eligible(target, context)) throw new Error('settings_binding_unavailable');
        const location = settingsRowIndex.get(id)!;
        if (location.section.scope === 'account' && location.section.id === 'reading') {
          const patch = this.options.reading.patch(id, reset ? null : settingValue(next), this.preferences);
          for (const [key, value] of Object.entries(patch)) {
            const scope = this.preferenceScope(target, key);
            preferencePatches.set(scope, {...preferencePatches.get(scope), [key]: value});
          }
          continue;
        }
        if (!binding) throw new Error('settings_binding_unavailable');
        const patch = settingsBindingPatch(binding, next, sources);
        if (patch.form === 'preferences') {
          for (const [key, value] of Object.entries(patch.values)) {
            const scope = this.preferenceScope(target, key);
            preferencePatches.set(scope, {...preferencePatches.get(scope), [key]: reset ? null : value});
          }
        } else patches.set(patch.form, {...patches.get(patch.form), ...patch.values});
      }
      for (const [scope, values] of preferencePatches) {
        const document = this.preferences.documents.find(document => document.scope === scope);
        if (!document) throw new Error('settings_preference_document_unavailable');
        const patch = validatePreferencePatch(this.preferences.registry, scope, values as PreferencePatch);
        this.preferences = await this.preferencesReader.applyPreferences(scope, this.preferences.deviceClass, document.revision, patch);
        if (!this.current()) return;
      }
      for (const [id, patch] of patches) {
        if (id === 'device') {await writeDeviceSettingsBinding(this.options.device, Object.fromEntries(Object.entries(patch).map(([key, value]) => [key, settingValue(value)])), this.lifetime.signal, this.current); continue;}
        const form = this.forms[id]; if (!form) throw new Error('settings_document_unavailable');
        await form.patch(patch); if (!this.current()) return;
      }
      if ([...patches.keys()].some(id => id !== 'device')) await this.readConfiguration();
      this.publish({saveState: 'saved'});
    } catch (error) {
      if (this.current()) {this.rowErrors.set(row.id, error); this.publish({saveState: undefined}); this.options.reportError(error);}
    } finally {this.writing = false; this.publish({});}
  };
  change = (row: SettingsRow, value: SettingsValue, context?: SettingsContext) => {void this.write(row, value, false, context);};
  reset = (row: SettingsRow, context?: SettingsContext) => {const value = this.read(row, context)?.default; if (value !== undefined) void this.write(row, value as SettingsValue, true, context);};
  dispose = () => {
    this.closed = true;for(const id of this.quietClients.keys())this.cancelQuietRead(id); this.backups.dispose(); this.library.dispose(); this.folders.dispose(); this.sectionReads.dispose(); this.lifetime.abort(); this.offDevice?.(); this.console.dispose(); this.offPreferences();
    for (const off of this.offForms) off();
    for (const form of Object.values(this.forms)) form.dispose();
    this.configuration = undefined; this.header = {facts: []}; this.rowErrors.clear(); this.listeners.clear();
  };
}
