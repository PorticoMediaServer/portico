// Test-only historical model; no production consumer mounts this module.
import type {ReadingClient, ReadingManifest, ReadingProgress, ReadingSession} from '../../../../../packages/client-core/src';
import type {NativeShellRoute} from '../../../src-next/shell/routes';
import {nativeReadEligibility, openNativeRead, type NativeReadEligibility, type NativeReadNavigationContext} from '../../../src-next/screens/read/eligibility';

export type NativeReadTitleRoute = Extract<NativeShellRoute, {kind: 'title'}>;
export type NativeReadTitleContext = NativeReadNavigationContext;
export type NativeReadTitleSnapshot = Readonly<{
  phase: 'checking' | 'ordinary' | 'reading' | 'error'; versions: Readonly<NonNullable<NativeReadEligibility>['versions']>;
  sourceId?: string; manifest?: ReadingManifest; progress?: ReadingProgress; busy: boolean; error?: unknown;
}>;

/** Title facts only. The real facade owns source/session/progress; an engine and
 * reading location store are never created by this detail controller. */
export class NativeReadTitleState {
  private snapshot: NativeReadTitleSnapshot = Object.freeze({phase: 'checking', versions: Object.freeze([]), busy: false});
  private listeners = new Set<() => void>();
  private client?: ReadingClient;
  private viewerFamily?: string;
  private round?: AbortController;
  private sessions = new Set<ReadingSession>();
  private closes = new WeakMap<ReadingSession, Promise<void>>();
  private active = true;
  private sequence = 0;
  private closing?: Promise<void>;
  private readonly retired: () => void;
  private readonly unsubscribeNavigation: () => void;
  private readonly context: NativeReadTitleContext;
  private readonly route: NativeReadTitleRoute;
  private readonly parent: AbortSignal;
  constructor(context: NativeReadTitleContext, route: NativeReadTitleRoute, parent: AbortSignal) {
    this.context = context; this.route = route; this.parent = parent;
    this.retired = () => {void this.dispose();};
    parent.addEventListener('abort', this.retired, {once: true});
    this.unsubscribeNavigation = context.navigation.subscribe(() => {
      if (context.navigation.getSnapshot().route !== route) void this.dispose();
    });
    if (parent.aborted) void this.dispose();
  }
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  getSnapshot = () => this.snapshot;
  private apiCurrent = () => this.context.current() && this.context.session()?.sessionFamilyId === this.viewerFamily;
  private current = () => this.active && !this.parent.aborted && this.apiCurrent() && this.context.navigation.getSnapshot().route === this.route;
  private publish(value: NativeReadTitleSnapshot) {if (!this.current()) return; this.snapshot = Object.freeze(value); for (const listener of [...this.listeners]) listener();}
  private close(session: ReadingSession): Promise<void> {
    const previous = this.closes.get(session); if (previous) return previous;
    const deadline = new AbortController(), timer = setTimeout(() => deadline.abort(), 2000);
    const promise = (this.apiCurrent() ? session.close(deadline.signal) : Promise.resolve())
      .finally(() => {clearTimeout(timer); this.sessions.delete(session);});
    this.closes.set(session, promise); return promise;
  }
  async load(): Promise<void> {
    const viewer = this.context.session();
    if (!this.active || this.parent.aborted || !viewer || !this.context.current() || this.context.navigation.getSnapshot().route !== this.route) return;
    this.viewerFamily ??= viewer.sessionFamilyId;
    if (!this.apiCurrent()) return;
    this.client ??= this.context.api.reading({viewer: viewer.viewer, isCurrent: this.apiCurrent});
    const sequence = ++this.sequence; this.round?.abort(); const round = this.round = new AbortController();
    try {
      const eligibility = await nativeReadEligibility(this.client, this.route.entry.id, round.signal);
      if (!this.current() || sequence !== this.sequence) return;
      if (eligibility === null) {this.publish({phase: 'ordinary', versions: Object.freeze([]), busy: false}); return;}
      this.publish({phase: 'reading', versions: eligibility.versions, busy: false});
      // A unique actual publication needs no choice. Multiple versions never
      // receive a client-invented first/preferred format or source.
      if (eligibility.versions.length === 1 && eligibility.versions[0]!.inventoryAvailable) await this.choose(eligibility.versions[0]!.sourceId);
    } catch (error) {if (this.current() && sequence === this.sequence) this.publish({...this.snapshot, phase: 'error', busy: false, error});}
  }
  choose = async (sourceId: string): Promise<void> => {
    if (!this.current() || !this.client || !this.snapshot.versions.some(version => version.sourceId === sourceId && version.inventoryAvailable)) return;
    const sequence = ++this.sequence; this.round?.abort(); const round = this.round = new AbortController();
    this.publish({phase: 'reading', versions: this.snapshot.versions, sourceId, busy: true});
    let session: ReadingSession | undefined;
    try {
      session = await this.client.open({itemId: this.route.entry.id, sourceId}, round.signal); this.sessions.add(session);
      const progress = await session.progress(round.signal), manifest = session.manifest;
      await this.close(session);
      if (this.current() && sequence === this.sequence) this.publish({phase: 'reading', versions: this.snapshot.versions, sourceId, manifest, progress, busy: false});
    } catch (error) {if (this.current() && sequence === this.sequence) this.publish({...this.snapshot, phase: 'error', busy: false, error});}
    finally {if (session) await this.close(session).catch(() => {});}
  };
  read = async (): Promise<boolean> => {
    const sourceId = this.snapshot.sourceId;
    if (!this.current() || this.snapshot.phase !== 'reading' || this.snapshot.busy || !sourceId || !this.snapshot.manifest) return false;
    return openNativeRead(this.context, {itemId: this.route.entry.id, sourceId}, this.parent);
  };
  dispose = (): Promise<void> => {
    if (this.closing) return this.closing;
    this.active = false; ++this.sequence; this.round?.abort(); this.parent.removeEventListener('abort', this.retired); this.unsubscribeNavigation(); this.listeners.clear();
    this.closing = Promise.allSettled([...this.sessions].map(session => this.close(session))).then(() => {this.client?.dispose(); this.sessions.clear();});
    return this.closing;
  };
}
