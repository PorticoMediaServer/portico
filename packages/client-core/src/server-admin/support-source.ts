import {linkAbortSignals, throwIfAborted} from '../abort-lifetime.ts';
import {ConsoleClient, type ConsoleAPI, type SupportExport} from '../console.ts';
import {isStatePermissionsAlert} from '../administration.ts';
import {fixStatePermissions} from '../backups.ts';
import {parseCapabilities, type ServerCapability} from './capabilities.ts';
import {skippedParts, type SkippedPart} from '../tolerant.ts';
import {randomId} from '../random-id.ts';
import {routePath} from '../../../contracts/src/route-paths.ts';

export type SupportSnapshot = Readonly<{
  phase: 'idle' | 'loading' | 'ready' | 'error';
  capabilities: readonly ServerCapability[];
  /** The live permissions warning the server reports, if any. */
  permissions: boolean;
  /** What this app left out since it started, counted by document and part. */
  skipped: readonly SkippedPart[];
  preparing: boolean;
  supportsPaths?: boolean;
  pendingExport?: Readonly<{windowMs:number;components:readonly SupportComponent[];includePaths:boolean}>;
  prepareError?: boolean;
  error?: unknown;
  export?: SupportExport;
  fixed: boolean;
}>;

const empty: SupportSnapshot = Object.freeze({phase: 'idle', capabilities: Object.freeze([]), permissions: false, skipped: Object.freeze([]), preparing: false, fixed: false});
/** The four parts the drawn export offers; the server decides what each one holds. */
export const SUPPORT_COMPONENTS = Object.freeze(['runtime', 'client', 'audit', 'snapshot'] as const);
export type SupportComponent = typeof SUPPORT_COMPONENTS[number];

/**
 * Owner-scoped Support: what this server can do, the permissions warning, the redacted
 * support export, and the parts this app could not read. Reads go through the same console
 * client and captured viewer fence as the rest of Settings.
 */
