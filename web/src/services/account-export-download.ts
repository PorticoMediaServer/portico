export type AccountExportDownloadState = Readonly<{
  phase: 'idle' | 'exporting' | 'pending' | 'downloaded' | 'failed';
  error?: unknown;
}>;
type ExportResult = {state: 'pending'} | {state: 'ready'; data: unknown};

/** One layout-owned export surface. A completed browser handoff belongs only to
 * its original account opening; a retired response cannot download or publish. */
export class AccountExportDownload {
  private readonly abort = new AbortController();
  private flight?: object;
  private live = true;
  private state: AccountExportDownloadState = Object.freeze({phase: 'idle'});
  private readonly listeners = new Set<() => void>();
  constructor(private readonly run: (signal: AbortSignal) => Promise<ExportResult>,
    private readonly handoff: (data: unknown, isCurrent: () => boolean) => void,
    private readonly isCurrent: () => boolean) {}
  current = () => {try {return this.live && !this.abort.signal.aborted && this.isCurrent();} catch {return false;}};
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: AccountExportDownloadState) {
    if (!this.current()) return;
    this.state = Object.freeze(state);
    for (const listener of this.listeners) {try {listener();} catch { /* A subscriber cannot replay the handoff. */ }}
  }
  download = async (expected: AccountExportDownloadState) => {
    if (this.flight || this.state !== expected) return;
    const flight = {};this.flight = flight;
    const owns = () => this.flight === flight && this.current();
    try {
      if (!owns()) return;
      this.publish({phase: 'exporting'});
      if (!owns()) return;
      const result = await this.run(this.abort.signal);
      if (!owns()) return;
      if (result.state === 'pending') this.publish({phase: 'pending'});
      else {
        this.handoff(result.data, owns);
        if (owns()) this.publish({phase: 'downloaded'});
      }
    } catch (error) {if (owns()) this.publish({phase: 'failed', error});}
    finally {if (this.flight === flight) this.flight = undefined;}
  };
  dispose = () => {this.live = false;this.abort.abort();this.listeners.clear();};
}

/** Success means the browser accepted its download action; web APIs cannot
 * observe the user's later filesystem/save decision. Always release our URL. */
export function handoffAccountExport(data: unknown, isCurrent: () => boolean) {
  if (!isCurrent()) throw new DOMException('Export opening retired', 'AbortError');
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], {type: 'application/json'}));
  try {
    if (!isCurrent()) throw new DOMException('Export opening retired', 'AbortError');
    const link = document.createElement('a');link.href = url;link.download = 'portico-account.json';link.hidden = true;
    document.body.append(link);
    try {link.click();} finally {link.remove();}
  } finally {setTimeout(() => URL.revokeObjectURL(url), 1000);}
}
