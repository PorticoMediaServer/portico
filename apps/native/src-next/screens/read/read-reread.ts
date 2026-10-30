import {rereadReadingSource, type ReadingRereadReceipt} from '../../../../../packages/client-core/src/reading-reread';
import type {RepairData} from '../../../../../packages/client-core/src/metadata-repair';
import type {HttpLocalApi, ReadingManifest} from '../../../../../packages/client-core/src';
import type {NativeReadVersion} from './read-book-model';

const invalid = () => Object.assign(new Error('invalid_reading_reread'), {code: 'invalid_reading_reread'});

export type NativeReadingReread = Readonly<{api: HttpLocalApi; itemId: string; manifest: ReadingManifest;
  version: NativeReadVersion; data: RepairData; signal: AbortSignal; isCurrent: () => boolean}>;

/** The exact source the page selected: the rendered version from the Reading
 * versions producer must be the manifest's own source, and the manifest or its
 * conversion witness must still describe that same file and revision. Nothing
 * is looked up again or inferred from the latest state. */
export function nativeReadingRereadTarget(itemId: string, manifest: ReadingManifest, version: NativeReadVersion, data: RepairData) {
  if (manifest.itemId !== itemId || data.target.kind !== 'item' || data.target.id !== itemId) throw invalid();
  if (!version.sourceId || version.sourceId !== manifest.sourceId || version.inventoryAvailable !== true) throw invalid();
  const conversion = manifest.conversion;
  if (conversion && (conversion.originalSourceId !== version.sourceId || conversion.revision !== manifest.revision)) throw invalid();
  if (version.size !== (conversion?.originalSize ?? manifest.size)) throw invalid();
  const sourceId = conversion?.originalSourceId ?? manifest.sourceId;
  const sourceRevision = conversion?.originalRevision ?? manifest.revision;
  return Object.freeze({target: Object.freeze({itemId, sourceId, sourceRevision, metadataRevision: data.revision})});
}

/** The official reread helper consumes the exact published descriptor and
 * parser. The caller owns the page lease and metadata document; after success it
 * re-reads the repair document for the values the server accepted. The receipt's
 * refreshed/unknown partition is returned as the server stated it: a PDF may
 * refresh its author while series and number remain unknown, and no partial
 * determination is widened or rejected here. */
export async function rereadNativeReadingSource(options: NativeReadingReread): Promise<ReadingRereadReceipt> {
  const {target} = nativeReadingRereadTarget(options.itemId, options.manifest, options.version, options.data);
  return rereadReadingSource(options.api, target, {signal: options.signal, isCurrent: options.isCurrent});
}
