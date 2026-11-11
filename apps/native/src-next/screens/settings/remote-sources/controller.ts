import {
  addCloudTokenSource, addRcloneRemoteFromFields, addRemoteFolderToLibrary, addRemoteSource, changeRemoteSource, createLibraryOnRemoteFolder,
  listAdminLibraries, listRemoteSources, remoteSourceFolders, removeRemoteSource, startDriveSignIn, testRemoteSource,
  type RemoteSource, type RemoteSourceCreate, type RemoteSourceEdit,
} from '../../../../../../packages/client-core/src/remote-storage';
import type {ServerRequester} from '../../../../../../packages/client-core/src/query/server-api';
import {NativeScreenLifetime, NativeScreenStore, screenScopeEnded} from '../../settings-live/lifetime';
import type {NativeSettingsLivePorts} from '../../settings-live/ports';

function terminalRefusal(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false;
  const refusal = error as {status?: unknown; code?: unknown};
  return refusal.status === 401 || refusal.status === 403 || refusal.code === 'unauthorized' || refusal.code === 'forbidden';
}

export type NativeRemoteSourcesSnapshot = Readonly<{allowed: boolean; loading: boolean; sources: readonly RemoteSource[]; error?: unknown}>;

/**
 * The owner's remote sources on the new server (definition 03, rules 7 to 14). Only the owner of this server, signed in
 * on its main profile, is admitted (rule 7); a refusal of sign-in or permission retires the screen. Every call goes
 * through client-core's `remote-storage`; credential fields stay in the call, never in a snapshot.
 */
export class NativeRemoteSourcesController extends NativeScreenStore<NativeRemoteSourcesSnapshot> {
  private readonly lifetime: NativeScreenLifetime;
  private readonly ports: NativeSettingsLivePorts;
  private readonly viewportCurrent: () => boolean;
  private offAuthority: () => void = () => {};
  private disposed = false;

  constructor(ports: NativeSettingsLivePorts, viewportCurrent: () => boolean = () => true) {
    const lifetime = new NativeScreenLifetime(ports.context);
    super({allowed: false, loading: true, sources: []});
    this.ports = ports; this.viewportCurrent = viewportCurrent; this.lifetime = lifetime;
    this.publish({allowed: this.allowed(), loading: this.allowed(), sources: []});
    this.offAuthority = ports.context.subscribeAuthority(() => {if (!this.allowed()) this.dispose();});
  }

  private allowed(): boolean {
    const account = this.ports.context.account;
    return !this.disposed && this.viewportCurrent() && this.lifetime.current() && account.canManage && !account.account.disabled
      && account.account.role === 'owner' && account.serverId === this.lifetime.authority.serverId
      && account.account.id === this.lifetime.authority.accountId && account.account.primaryProfileId === this.lifetime.authority.profileId;
  }
  /** Runs one client-core call for this screen, with the screen's own abort: refused before it starts and after it answers
   * once the owner is no longer the one signed in here; a refused sign-in or permission retires the screen. The list is
   * read again after any change. */
  private async call<T>(work: (api: ServerRequester, signal: AbortSignal) => Promise<T>, reload = true): Promise<T> {
    if (!this.allowed()) {this.dispose(); throw screenScopeEnded();}
    const result = await this.lifetime.request(async signal => {
      try { return await work(this.ports.context.api, signal); }
      catch (error) { if (terminalRefusal(error)) this.dispose(); throw error; }
    });
    if (!this.allowed()) {this.dispose(); throw screenScopeEnded();}
    if (reload) void this.load();
    return result;
  }

  async load(): Promise<void> {
    if (!this.allowed()) {this.dispose(); return;}
    this.publish({...this.getSnapshot(), loading: true});
    try {
      const sources = await this.call((api, signal) => listRemoteSources(api, signal), false);
      if (this.allowed()) this.publish({allowed: true, loading: false, sources});
    } catch (error) {
      if (this.allowed()) this.publish({...this.getSnapshot(), loading: false, error});
    }
  }
  /** Tests the connection and saves the source only if it works (rule 10). */
  add(source: RemoteSourceCreate) { return this.call((api, signal) => addRemoteSource(api, source, signal)); }
  addCloudToken(source: Parameters<typeof addCloudTokenSource>[1]) { return this.call((api, signal) => addCloudTokenSource(api, source, signal)); }
  addRcloneFromFields(source: Parameters<typeof addRcloneRemoteFromFields>[1]) { return this.call((api, signal) => addRcloneRemoteFromFields(api, source, signal)); }
  change(id: string, edit: RemoteSourceEdit) { return this.call((api, signal) => changeRemoteSource(api, id, edit, signal)); }
  test(id: string) { return this.call((api, signal) => testRemoteSource(api, id, signal)); }
  remove(id: string) { return this.call((api, signal) => removeRemoteSource(api, id, signal)); }
  folders(id: string, path: string) { return this.call((api, signal) => remoteSourceFolders(api, id, path, signal), false); }
  startDriveSignIn(clientId: string, clientSecret: string) { return this.call((api, signal) => startDriveSignIn(api, clientId, clientSecret, signal), false); }
  libraries() { return this.call((api, signal) => listAdminLibraries(api, signal), false); }
  /** Continue setup: a folder of the source into an existing library, or a new one made from it. */
  putFolderInLibrary(source: RemoteSource, path: string, library: Readonly<{id: string}> | Readonly<{name: string; kind: string}>) {
    const folder = {source: source.id, path};
    return this.call((api, signal) => 'id' in library ? addRemoteFolderToLibrary(api, library.id, folder, signal) : createLibraryOnRemoteFolder(api, library, folder, signal));
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.offAuthority();
    this.lifetime.dispose();
    this.publish({allowed: false, loading: false, sources: []});
  }
}
