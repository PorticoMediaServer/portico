import type {DownloadReceipt} from '../../../packages/client-core/src/downloads';
import type {PublicationBinding} from './offline-reading';

/** Trusted native-app port only. Neither this lease nor its receipt enters the
 * Reader guest. It describes the existing verified private download and retires
 * on exact context/entry/profile/receipt lifetime changes. History pause is NOT
 * an explicit-annotation permission and is intentionally absent here. */
export type NativeOfflinePublicationLease = Readonly<{
  entryId: string; itemId: string; owner: string; binding: PublicationBinding;
  signal: AbortSignal; current: () => boolean;
  receipt: () => DownloadReceipt;
  descriptor: () => Promise<string>;
  /** One private bounded namespace beside this exact download. Callers own
   * journal schema, CAS/merge and operation/tombstone semantics. */
  readState: (name: string) => Promise<string>;
  writeState: (name: string, json: string) => Promise<void>;
  dispose: () => void;
}>;
