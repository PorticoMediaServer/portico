import {clonePortableDto} from './clone-portable-dto.ts';
import type {ReadingAnnotation, ReadingAnnotationContent, ReadingAnnotationPort, ReadingAnnotationsPage} from './reading-annotations.ts';

export type ReadingAnnotationIntent = Readonly<{revision: number | undefined; signal: AbortSignal; isCurrent: () => boolean}>;
export type ReadingAnnotationState = Readonly<{entries: readonly ReadingAnnotation[]; revision?: number; nextCursor: string; loading: boolean; saving: boolean; error?: unknown}>;
const empty: ReadingAnnotationState = Object.freeze({entries: Object.freeze([]), nextCursor: '', loading: false, saving: false});
/** One captured profile/source session. No background loops, offline replay or re-anchoring. */
export class ReadingAnnotationService {
  private state: ReadingAnnotationState = Object.freeze({...empty, loading: true});
  private listeners = new Set<() => void>();
  private request?: AbortController;
  private disposed = false;
  constructor(readonly port: ReadingAnnotationPort, private readonly isCurrent: () => boolean) {}
  current = () => !this.disposed && !this.port.signal.aborted && this.port.manifest.canAnnotate === true && this.isCurrent();
  getSnapshot = () => this.current() ? this.state : empty;
  subscribe = (listener: () => void) => {this.listeners.add(listener);return () => {this.listeners.delete(listener);};};
  private publish(state: ReadingAnnotationState) {if (this.current()) {this.state = Object.freeze(state);this.listeners.forEach(listener => listener());}}
  private owns(request: AbortController) {return this.current() && this.request === request && !request.signal.aborted;}
  private accept(page: ReadingAnnotationsPage, append: boolean) {
    if (append && page.revision !== this.state.revision) throw new Error('annotation_revision_changed');
    const entries = append ? [...this.state.entries, ...page.entries] : [...page.entries];
    if (entries.length > 256 || new Set(entries.map(entry => entry.id)).size !== entries.length) throw new Error('invalid_reading_annotations');
    this.publish({entries: Object.freeze(entries), revision: page.revision, nextCursor: page.nextCursor, loading: false, saving: false});
  }
  load = async (more = false): Promise<void> => {
    if (!this.current() || this.request || more && !this.state.nextCursor) return;
    const request = new AbortController();this.request = request;
    const cursor = more ? this.state.nextCursor : '', revision = more ? this.state.revision : undefined;
    this.publish({...this.state, loading: true, error: undefined});
    try {
      if (!this.owns(request)) return;
      const page = await this.port.annotations(cursor, revision, request.signal);
      if (this.owns(request)) this.accept(page, more);
    } catch (error) {if (this.owns(request)) this.publish({...this.state, revision: undefined, loading: false, saving: false, error});}
    finally {if (this.request === request) this.request = undefined;}
  };
  write = async (content: ReadingAnnotationContent | Readonly<{id: string; remove: true}>, intent: ReadingAnnotationIntent): Promise<boolean> => {
    const admitted = () => this.current() && !intent.signal.aborted && intent.isCurrent();
    if (!admitted() || this.request || intent.revision === undefined || this.state.revision !== intent.revision) return false;
    const expectedRevision = intent.revision, captured = clonePortableDto(content);
    const mutation = 'remove' in captured ? {operation: 'delete' as const, expectedRevision, id: captured.id}
      : {...captured, operation: 'upsert' as const, expectedRevision};
    const request = new AbortController();this.request = request;
    const abort = () => request.abort();intent.signal.addEventListener('abort', abort, {once:true});
    const owns = () => this.owns(request) && admitted();
    let attempted = false, accepted = false;
    this.publish({...this.state, saving: true, error: undefined});
    try {
      if (!owns() || this.state.revision !== expectedRevision) return false;
      attempted = true;
      const saved = await this.port.mutateAnnotation(mutation, request.signal);
      if (!owns()) return false;
      const page = await this.port.annotations('', undefined, request.signal);
      if (!owns()) return false;
      if (page.revision !== saved.revision) throw new Error('annotation_revision_changed');
      this.accept(page, false);accepted = true;return true;
    } catch (error) {
      if (owns()) this.publish({...this.state, revision: undefined, loading: false, saving: false, error});
      return false;
    } finally {
      intent.signal.removeEventListener('abort', abort);
      if (this.request === request) {
        this.request = undefined;
        // Cancellation after admission may have committed remotely. Never reuse its CAS.
        if (!accepted && this.current()) this.publish({...this.state, saving:false,
          ...(attempted ? {revision:undefined,error:new Error('annotation_reload_required')} : {})});
      }
    }
  };
  dispose() {this.disposed = true;this.request?.abort();this.request = undefined;this.listeners.clear();this.state = empty;}
}
