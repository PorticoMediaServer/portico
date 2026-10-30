import {browseCapabilitiesPath, parseBrowseCapabilities, type BrowseCapabilities} from '../../../../../packages/client-core/src/browse';
import type {LibraryContentApi} from '../../../../../packages/client-core/src/library-content';
import type {PersonalResource} from '../../../../../packages/client-core/src/personal-saved';
import {savedViewSummary, type SavedViewSummaryText} from '../../../../../packages/client-core/src/presentation/saved-view-summary';

type Ports = Readonly<{api: LibraryContentApi; serverId: string; current: () => boolean; libraryIds: () => readonly string[]}>;
const key = (libraryId: string, pivot: string) => JSON.stringify([libraryId, pivot]);

/** Route-local capability reads for the current bounded Saved page. Only the
 * canonical resolver computes words, at render time; no resource is mutated. */
export class NativeHomeSavedViewSummaries {
  private resources: readonly PersonalResource[] = [];
  private capabilities = new Map<string, BrowseCapabilities>();
  private listeners = new Set<() => void>();
  private revision = 0;
  private read?: AbortController;
  private alive = true;
  private readonly ports: Ports;
  constructor(ports: Ports) {this.ports = ports;}
  getSnapshot = () => this.revision;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private current = () => this.alive && this.ports.current();
  private eligible = (view: PersonalResource) => this.current() && view.serverId === this.ports.serverId && view.kind === 'view' && view.status === 'ready'
    && !!view.definition && this.ports.libraryIds().includes(view.definition.libraryId);
  private publish() {if (!this.current()) return; this.revision++; for (const listener of this.listeners) listener();}
  load = async (resources: readonly PersonalResource[]): Promise<void> => {
    if (!this.current() || resources === this.resources) return;
    this.read?.abort(); const read = this.read = new AbortController();
    this.resources = resources; this.capabilities.clear(); this.publish();
    const targets = [...new Map(resources.filter(this.eligible).map(view => {
      const definition = view.definition!; return [key(definition.libraryId, definition.pivot), definition] as const;
    })).entries()];
    const current = () => this.current() && this.read === read && !read.signal.aborted;
    let next = 0;
    await Promise.all(Array.from({length: Math.min(4, targets.length)}, async () => {
      while (current()) {
        const target = targets[next++]; if (!target) return;
        const [id, definition] = target;
        try {
          const raw = await this.ports.api.request<unknown>(browseCapabilitiesPath(definition.libraryId, definition.pivot), 'GET', undefined, read.signal);
          if (!current() || !this.ports.libraryIds().includes(definition.libraryId)) continue;
          const capabilities = parseBrowseCapabilities(raw);
          if (capabilities.library.id !== definition.libraryId) continue;
          this.capabilities.set(id, capabilities); this.publish();
        } catch { /* An unavailable vocabulary supplies no summary or guessed fallback. */ }
      }
    }));
  };
  line = (view: PersonalResource, text: SavedViewSummaryText): string | undefined => {
    if (!this.eligible(view) || !this.resources.includes(view)) return;
    const definition = view.definition!, capabilities = this.capabilities.get(key(definition.libraryId, definition.pivot));
    return capabilities ? savedViewSummary(definition, capabilities, text)?.line : undefined;
  };
  dispose = () => {this.alive = false; this.read?.abort(); this.resources = []; this.capabilities.clear(); this.listeners.clear();};
}
