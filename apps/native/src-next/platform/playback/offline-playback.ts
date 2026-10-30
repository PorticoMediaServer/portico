import type {LocalSession, PlaybackApi, PlaybackSession} from '../../../../../packages/client-core/src/index';
import {serviceText} from '../../../../../packages/client-core/src/presentation/service-text';
import {localPlaybackFence, type LocalPlaybackFence, type LocalPlaybackScope} from '../../../../../packages/player-core/src';
import {LocalPlayback, type LocalMedia, type LocalProgress} from '../../../platform/local-playback';
import type {RememberedDownloadsViewer} from '../../shell/remembered-downloads-viewer';
import type {OfflineDownloads, OfflineEntry} from '../../../platform/offline-downloads';

/** Captured from the genuine installation session, never a server queue/controller scope. */
export type NativeOfflineOwner = LocalPlaybackScope;
export type NativeOfflineSource = Readonly<{
  owner: NativeOfflineOwner; entryId: string; itemId: string; preparationId: string;
  artifact: Readonly<{sha256: string; bytes: number}>; media: LocalMedia;
  metadata: Readonly<{title: string; kind?: string; libraryId?: string}>;
  session: () => PlaybackSession;
  /** Owner, artifact and receipt validity, independent of the screen that opened the file. */
  current: () => boolean;
}>;
export type NativeOfflinePlaybackDevice = Pick<OfflineDownloads, 'getSnapshot' | 'subscribe' | 'localMedia' | 'playabilityReason' | 'recordProgress'>;
const ownerKey = (owner: NativeOfflineOwner) => JSON.stringify([owner.authority, owner.serverId, owner.accountId, owner.profileId, owner.sessionFamilyId]);
const text = (value: unknown): value is string => typeof value === 'string' && value.length > 0 && value.length <= 512 && !/[\u0000-\u001f\u007f]/.test(value);
const unavailable = () => new Error(serviceText('downloads.offline.notPlayableHere'));

/** Admission and journal routing for the existing local playback path. This creates neither
 * another player nor another store. Only a verified current-owner artifact can supply a file. */
