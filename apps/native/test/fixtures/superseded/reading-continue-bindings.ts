// Test-only historical model; no production consumer mounts this module.
import {ReadingError, type LocalSession, type ReadingClient} from '../../../../../packages/client-core/src';
import {sameAuthority, type ShellAuthority} from '../../../src-next/shell/model';
import {openNativeRead, type NativeReadNavigationContext} from '../../../src-next/screens/read/eligibility';

/** The canonical decoded page and row, never a second Continue DTO. */
export type NativeReadingContinuePage = Awaited<ReturnType<ReadingClient['continue']>>;
export type NativeReadingContinueRow = NativeReadingContinuePage['items'][number];
/** The actual optional library/cursor/limit accepted by the canonical client. */
export type NativeReadingContinueOptions = NonNullable<Parameters<ReadingClient['continue']>[0]>;
export type NativeReadingContinueBindingsOptions = Readonly<{
  /** The consumer's own visible list: the actual resident row object, never an
   * equal-looking copy or an id that merely exists in a previous page. */
  resident: (row: NativeReadingContinueRow) => boolean;
  /** Read destinations are not admitted on television; refuse before any read. */
  isTV: boolean;
}>;
const refused = () => Object.assign(new Error('native_reading_continue_refused'), {name: 'AbortError', code: 'cancelled'});

/** Reading Continue facts for Native Home. Construction performs no request and
 * stores no page; the visible list stays the consumer's, and the canonical
 * ReadingClient owns the captured viewer's one authorized Continue source. */
export class NativeReadingContinueBindings {
  private readonly context: NativeReadNavigationContext;
  private readonly resident: NativeReadingContinueBindingsOptions['resident'];
  private readonly isTV: boolean;
  private readonly authority: ShellAuthority;
  private readonly captured?: Readonly<{sessionFamilyId: string; viewer: LocalSession['viewer']}>;
  private readonly life = new AbortController();
  private client?: ReadingClient;
  private disposed = false;
  constructor(context: NativeReadNavigationContext, options: NativeReadingContinueBindingsOptions) {
    this.context = context; this.resident = options.resident; this.isTV = options.isTV;
    this.authority = context.authority();
    const selected = context.session();
    this.captured = selected && Object.freeze({sessionFamilyId: selected.sessionFamilyId, viewer: Object.freeze({...selected.viewer})});
  }
  /** The original captured viewer and authority, not whichever is mounted now. */
  private current = (): boolean => {
    const captured = this.captured, selected = this.context.session();
    return !this.disposed && !this.life.signal.aborted && this.context.current() && !!captured && !!selected
      && selected.sessionFamilyId === captured.sessionFamilyId && sameAuthority(this.authority, this.context.authority());
  };
  private reading(): ReadingClient {
    if (!this.current()) throw refused();
    return this.client ??= this.context.api.reading({viewer: this.captured!.viewer, isCurrent: this.current});
  }
  /** One canonical page exactly as decoded: no local default, cache, row store or
   * per-item prepare. A retired, replaced or TV binding refuses before IO. */
  continue = (options: NativeReadingContinueOptions = {}, signal?: AbortSignal): Promise<NativeReadingContinuePage> => {
    if (this.isTV || !this.current() || signal?.aborted) return Promise.reject(refused());
    return this.reading().continue(options, signal);
  };
  /** Opens the exact resident row's real source through the existing Read door.
   * Membership, inventory and platform are decided before any network request. */
  open = async (row: NativeReadingContinueRow): Promise<boolean> => {
    if (this.isTV || !this.current()) return false;
    if (!this.resident(row)) return false;
    if (row.item.inventoryAvailable !== true) return false;
    try {return await openNativeRead(this.context, {itemId: row.item.id, sourceId: row.item.sourceId}, this.life.signal);}
    catch (error) {
      // Canonical cancellation (retirement or replaced owner) is a refusal, not
      // a source failure; it never becomes a late navigation on the old owner.
      const cancelled = error instanceof ReadingError && error.code === 'cancelled' && error.status === undefined;
      if (this.life.signal.aborted || !this.current() || cancelled) return false;
      throw error;
    }
  };
  dispose = (): void => {
    if (this.disposed) return;
    this.disposed = true; this.life.abort(); this.client?.dispose(); this.client = undefined;
  };
}