export class SettingsSupportSource {
  private state: SupportSnapshot = empty;
  private readonly listeners = new Set<() => void>();
  private readonly lifetime = new AbortController();
  private reading?: Promise<void>;
  private readonly console: ConsoleClient;
  private fixing?: Promise<boolean>;
  private fixOperation?: string;
  private preparing?: object;
  private exportIntent?: Readonly<{from:number;to:number;windowMs:number;components:readonly SupportComponent[];includePaths?:boolean}>;
  private readonly transport: ConsoleAPI;
  constructor(transport: ConsoleAPI, private readonly serverId: string, viewerId: string, private readonly isCurrent: () => boolean, private readonly reportError?: (error:unknown)=>void, private readonly operationId:()=>string=randomId) {
    const guarded = async <T>(consoleRequest: boolean, path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
      this.assertCurrent();
      const lifetime = linkAbortSignals([signal, this.lifetime.signal]);
      try {
        throwIfAborted(lifetime.signal);
        const value = await (consoleRequest && transport.requestConsole ? transport.requestConsole<T>(path, method, body, lifetime.signal) : transport.request<T>(path, method, body, lifetime.signal));
        this.assertCurrent();throwIfAborted(lifetime.signal);return value;
      } finally {lifetime.dispose();}
    };
    this.transport = {request: <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => guarded<T>(false, path, method, body, signal),
      ...(transport.requestConsole ? {requestConsole: <T>(path: string, method: string, body: unknown, signal: AbortSignal) => guarded<T>(true, path, method, body, signal)} : {})};
    this.console = new ConsoleClient(this.transport, {serverId, viewerId});
    this.console.onDenied(()=>{const error=Object.assign(new Error('The support authority ended.'),{status:403});if(this.reportError){this.dispose();this.reportError(error);}else this.publish({...empty,phase:'error',error});});
  }
  private current() {return !this.lifetime.signal.aborted && this.isCurrent();}
  private assertCurrent() {if (!this.current()) throw Object.assign(new Error('The support scope ended.'), {name: 'AbortError'});}
  canDownload = (id: string) => this.current() && this.state.export?.id === id && this.state.export.expiresAt > Date.now();
  getSnapshot = (): SupportSnapshot => this.current() ? this.state : empty;
  subscribe = (listener: () => void): (() => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: SupportSnapshot) {if (this.current()) {this.state = Object.freeze(state);for (const listener of [...this.listeners]) listener();}}
  load = (): Promise<void> => {
    if (!this.current() || this.reading) return this.reading ?? Promise.resolve();
    const task = Promise.resolve().then(async () => {
      if (!this.current()) return;
      this.publish({...this.state, phase: 'loading', error: undefined});
      try {
        const [capabilities, alerts, registry] = await Promise.all([
          this.transport.request<unknown>(routePath('get_capability_report'), 'GET', undefined, this.lifetime.signal).then(parseCapabilities),
          this.console.alerts(),
          this.console.registry(),
        ]);
        if (!this.current()) return;
        this.publish({...this.state, phase: 'ready', capabilities, supportsPaths: registry.exportOptions?.includePaths === true, permissions: alerts.some(alert => isStatePermissionsAlert(alert)), skipped: skippedParts()});
      } catch (error) {if (this.current()) {const failure=(error&&typeof error==='object'?error:{}) as {status?:number};if(this.reportError&&(failure.status===401||failure.status===403)){this.dispose();this.reportError(error);}else this.publish({...this.state,phase:'error',error});}}
      finally {this.reading = undefined;}
    });
    this.reading = task;return task;
  };
  /** Prepare the redacted bundle the drawn page offers, over the window and parts chosen. */
  async prepare(windowMs: number, components: readonly SupportComponent[], includePaths = false): Promise<void> {
    if (!this.current() || this.preparing) return;
    let intent = this.exportIntent;
    if (!intent) {
      if (typeof includePaths !== 'boolean' || includePaths && !this.state.supportsPaths || !Number.isFinite(windowMs) || windowMs <= 0 || !components.length || components.some(part => !SUPPORT_COMPONENTS.includes(part))) {this.publish({...this.state,prepareError:true});return;}
      const to = Date.now();intent = Object.freeze({from:to-windowMs,to,windowMs,components:Object.freeze([...new Set(components)]),...(this.state.supportsPaths?{includePaths}:{})});this.exportIntent = intent;
    }
    const attempt = {};this.preparing = attempt;
    const owns = () => this.current() && this.preparing === attempt && this.exportIntent === intent;
    this.publish({...this.state,preparing:true,prepareError:false,pendingExport:Object.freeze({windowMs:intent.windowMs,components:intent.components,includePaths:intent.includePaths === true})});
    try {
      if (!owns()) return;
      const ready = await this.console.createExport(intent.from,intent.to,[...intent.components],intent.includePaths);
      if (!owns()) return;
      if ((ready.manifest.includePaths ?? false) !== (intent.includePaths ?? false) || ready.manifest.from !== intent.from || ready.manifest.to !== intent.to || JSON.stringify([...ready.manifest.included].sort()) !== JSON.stringify([...intent.components].sort())) throw new Error('Export receipt does not match its captured request.');
      this.exportIntent = undefined;this.publish({...this.state,preparing:false,pendingExport:undefined,prepareError:false,export:ready});
    } catch (error) {
      if (!owns()) return;
      const failure = (error && typeof error === 'object' ? error : {}) as {status?:number;code?:string};
      if (failure.status === 401 || failure.status === 403 || ['unauthorized','forbidden','not_permitted'].includes(failure.code ?? '')) {this.dispose();throw error;}
      if (!this.console.hasUnconfirmed()) this.exportIntent = undefined;
      this.publish({...this.state,preparing:false,prepareError:true,...(!this.exportIntent?{pendingExport:undefined}:{})});
    } finally {if (this.preparing === attempt) {this.preparing = undefined;this.publish({...this.state,preparing:false});}}
  }
  /** The prepared bundle itself, for the download the owner asked for. */
  async download(): Promise<SupportExport> {
    const ready = this.state.export;
    if (!this.current() || !ready) throw new Error('No prepared export.');
    const bundle = await this.console.export(ready.id);
    this.assertCurrent();
    if (bundle.id !== ready.id || (bundle.manifest.includePaths ?? false) !== (ready.manifest.includePaths ?? false)) throw new Error('The export receipt changed.');
    if (!this.canDownload(ready.id)) throw Object.assign(new Error('The prepared export changed.'), {name: 'AbortError'});
    return bundle;
  }
  fixPermissions = (): Promise<boolean> => {
    if (!this.current()) return Promise.resolve(false);
    if (this.fixing) return this.fixing;
    const operation = this.fixOperation ??= this.operationId();
    const task = Promise.resolve().then(async () => {
      if (!this.current()) return false;
      await fixStatePermissions(this.transport, this.serverId, operation, this.lifetime.signal);
      if (!this.current()) return false;
      this.fixOperation = undefined;
      this.publish({...this.state, permissions: false, fixed: true});return true;
    }).finally(() => {this.fixing = undefined;});
    this.fixing = task;return task;
  };
  retry = (): Promise<void> => {const intent=this.exportIntent;this.state = {...empty,...(intent?{prepareError:true,pendingExport:Object.freeze({windowMs:intent.windowMs,components:intent.components,includePaths:intent.includePaths===true})}:{}),...(this.state.export?{export:this.state.export}:{})};return this.load();};
  dispose(): void {if (!this.lifetime.signal.aborted) {this.lifetime.abort();this.listeners.clear();this.console.dispose();this.exportIntent=undefined;this.preparing=undefined;this.state = empty;}}
}
