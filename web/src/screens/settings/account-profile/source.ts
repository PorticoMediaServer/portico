import type {QuietRefreshOutcome} from '@core/freshness-actions.ts';
import {routePath} from '../../../../../packages/contracts/src/routes.ts';
import {CodedError} from '@core/server-messages.ts';
import {IdentityClient, type IdentityUpload} from '@core/identity-client.ts';
import type {ProfileAvatar} from '@core/identity-restrictions.ts';
import {ManagedProfilesClient, parseDirectSnapshot, type DirectSnapshot, type ManagedProfile, type ProfileAPI, type ProfileArt, type ProfileDeletion, type ProfileRetirement} from '@core/profile-management.ts';

export type ProfileSettingsScope = Readonly<{serverId: string; accountId: string; profileId: string}>;
export type ProfileSettingsSnapshot = Readonly<{
  loading: boolean; busy: boolean; document?: DirectSnapshot; selectedId?: string; error?: unknown; avatars?: readonly ProfileAvatar[];
  /** The profile this session is watching as; the list marks it in use. */
  inUseId: string;
}>;
const empty: ProfileSettingsSnapshot = Object.freeze({loading: true, busy: false, inUseId: ''});

/** The server owns profiles even when the account signs in through Portico.
 * Keep the checked account document and its revisions together; never turn a
 * cached shell capability into permission to edit another profile. */
export class ProfileSettingsSource {
  private readonly client: ManagedProfilesClient;
  private readonly identity: IdentityClient;
  private readonly scope: ProfileSettingsScope;
  private readonly lifetime = new AbortController();
  private readonly listeners = new Set<() => void>();
  private snapshot: ProfileSettingsSnapshot = empty;
  private pending?: Promise<void>;
  private closed = false;
  private refreshSerial = 0;
  private inputActive = false;
  private quietRead?: AbortController;
  invalidateQuiet = () => {++this.refreshSerial;this.quietRead?.abort();this.quietRead=undefined;};
  setQuietInputActive = (active: boolean) => {
    if (active === this.inputActive || !this.current()) return;
    this.inputActive = active;this.invalidateQuiet();
    for (const listener of this.listeners) listener();
  };
  quietBlocked = () => this.inputActive || this.snapshot.busy || !!this.pending;

