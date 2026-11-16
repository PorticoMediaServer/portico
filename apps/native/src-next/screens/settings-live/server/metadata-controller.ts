import type {LibraryContentApi} from '../../../../../../packages/client-core/src/library-content';
import {metadataSettingsService, type MetadataSettings, type MetadataSettingsChange} from '../../../../../../packages/client-core/src/metadata-settings';
import {metadataChange} from './metadata-setting-values';

export type NativeMetadataSnapshot = Readonly<{data?: MetadataSettings; busy: boolean; error?: unknown; needsReload: boolean}>;
export type MetadataPatch = Partial<Omit<MetadataSettingsChange, 'expectedRevision'>>;

/** One mounted owner page, one revisioned command, no retained credential drafts. */
export class NativeMetadataController {
  private state: NativeMetadataSnapshot = Object.freeze({busy: false, needsReload: false});
  private listeners = new Set<() => void>();
  private abort = new AbortController();
  private service: ReturnType<typeof metadataSettingsService>;
  private isCurrent: () => boolean;
  constructor(api: LibraryContentApi, isCurrent: () => boolean) {
    this.isCurrent = isCurrent;
    this.service = metadataSettingsService(async <T,>(path: string, method?: string, body?: unknown): Promise<T> => {
      if (!this.current()) throw Object.assign(new Error('metadata_page_closed'), {name: 'AbortError'});
      const value = await api.request<T>(path, method, body, this.abort.signal);
      if (!this.current()) throw Object.assign(new Error('metadata_page_closed'), {name: 'AbortError'});
      return value;
    });
  }
  private current = () => !this.abort.signal.aborted && this.isCurrent();
  getSnapshot = (): NativeMetadataSnapshot => this.state;
  subscribe = (listener: () => void) => {this.listeners.add(listener); return () => {this.listeners.delete(listener);};};
  private publish(patch: Partial<NativeMetadataSnapshot>) {
    if (!this.current()) return;
    this.state = Object.freeze({...this.state, ...patch});
    for (const listener of [...this.listeners]) listener();
  }
  dispose() {this.abort.abort(); this.listeners.clear();}
  async load(): Promise<void> {
    if (!this.current() || this.state.busy) return;
    this.publish({busy: true, error: undefined});
    try {const data = await this.service.read(); this.publish({data, needsReload: false});}
    catch (error) {this.publish({error});}
    finally {this.publish({busy: false});}
  }
  async save(original: MetadataSettings, patch: MetadataPatch): Promise<boolean> {
    if (!this.current() || this.state.busy || this.state.needsReload || this.state.data !== original) return false;
    this.publish({busy: true, error: undefined});
    try {const data = await this.service.save(metadataChange(original, patch)); if (!this.current()) return false; this.publish({data}); return true;}
    catch (error) {this.publish({error, needsReload: true}); return false;}
    finally {this.publish({busy: false});}
  }
  async checkOpenSubtitles(original: MetadataSettings): Promise<void> {
    if (!this.current() || this.state.busy || this.state.needsReload || this.state.data !== original) return;
    this.publish({busy: true, error: undefined});
    try {const openSubtitles = await this.service.checkOpenSubtitles(); this.publish({data: Object.freeze({...original, openSubtitles})});}
    catch (error) {this.publish({error});}
    finally {this.publish({busy: false});}
  }
}
