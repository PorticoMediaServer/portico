import {callServer} from '@core/query/server-api.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import {discovery} from './checked';
import {OwnerStore, errorFacts} from './lifetime';
export type Discovery = ReturnType<typeof discovery>;
export class DiscoveryController extends OwnerStore<Readonly<{busy: boolean; result?: Discovery; error?: unknown}>> {
  constructor(private api: LibraryContentApi, private serverId: string, current: () => boolean, reportError: (error: unknown) => void) {super(Object.freeze({busy: false}), current, reportError);}
  async search() {
    if (!this.isActive() || this.snapshot.busy) return;
    const attempt = this.begin();this.publish({busy: true});if (!this.valid(attempt)) return;
    try {
      const raw = await callServer(this.api, 'POST /v1/admin/live-sources/discover', {body: {windowMs: 5000}, signal: attempt.abort.signal});
      if (this.valid(attempt)) this.publish({busy: false, result: discovery(raw, this.serverId)});
    } catch (error) {if (this.valid(attempt)) this.fail(error, {busy: false, error: errorFacts(error)});}
  }
}
