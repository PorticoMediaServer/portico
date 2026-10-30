import type {DownloadPreparation} from '@core/downloads.ts';
import type {DownloadsService} from '@core/downloads-service.ts';
import type {SavedDownloadsScope} from './downloads-port.ts';
import {SavedOperations} from './operations.ts';

export type SaveReadyPreparation = (preparation: DownloadPreparation, signal: AbortSignal) => Promise<void>;

/** Grant and progress revisions do not identify different bytes. */
export function readyArtifactKey(preparation: DownloadPreparation): string | undefined {
  const artifact = preparation.artifact;
  if (preparation.state !== 'ready' || !artifact.kind || artifact.estimated || !/^[0-9a-f]{64}$/.test(artifact.sha256) || !Number.isSafeInteger(artifact.bytes) || artifact.bytes < 1) return undefined;
  return JSON.stringify([preparation.id, artifact.kind, artifact.ref, artifact.sha256, artifact.bytes]);
}

/** One captured runtime observes its existing service, without another list or
 * transfer engine. A resolved handoff acknowledges the native queue only. */
export class SavedReadyDownloads {
  private readonly attempted = new Set<string>();
  private readonly operations: SavedOperations;
  private readonly unsubscribe: () => void;

  constructor(service: DownloadsService, scope: SavedDownloadsScope, parent: SavedOperations, save: SaveReadyPreparation, reportError: (error: unknown) => void) {
    this.operations = new SavedOperations(() => parent.current);
    const observe = () => {
      if (!this.operations.current) return;
      for (const preparation of service.getSnapshot().items) {
        if (preparation.profileId !== scope.profileId) continue;
        const key = readyArtifactKey(preparation);
        if (key === undefined || this.attempted.has(key)) continue;
        // Reserve before calling the adapter: native ACKs can synchronously
        // publish another snapshot while this genuine artifact is pending.
        this.attempted.add(key);
        void this.operations.run(signal => save(preparation, signal)).catch(reportError);
      }
    };
    this.unsubscribe = service.subscribe(observe);
    observe();
  }

  dispose(): void {
    this.unsubscribe();
    this.operations.dispose();
    this.attempted.clear();
  }
}