export class NativeOfflinePlayback {
  private readonly local: LocalPlayback;
  private readonly identity: (() => LocalSession | undefined) | RememberedDownloadsViewer;
  private readonly device: NativeOfflinePlaybackDevice;
  private readonly sessions = new Map<string, {source: NativeOfflineSource; media: LocalMedia; sessionId: string; generation: number; sequence: number; run?: number; journalCurrent: () => boolean}>();
  private disposed = false;
  constructor(identity: (() => LocalSession | undefined) | RememberedDownloadsViewer, device: NativeOfflinePlaybackDevice) {
    this.identity = identity;
    this.device = device;
    this.local = new LocalPlayback(progress => this.progress(progress));
  }
  owner(): NativeOfflineOwner | undefined {
    try {
    if (typeof this.identity !== 'function') return this.identity.current() ? this.identity.owner : undefined;
    const session = this.identity(), viewer = session?.viewer;
    if (!session || !viewer || viewer.authority !== 'local' && viewer.authority !== 'hosted'
      || ![viewer.serverId, viewer.accountId, viewer.profileId, session.sessionFamilyId].every(text)) return;
    return Object.freeze({authority: viewer.authority, serverId: viewer.serverId, accountId: viewer.accountId, profileId: viewer.profileId, sessionFamilyId: session.sessionFamilyId});
    } catch {return undefined;}
  }
  /** A closed local transport. Ordinary create/progress/stop can never reach a server.
   * PlaybackService.playLocal explicitly uses only the admitted journal owner. */
  localOnlyApi(): PlaybackApi {
    const refuse = () => Promise.reject(unavailable());
    return this.wrap({createPlayback: refuse, progressPlayback: refuse, stopPlayback: refuse});
  }
  wrap(api: PlaybackApi): ReturnType<LocalPlayback['wrap']> {
    const wrapped = this.local.wrap(api);
    const progressPlayback: PlaybackApi['progressPlayback'] = (id, payload) => {
      // A retry keeps one journal play but creates a new real local session.
      // Its retired session must not overwrite the new session's observation.
      const held = [...this.sessions.values()].find(value => value.sessionId === id && value.generation === payload.generation);
      if (!held || !held.journalCurrent() || this.disposed && payload.state === 'playing'
        || !Number.isSafeInteger(payload.sequence) || payload.sequence <= held.sequence) return Promise.resolve();
      held.sequence = payload.sequence;
      return wrapped.localPlayback.progressPlayback(id, payload);
    };
    return Object.assign({...wrapped},
    {localPlayback: Object.freeze({progressPlayback, stopPlayback: wrapped.localPlayback.stopPlayback})});
  }
  /** Resolve only the native file already admitted for this exact current local
   * session. A file-looking URL is not itself permission to load a file. */
  mediaUrl(session: PlaybackSession): string | undefined {
    const held = this.admitted(session);
    return held?.media.fileUrl;
  }
  private admitted(session: PlaybackSession) {
    const held = [...this.sessions.values()].find(value => value.sessionId === session.id && value.generation === session.generation);
    return held && session.mode === 'local' && session.streamUrl === held.media.fileUrl && held.source.current() ? held : undefined;
  }
  /** Only an actual admitted session can publish the local identity used by the
   * existing dock and controls. Paths and receipt payloads stay app-private. */
  fence(session: PlaybackSession, run: number): LocalPlaybackFence | undefined {
    const held = this.admitted(session);
    if (!held || held.run !== undefined && held.run !== run) return;
    const {source, media} = held;
    try {
      const fence = localPlaybackFence({kind: 'local', playback: {kind: 'local', scope: source.owner, run},
        sessionId: session.id, generation: session.generation, itemId: source.itemId,
        entryId: source.entryId, playId: media.playId, artifact: source.artifact});
      // A following Play increments the service run before retiring its old
      // session. That must never relabel this admitted source as the new play.
      held.run = run;
      return fence;
    } catch { return; }
  }
  /** A parked bookmark reopens its actual device entry and journal play. Neither
   * the old URL nor a structurally similar source object can authorize it. */
  async reopen(source: NativeOfflineSource, isCurrent: () => boolean): Promise<NativeOfflineSource> {
    if (!isCurrent() || !source.current() || this.sessions.get(source.media.playId)?.source !== source) throw unavailable();
    const entries = this.device.getSnapshot().entries.filter(entry => entry.id === source.entryId);
    if (entries.length !== 1) throw unavailable();
    const fresh = await this.prepare(entries[0]!, isCurrent);
    if (!isCurrent() || !source.current() || fresh.preparationId !== source.preparationId
      || fresh.artifact.sha256 !== source.artifact.sha256 || fresh.artifact.bytes !== source.artifact.bytes
      || fresh.metadata.kind !== source.metadata.kind) throw unavailable();
    return fresh;
  }
  async prepare(entry: OfflineEntry, isCurrent: () => boolean): Promise<NativeOfflineSource> {
    const owner = this.owner(), claims = entry.receipt?.claims;
    if (this.disposed || !owner || !claims || !text(entry.id) || !text(entry.itemId) || claims.itemId !== entry.itemId
      || !text(claims.preparationId) || !/^[a-f0-9]{64}$/.test(claims.artifact.sha256)
      || !Number.isSafeInteger(claims.artifact.bytes) || claims.artifact.bytes < 1) throw unavailable();
    const artifact = Object.freeze({...claims.artifact}), preparationId = claims.preparationId;
    const currentEntry = (): OfflineEntry | undefined => {
      try {
      const currentOwner = this.owner();
      if (!currentOwner || ownerKey(currentOwner) !== ownerKey(owner)) return;
      const matches = this.device.getSnapshot().entries.filter(value => value.id === entry.id);
      if (matches.length !== 1) return;
      const live = matches[0]!, receipt = live.receipt?.claims;
      if (!receipt || live.itemId !== entry.itemId || receipt.itemId !== entry.itemId || receipt.preparationId !== preparationId
        || receipt.artifact.sha256 !== artifact.sha256 || receipt.artifact.bytes !== artifact.bytes
        || (['authority', 'serverId', 'accountId', 'profileId'] as const).some(key => receipt.viewer[key] !== owner[key])
        || this.device.playabilityReason(live) !== null) return;
      return live;
      } catch {return undefined;}
    };
    const before = currentEntry();
    if (!isCurrent() || !before) throw unavailable();
    const media = Object.freeze({...await this.device.localMedia(before)}), live = currentEntry();
    if (this.disposed || !isCurrent() || !live || media.entryId !== entry.id || media.itemId !== entry.itemId || !text(media.playId)
      || !Number.isFinite(media.durationSeconds) || media.durationSeconds < 0 || !Number.isFinite(media.startSeconds) || media.startSeconds < 0) throw unavailable();
    let url: URL;
    try { url = new URL(media.fileUrl); } catch { throw unavailable(); }
    if (url.protocol !== 'file:' || url.hostname !== '' || !url.pathname.startsWith('/') || url.search || url.hash) throw unavailable();
    const make = this.local.session(media);
    const metadata = Object.freeze({title: live.title, kind: live.kind ?? live.info?.kind, libraryId: live.libraryId});
    const source: NativeOfflineSource = Object.freeze({owner, entryId: entry.id, itemId: entry.itemId, preparationId, artifact, media, metadata,
      current: () => !this.disposed && !!currentEntry(),
      session: () => {
        if (this.disposed || !isCurrent() || !currentEntry()) throw unavailable();
        const session = make();
        this.sessions.set(media.playId, {source, media, sessionId: session.id, generation: session.generation, sequence: 0, journalCurrent: () => !!currentEntry()});
        while (this.sessions.size > 16) this.sessions.delete(this.sessions.keys().next().value!);
        return session;
      },
    });
    return source;
  }
  private progress(progress: LocalProgress): void {
    const held = this.sessions.get(progress.playId);
    if (!held || held.media.entryId !== progress.entryId || held.media.itemId !== progress.itemId || !held.journalCurrent()) return;
    this.device.recordProgress(progress);
  }
  // PlaybackService may still flush its already-captured final checkpoint. Keep
  // its bounded private mappings until that service is released; admission and
  // engine resolution retire immediately, and owner/receipt fences still apply.
  dispose(): void { this.disposed = true; }
}
