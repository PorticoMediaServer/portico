import {callServer} from '@core/query/server-api.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {OwnerTunerView} from '@core/server-admin/live-sources.ts';
import {OwnerStore, errorFacts} from '../../../live/owner/lifetime';
export class TunerController extends OwnerStore<Readonly<{loading: boolean; view?: OwnerTunerView; error?: unknown}>> {
  constructor(private api: LibraryContentApi, _serverId: string, current: () => boolean, reportError: (error: unknown) => void) {super(Object.freeze({loading: false}), current, reportError);}
  async load() {
    if (!this.isActive() || this.snapshot.loading) return;
    const attempt = this.begin();this.publish({...this.snapshot, loading: true, error: undefined});if (!this.valid(attempt)) return;
    try {
      const view = await callServer(this.api, 'GET /v1/admin/dvr/tuners', {signal: attempt.abort.signal});if (!this.valid(attempt)) return;
      if (!Array.isArray(view.sources) || view.sources.some(source => !Number.isSafeInteger(source.inUse) || !Array.isArray(source.active) || !Array.isArray(source.upcoming))) throw new Error('live_owner_response_invalid');
      this.publish({loading: false, view});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {...this.snapshot, loading: false, error: errorFacts(error)});}
  }
}
