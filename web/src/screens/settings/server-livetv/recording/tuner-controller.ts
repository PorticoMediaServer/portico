import type {LibraryContentApi} from '@core/library-content.ts';
import {parseEnvelope, parseTunerView, type TunerView} from '@core/administration.ts';
import {OwnerStore, errorFacts} from '../../../live/owner/lifetime';
export class TunerController extends OwnerStore<Readonly<{loading: boolean; view?: TunerView; error?: unknown}>> {
  constructor(private api: LibraryContentApi, private serverId: string, current: () => boolean, reportError: (error: unknown) => void) {super(Object.freeze({loading: false}), current, reportError);}
  async load() {
    if (!this.isActive() || this.snapshot.loading) return;
    const attempt = this.begin();this.publish({...this.snapshot, loading: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      const raw = await this.api.request('/v1/admin/dvr/tuners', 'GET', undefined, attempt.abort.signal);if (!this.valid(attempt)) return;
      const view = parseEnvelope(raw, this.serverId, parseTunerView).result;this.publish({loading: false, view});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
}