  constructor(private readonly api: ProfileAPI, scope: ProfileSettingsScope, private readonly isCurrent: () => boolean, upload?: (path: string, body: Parameters<IdentityUpload>[1], contentType: string, signal: AbortSignal) => Promise<unknown>, private readonly observeDirect?: (document: DirectSnapshot) => void) {
    this.scope = Object.freeze({...scope});
    this.snapshot = Object.freeze({...this.snapshot, inUseId: this.scope.profileId});
    this.client = new ManagedProfilesClient(api, {...scope, authority: 'local'});
    this.identity = new IdentityClient({request: (path, method, body, signal) => api.request(path, method, body, signal ?? this.lifetime.signal)}, upload ? (path, body, contentType) => upload(path, body, contentType, this.lifetime.signal) : undefined);
  }
  private current = () => !this.closed && this.isCurrent();
  getSnapshot = (): ProfileSettingsSnapshot => this.current() ? this.snapshot : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(snapshot: ProfileSettingsSnapshot) {
    if (!this.current()) return;
    this.snapshot = Object.freeze(snapshot);
    for (const listener of this.listeners) listener();
  }
  private async read(signal = this.lifetime.signal): Promise<DirectSnapshot> {
    const raw = await this.api.request(routePath('get_direct_account'), 'GET', undefined, AbortSignal.any([this.lifetime.signal,signal]));
    const document = parseDirectSnapshot(raw, {...this.scope, authority: 'local'});
    const guard = () => {if(!this.current() || signal.aborted || this.lifetime.signal.aborted)throw new DOMException('Profile read retired','AbortError');};
    guard();this.observeDirect?.(document);guard();
    const viewer = document.profiles.find(profile => profile.id === this.scope.profileId);
    if (!viewer || document.account.disabled || !viewer.primary && Object.values(document.capabilities).some(Boolean)) {
      throw new CodedError('forbidden', 'The selected profile is no longer available.');
    }
    return document;
  }
  refresh = async (): Promise<QuietRefreshOutcome> => {
    if (!this.current() || this.quietBlocked()) return {kind: 'deferred'};
    this.invalidateQuiet();const serial = this.refreshSerial, before = this.snapshot.document, read = this.quietRead = new AbortController();
    const owns = () => this.current() && !read.signal.aborted && serial === this.refreshSerial && !this.quietBlocked() && this.snapshot.document === before;
    try {
      const document = await this.read(read.signal);
      if (!owns()) return {kind: 'deferred'};
      const selectedId = document.profiles.some(profile => profile.id === this.snapshot.selectedId) ? this.snapshot.selectedId : this.scope.profileId;
      this.publish({...this.snapshot, loading: false, document, selectedId});
      return {kind: 'adopted'};
    } catch (error) {
      if (!owns()) return {kind: 'deferred'};
      const e=error as {code?: string;status?: number};
      if (e?.code==='forbidden' || e?.code==='unauthorized' || e?.status===401 || e?.status===403) this.publish({...this.snapshot,error});
      return {kind: 'unreadable', error};
    } finally {if(this.quietRead===read)this.quietRead=undefined;}
  };
  load = (): Promise<void> => {
    if (!this.current() || this.snapshot.document) return Promise.resolve();
    if (this.pending) return this.pending;
    this.invalidateQuiet();
    this.publish({loading: true, busy: false, inUseId: this.scope.profileId});
    const task = this.read().then(document => {
      this.publish({loading: false, busy: false, inUseId: this.scope.profileId, document, selectedId: this.scope.profileId});
    }, error => {this.publish({loading: false, busy: false, inUseId: this.scope.profileId, error});}).finally(() => {
      if (this.pending === task) this.pending = undefined;
    });
    this.pending = task;return task;
  };
  selected = (): ManagedProfile | undefined => {
    const state = this.getSnapshot();
    return state.document?.profiles.find(profile => profile.id === state.selectedId);
  };
  select = (id: string): void => {
    const state = this.getSnapshot(), document = state.document;
    if (!document || state.busy || !document.profiles.some(profile => profile.id === id) ||
      id !== this.scope.profileId && !document.canManage) return;
    this.invalidateQuiet();this.publish({...state, selectedId: id, error: undefined});
  };
  private requireManager(): DirectSnapshot {
    const document = this.getSnapshot().document;
    if (!document?.canManage || !document.capabilities.manageAccount ||
      !document.profiles.some(profile => profile.id === this.scope.profileId && profile.primary)) {
      throw new CodedError('forbidden', 'Profile management requires the main profile.');
    }
    return document;
  }
  private async mutate(command: (document: DirectSnapshot) => Promise<void>): Promise<void> {
    if (!this.current() || this.snapshot.busy || this.pending) throw new CodedError('conflict', 'Refresh the profiles before trying again.');
    const document = this.requireManager();
    this.invalidateQuiet();this.publish({...this.snapshot, busy: true, error: undefined});
    try {
      // Publication calls listeners synchronously. A listener may retire this viewer.
      if (!this.current()) return;
      await command(document);
      if (!this.current()) return;
      const refreshed = await this.read();
      const selectedId = refreshed.profiles.some(profile => profile.id === this.snapshot.selectedId) ? this.snapshot.selectedId : this.scope.profileId;
      this.publish({loading: false, busy: false, inUseId: this.scope.profileId, document: refreshed, selectedId});
    } catch (error) {
      // A failed write/readback must not leave old revisions available for retry.
      this.publish({loading: false, busy: false, inUseId: this.scope.profileId, error});
      throw error;
    }
  }
  rename = (name: string): Promise<void> => this.edit({name});
  color = (art: ProfileArt): Promise<void> => this.edit({art});
  loadPictures = async (): Promise<void> => {
    if (!this.current() || !this.snapshot.document || this.snapshot.avatars) return;
    try {
      const avatars = await this.identity.avatars(this.lifetime.signal);
      if (this.current() && this.snapshot.document) this.publish({...this.snapshot, avatars});
    } catch (error) {if (this.current()) this.publish({loading: false, busy: false, inUseId: this.scope.profileId, error});}
  };
  picture = (target: Pick<ManagedProfile, 'id' | 'revision'>, file?: File): Promise<void> => this.mutate(async document => {
    const profile = document.profiles.find(candidate => candidate.id === target.id && candidate.revision === target.revision);
    if (!profile) throw new CodedError('conflict', 'Refresh the profiles before trying again.');
    if (file) await this.identity.uploadAvatar(profile.id, file, file.type);
    else await this.identity.removeAvatar(profile.id);
  }).then(() => this.loadPictures());
  /** Bind a PIN dialog to the profile/revision it actually opened for. */
  setPIN = (target: Pick<ManagedProfile, 'id' | 'revision'>, pin: string): Promise<void> => this.mutate(async document => {
    const profile = document.profiles.find(candidate => candidate.id === target.id && candidate.revision === target.revision);
    if (!profile) throw new CodedError('conflict', 'Refresh the profiles before trying again.');
    await this.client.edit(profile, {pin}, this.lifetime.signal);
  });
  forgetDevices = (target: Pick<ManagedProfile, 'id' | 'revision'>): Promise<void> => this.mutate(async document => {
    const profile = document.profiles.find(candidate => candidate.id === target.id && candidate.revision === target.revision && candidate.pinRequired);
    if (!profile) throw new CodedError('conflict', 'Refresh the profiles before trying again.');
    await this.client.forgetTrust(profile.id, this.lifetime.signal);
  });
  previewDelete = async (target: Pick<ManagedProfile, 'id' | 'revision'>): Promise<ProfileDeletion> => {
    if (!this.current() || this.snapshot.busy || this.pending) throw new CodedError('conflict', 'Refresh the profiles before trying again.');
    const document = this.requireManager();
    const profile = document.profiles.find(candidate => candidate.id === target.id && candidate.revision === target.revision && !candidate.primary);
    if (!profile) throw new CodedError('conflict', 'Refresh the profiles before trying again.');
    const preview = await this.client.previewDelete(profile, this.lifetime.signal);
    if (!this.current()) throw new CodedError('forbidden', 'The selected profile is no longer available.');
    return Object.freeze({...preview});
  };
  deleteProfile = (preview: ProfileDeletion, onStatus?: (retirement: ProfileRetirement) => void): Promise<void> => this.mutate(async document => {
    if (!document.profiles.some(profile => profile.id === preview.profileId && !profile.primary) || preview.primary) {
      throw new CodedError('forbidden', 'The main profile cannot be deleted.');
    }
    // The accepted retirement is waited out here, so the row never disappears while the screen
    // still calls it pending; every reported state is handed to the dialog that opened it.
    await this.client.delete(preview, {signal: this.lifetime.signal, onStatus: retirement => {if (this.current()) onStatus?.(retirement);}});
  });
  private edit(change: {name?: string; art?: ProfileArt}): Promise<void> {
    const selectedId = this.getSnapshot().selectedId;
    return this.mutate(async document => {
      const profile = document.profiles.find(candidate => candidate.id === selectedId);
      if (!profile) throw new CodedError('not_found', 'This profile is no longer available.');
      await this.client.edit(profile, change, this.lifetime.signal);
    });
  }
  create = (name: string, art: ProfileArt): Promise<void> => this.mutate(async document => {
    if (document.profiles.length >= 8) throw new CodedError('conflict', 'This account already has eight profiles.');
    await this.client.create(name, art, this.lifetime.signal);
  });
  move = (id: string, direction: -1 | 1): Promise<void> => this.mutate(async document => {
    const profiles = [...document.profiles], from = profiles.findIndex(profile => profile.id === id), to = from + direction;
    if (from < 0 || to < 0 || to >= profiles.length) throw new CodedError('conflict', 'Refresh the profiles before trying again.');
    [profiles[from], profiles[to]] = [profiles[to]!, profiles[from]!];
    await this.client.order(profiles, this.lifetime.signal);
  });
  dispose = () => {
    if (this.closed) return;
    this.invalidateQuiet();this.closed = true;this.lifetime.abort();this.snapshot = empty;
    for (const listener of this.listeners) listener();
    this.listeners.clear();
  };
}
