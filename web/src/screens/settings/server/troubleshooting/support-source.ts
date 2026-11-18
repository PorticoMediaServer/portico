import type {HttpLocalApi} from '@core/index.ts';
import {fixOwnerSupportPermissions, prepareOwnerSupportExport, readOwnerSupport, type OwnerSupportFacts, type OwnerSupportOptions} from '@core/server-admin/support-next.ts';
import {skippedParts, type SkippedPart} from '@core/tolerant.ts';
export {SUPPORT_COMPONENTS, type SupportComponent} from '@core/server-admin/support-next.ts';

export type SupportSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  /** What this server can do (`GET /v1/admin/capabilities`) and the data folder's permissions. */
  facts?: OwnerSupportFacts;
  /** What this app left out since it started, counted by document and part. */
  skipped: readonly SkippedPart[];
  preparing: boolean;
  prepareError?: boolean;
  /** The file name of the export this visit downloaded. */
  downloaded?: string;
  fixing: boolean;
  fixed: boolean;
  error?: unknown;
}>;
export type SupportApi = Pick<HttpLocalApi, 'request' | 'downloadSupportExport'>;
/** Saves the ZIP the owner asked for; the browser's own download. */
export type SaveFile = (blob: Blob, name: string) => void;

const empty: SupportSnapshot = Object.freeze({phase: 'idle', skipped: Object.freeze([]), preparing: false, fixing: false, fixed: false});
const terminal = (error: unknown) => {const status = (error as {status?: unknown} | null)?.status;return status === 401 || status === 403;};

export const saveInBrowser: SaveFile = (blob, name) => {
  const url = URL.createObjectURL(blob), link = document.createElement('a');
  try {link.href = url;link.download = name;link.click();} finally {setTimeout(() => URL.revokeObjectURL(url), 0);}
};

/**
 * Troubleshooting › Support on the rebuilt server (definition 12 rules 74 to 76): what this server can do,
 * the data folder's permissions with Fix permissions, and a support export that is made when asked and
 * downloaded at once (`POST /v1/admin/support-export`); nothing is stored on the server, so there is no
 * export to fetch again later.
 */
export class SettingsSupportSource {
  private state: SupportSnapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private reading?: Promise<void>;
  constructor(private readonly api: SupportApi, private readonly isCurrent: () => boolean, private readonly reportError?: (error: unknown) => void, private readonly save: SaveFile = saveInBrowser) {}
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  getSnapshot = (): SupportSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<SupportSnapshot>) {if (this.current()) {this.state = Object.freeze({...this.state, ...patch});for (const listener of [...this.listeners]) listener();}}
  /** A signed-out answer or a lost role ends this part and is the page's to show. */
  private failed(error: unknown): boolean {
    if (!terminal(error) || !this.reportError) return false;
    this.dispose();this.reportError(error);return true;
  }
  load = (): Promise<void> => {
    if (!this.current() || this.reading || this.state.phase === 'ready') return this.reading ?? Promise.resolve();
    this.publish({phase: 'loading', error: undefined});
    const task = readOwnerSupport(this.api, this.lifetime.signal).then(
      facts => this.publish({phase: 'ready', facts, skipped: skippedParts()}),
      error => {if (this.current() && !this.failed(error)) this.publish({phase: 'error', error});},
    ).finally(() => {this.reading = undefined;});
    this.reading = task;return task;
  };
  retry = (): Promise<void> => {this.publish({phase: 'idle', error: undefined});return this.load();};
  /** Make the export over the chosen window and parts, and download it. */
  async prepare(options: OwnerSupportOptions): Promise<void> {
    if (!this.current() || this.state.preparing) return;
    this.publish({preparing: true, prepareError: false, downloaded: undefined});
    try {
      const made = await prepareOwnerSupportExport(this.api, options, this.lifetime.signal);
      if (!this.current()) return;
      const name = `portico-support-${new Date().toISOString().slice(0, 10)}.zip`;
      this.save(made.blob, name);
      this.publish({preparing: false, downloaded: name});
    } catch (error) {
      if (this.current() && !this.failed(error)) this.publish({preparing: false, prepareError: true});
    }
  }
  /** Fix permissions, offered only while the server says other accounts can read the data folder and it can fix that. */
  fixPermissions = async (): Promise<void> => {
    if (!this.current() || this.state.fixing) return;
    this.publish({fixing: true});
    try {
      const permissions = await fixOwnerSupportPermissions(this.api, this.lifetime.signal);
      if (this.current() && this.state.facts) this.publish({fixing: false, fixed: true, facts: Object.freeze({...this.state.facts, permissions})});
    } catch (error) {
      if (this.current() && !this.failed(error)) this.publish({fixing: false, error});
    }
  };
  dispose(): void {if (!this.lifetime.signal.aborted) {this.lifetime.abort();this.listeners.clear();this.state = empty;}}
}
