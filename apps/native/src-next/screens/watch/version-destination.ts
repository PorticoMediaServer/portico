import type {NativePlaybackAdapter} from '../../platform/playback/types';
import type {PlaybackOptions} from '../../../../../packages/client-core/src/playback-v1/types';
import type {NativeWatchCapture} from './factory';
import type {DiscTitles} from './disc-titles';
import type {NativeVersionRow} from './version-options';

type Lifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;
type DiscTitle = Readonly<{versionId: string; discTitle: Readonly<{id: string; revision: string}>}>;

type Ports = Readonly<{
  capture: NativeWatchCapture;
  options: PlaybackOptions;
  rows: readonly NativeVersionRow[];
  selectedRowId?: string;
  hostCurrent: () => boolean;
  sourceCurrent: () => boolean;
  start: NativePlaybackAdapter['startVersion'];
  /** The opening this chooser was built for; live until it is presented. */
  opening?: () => boolean;
  /** The device's preparation of one alternate title; its 202 body is the document
   * it re-read. Bound to the real route, never to a synthetic version. */
  prepare: (write: Readonly<{itemId: string; versionId: string; revision: string; titleId: string}>, lifetime: Lifetime) => Promise<DiscTitles | undefined>;
  /** The viewer's own read of that version's titles, under the same opening. */
  reread: (versionId: string, lifetime: Lifetime) => Promise<DiscTitles | undefined>;
  /** That exact title's own options read. The canonical read refuses a response
   * whose version does not echo this id and revision. */
  preview: (preview: DiscTitle, signal: AbortSignal) => Promise<PlaybackOptions>;
  onDispose?: () => void;
}>;

/** The sheet owns its pending command lifetime. The old output fence remains
 * runtime preflight authority; the host predicate permits its accepted new source. */
export class NativeVersionChooserController {
  readonly options: PlaybackOptions;
  readonly rows: readonly NativeVersionRow[];
  readonly selectedRowId?: string;
  private readonly ports: Ports;
  private readonly controller = new AbortController();
  private pending = false;
  /** One acknowledged preparation per row and document revision. A later press on
   * the same row reads again instead of asking the server to analyze it twice. */
  private acknowledged = new Map<string, string>();
  /** The row whose facts the server has been asked for and not accepted yet. */
  pendingRowId?: string;
  constructor(ports: Ports) {this.ports = ports; this.options = ports.options; this.rows = ports.rows; this.selectedRowId = ports.selectedRowId;}
  /** Bound by the presenter once this chooser is the presented one: a sheet that was
   * replaced is no longer a command lifetime, whatever the route still says. */
  present = (current: () => boolean) => {this.presented = current;};
  private presented?: () => boolean;
  private hostCurrent = () => !this.controller.signal.aborted && (this.presented ? this.presented() : this.ports.opening?.() !== false) && this.ports.hostCurrent();
  isCurrent = () => this.hostCurrent() && (this.pending || this.ports.sourceCurrent());
  /** Returns whether a start was actually issued. A disc title that is no longer
   * ready, or whose facts moved, is not played and the sheet stays open. */
  choose = async (id: string): Promise<boolean> => {
    if (this.pending || !this.hostCurrent() || !this.ports.sourceCurrent()) return false;
    const row = this.rows.find(value => value.id === id);
    if (!row) return false;
    this.pending = true;
    try {
      return row.disc ? await this.chooseDisc(row, row.disc) : await this.chooseVersion(row.id);
    } finally {this.pending = false;}
  };
  private chooseVersion = async (versionId: string): Promise<boolean> => {
    const {capture, options, start} = this.ports, target = capture.entry.playback;
    if (!target || target.itemId !== options.itemId || target.itemId !== capture.entry.id) return false;
    if (!this.hostCurrent() || !this.ports.sourceCurrent()) return false;
    await start({entry: capture.entry, target: {...target, startSeconds: (options.resume?.positionMs ?? 0) / 1000}},
      {authority: capture.authority, options, versionId, expectedFence: capture.expectedFence,
        lifetime: {signal: this.controller.signal, isCurrent: this.hostCurrent}});
    return true;
  };
  /** One of the physical version's other titles: prepared under the document the row
   * was drawn from, previewed on that exact title, then started on it. Nothing here
   * is a version of the film and nothing falls back to the main title. */
  private chooseDisc = async (row: NativeVersionRow, disc: NonNullable<NativeVersionRow['disc']>): Promise<boolean> => {
    const {capture, prepare, reread, preview, start} = this.ports, target = capture.entry.playback;
    if (!target || target.itemId !== capture.entry.id) return false;
    const lifetime = {signal: this.controller.signal, isCurrent: this.hostCurrent};
    // One write under the document revision the row was drawn from. For a title the
    // server has not analyzed yet this is what queues its analysis; either way it
    // changes neither the main title nor this version's duration or association. An
    // acknowledged preparation is never repeated for this row and revision.
    if (this.acknowledged.get(row.id) !== disc.revision) {
      const prepared = await prepare({itemId: capture.entry.id, versionId: disc.versionId, revision: disc.revision, titleId: disc.titleId}, lifetime);
      if (!prepared || !this.isCurrent() || prepared.versionId !== disc.versionId) return false;
      this.acknowledged.set(row.id, disc.revision);
    }
    // A 202 only says the work is queued. The viewer's own read, under the same
    // opening, is what says this title's facts are accepted now; its own revision is
    // the only one a start may name, and a row that was already analyzed must not
    // have moved to a different one.
    const fresh = await reread(disc.versionId, lifetime);
    if (!fresh || !this.isCurrent() || fresh.versionId !== disc.versionId) return false;
    const title = fresh.titles.find(value => value.id === disc.titleId);
    // A title that is no longer in the document is stale or gone, not preparing: say
    // so with the app's own words and stop claiming it is pending. Only a title that
    // is actually present without accepted facts keeps its Preparing marker.
    if (!title) {
      this.pendingRowId = undefined;
      throw Object.assign(new Error('disc title unavailable'), {code: 'not_found'});
    }
    if (!title.ready || !title.revision) {this.pendingRowId = row.id; return false;}
    if (disc.titleRevision !== undefined && title.revision !== disc.titleRevision) {this.pendingRowId = undefined; return false;}
    this.pendingRowId = undefined;
    const chosen = {id: title.id, revision: title.revision};
    const offered = await preview({versionId: disc.versionId, discTitle: chosen}, this.controller.signal);
    if (!offered || !this.isCurrent() || offered.itemId !== capture.entry.id) return false;
    // An alternate title begins at its own start, never at the main title's resume.
    await start({entry: capture.entry, target: {...target, startSeconds: 0}},
      {authority: capture.authority, options: offered, versionId: disc.versionId, discTitle: chosen,
        expectedFence: capture.expectedFence, lifetime});
    return true;
  };
  dispose = () => {if (this.controller.signal.aborted) return; this.controller.abort(); this.ports.onDispose?.();};
}
