import type {Backups, BackupInspection, RestoreStaged} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';
import type {BackupSummary} from '../administration.ts';

/**
 * Storage & backups on the rebuilt server, for the owner. `GET /v1/admin/backups` lists the backups, the
 * one being made, the folder and a restore waiting for a restart. A restore is checked whole, needs the
 * owner's password again (and a two-step code where they use one), is made at the next start, and a
 * backup this server did not make needs a typed confirmation first. The server restarts when the owner
 * says so; everyone signs in again afterwards.
 */
export type {BackupInspection};
export type RestoreTarget = Readonly<{id: string} | {path: string}>;
export type RestoreProof = Readonly<{password: string; code?: string; notMadeHereConfirmed?: boolean}>;
/** The owner's `backups` settings document: the schedule, how many are kept, the folder and artwork. */
export type BackupSettings = Readonly<{days: readonly number[]; hour: number; keep: number; folder: string; includeArtwork: boolean}>;
export type ServerBackupSnapshot = Readonly<{backups?: Backups; settings?: BackupSettings; list: readonly BackupSummary[]; staged?: RestoreStaged; restarting?: boolean; busy: boolean}>;

const empty: ServerBackupSnapshot = Object.freeze({list: Object.freeze([]), busy: false});
const ended = () => Object.assign(new Error('The backup scope ended.'), {name: 'AbortError'});
/** Signed out, or no longer the owner. A wrong password or two-step code on a restore is neither. */
export function scopeEnded(error: unknown): boolean {
  const e = error as {status?: unknown; code?: unknown} | null;
  return e?.status === 401 || e?.status === 403 && e.code !== 'current_password_incorrect' && e.code !== 'two_factor_invalid';
}
function backupSettings(values: unknown): BackupSettings {
  const v = (values ?? {}) as Record<string, unknown>;
  const whole = (n: unknown, fallback: number) => typeof n === 'number' && Number.isSafeInteger(n) ? n : fallback;
  return Object.freeze({days: Object.freeze(Array.isArray(v.days) ? v.days.filter((d): d is number => typeof d === 'number') : []), hour: whole(v.hour, 3), keep: whole(v.keep, 7),
    folder: typeof v.folder === 'string' ? v.folder : '', includeArtwork: v.includeArtwork === true});
}
const target = (t: RestoreTarget) => 'id' in t ? {id: t.id} : {path: t.path};

/** A listed backup in the shape the backup list draws. */
export function backupSummary(b: Backups['items'][number]): BackupSummary {
  return Object.freeze({id: b.id, createdAt: b.createdAt, kind: b.scheduled ? 'scheduled' : 'manual', schemaVersion: 0, serverVersion: b.version, bytes: b.size, path: ''});
}

/** A refusal that says this backup was not made here: the page shows the server's warning and asks for the typed word. */
export function notMadeHere(error: unknown): string | undefined {
  const e = error as {status?: unknown; code?: unknown; message?: unknown} | null;
  return e?.status === 409 && e.code === 'backup_not_made_here' ? (typeof e.message === 'string' ? e.message : '') : undefined;
}

export class ServerBackupSource {
  private state = empty;
  private readonly lifetime = new AbortController();
  constructor(private readonly api: ServerRequester, private readonly isCurrent: () => boolean, private readonly notify: () => void,
    private readonly pollMs = 2000) {}
  private current = () => !this.lifetime.signal.aborted && this.isCurrent();
  getSnapshot = () => this.current() ? this.state : empty;
  private publish(next: Partial<ServerBackupSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...next});this.notify();}}
  private check() {if (!this.current()) throw ended();}
  /** A signed-out answer ends this source; the shell opens sign-in. */
  private async run<T>(work: (signal: AbortSignal) => Promise<T>, signal?: AbortSignal): Promise<T> {
    this.check();
    const request = signal ? AbortSignal.any([signal, this.lifetime.signal]) : this.lifetime.signal;
    try {const value = await work(request);this.check();return value;}
    catch (error) {if (scopeEnded(error)) {this.dispose();this.notify();}throw error;}
  }

  load = (signal?: AbortSignal): Promise<void> => this.run(async s => {
    const [backups, doc] = await Promise.all([callServer(this.api, 'GET /v1/admin/backups', {signal: s}), callServer(this.api, 'GET /v1/admin/settings/{name}', {id: 'backups', signal: s})]);
    this.check();
    this.publish({backups, settings: backupSettings(doc.values), list: Object.freeze(backups.items.map(backupSummary)), ...(backups.pendingRestore ? {} : {staged: undefined})});
  }, signal);

  /** Saves the fields given; the others keep their value. A new folder is used by the next backup. */
  async saveSettings(change: Partial<BackupSettings>): Promise<void> {
    await this.command(s => callServer(this.api, 'PUT /v1/admin/settings/{name}', {id: 'backups', body: change, signal: s}));
  }
  setFolder = (folder: string) => this.saveSettings({folder});

  /** Starts a backup and follows it until it is listed. */
  async backupNow(): Promise<void> {
    await this.command(async s => {
      await callServer(this.api, 'POST /v1/admin/backups', {signal: s});
      await this.load(s);
      while (this.state.backups?.running) {
        await new Promise(resolve => setTimeout(resolve, this.pollMs));
        await this.load(s);
      }
    });
  }

  async remove(id: string, signal?: AbortSignal): Promise<void> {
    await this.command(s => callServer(this.api, 'DELETE /v1/admin/backups/{id}', {id, signal: s}), signal);
  }

  /** What a backup holds and what restoring it does; a damaged one is refused here. */
  inspect(t: RestoreTarget, signal?: AbortSignal): Promise<BackupInspection> {
    return this.run(s => callServer(this.api, 'POST /v1/admin/backups/inspect', {body: target(t), signal: s}), signal);
  }

  /** Stages the restore for the next start. It throws `backup_not_made_here` until that is confirmed. */
  async restore(t: RestoreTarget, proof: RestoreProof, signal?: AbortSignal): Promise<RestoreStaged> {
    let staged!: RestoreStaged;
    await this.command(async s => {
      staged = await callServer(this.api, 'POST /v1/admin/backups/restore', {body: {...target(t), currentPassword: proof.password,
        ...(proof.code ? {code: proof.code} : {}), ...(proof.notMadeHereConfirmed ? {notMadeHereConfirmed: true} : {})}, signal: s});
      this.publish({staged});
    }, signal);
    return staged;
  }

  /** Drops a restore that waits for a restart. */
  async cancelRestore(): Promise<void> {
    await this.command(async s => {await callServer(this.api, 'DELETE /v1/admin/backups/restore', {signal: s});this.publish({staged: undefined});});
  }

  /** Restarts the server now, which makes the waiting restore. Everyone signs in again afterwards. */
  async restart(): Promise<boolean> {
    let restarting = false;
    await this.command(async s => {
      const out = await callServer(this.api, 'POST /v1/admin/restart', {query: {confirm: true}, signal: s});
      restarting = out.restarting;
      this.publish({restarting});
    }, undefined, false);
    return restarting;
  }

  /** One command at a time; the list is read again after each. */
  private async command(work: (signal: AbortSignal) => Promise<unknown>, signal?: AbortSignal, reload = true): Promise<void> {
    if (this.state.busy) throw Object.assign(new Error('Another backup action is under way.'), {code: 'backup_busy'});
    this.publish({busy: true});
    try {
      await this.run(work, signal);
      if (reload) await this.load(signal).catch(() => undefined);
    } finally {this.publish({busy: false});}
  }

  dispose() {this.lifetime.abort();this.state = empty;}
}
