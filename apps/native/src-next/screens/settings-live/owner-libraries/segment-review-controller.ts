import {approveSegments, editSegmentMarker, readSegmentStatus, readSegmentTitle, readSegmentTitles, redetectSegments, type SegmentApi, type SegmentEdit, type SegmentReviewEntry, type SegmentStatus, type SegmentTitlePage, type SegmentTitleReview} from '../../../../../../packages/client-core/src/segment-review';
import {NativeScreenLifetime, NativeScreenStore} from '../lifetime';
type Snapshot = Readonly<{status?: SegmentStatus; page?: SegmentTitlePage; review?: SegmentTitleReview; libraryId?: string; titleId?: string; loading: boolean; busy: boolean; error?: unknown; notice?: Readonly<{kind: 'approved' | 'redetected'; count: number}>}>;
/** One viewport, actual server answers, no optimistic marker state. Navigation cancels stale reads. */
export class NativeSegmentReviewController extends NativeScreenStore<Snapshot> {
  private read?: AbortController;
  private generation = 0;
  private disposed = false;
  constructor(private api: SegmentApi, readonly lifetime: NativeScreenLifetime) {super({loading: false, busy: false});}
  private current = () => !this.disposed && this.lifetime.current();
  async open(libraryId?: string, titleId?: string, append = false): Promise<void> {
    if (!this.current() || this.getSnapshot().busy) return;
    this.read?.abort(); const read = this.read = new AbortController(), generation = ++this.generation;
    const before = this.getSnapshot(), sameView = before.libraryId === libraryId && before.titleId === titleId;
    this.publish({...before, libraryId, titleId, page: append || sameView ? before.page : undefined, review: sameView ? before.review : undefined, loading: true, error: undefined, notice: undefined});
    try {
      const value = await this.lifetime.request<SegmentTitleReview | SegmentTitlePage | SegmentStatus>(signal => titleId ? readSegmentTitle(this.api, titleId, signal) : libraryId ? readSegmentTitles(this.api, libraryId, append ? before.page?.nextCursor : undefined, signal) : readSegmentStatus(this.api, signal), read.signal);
      if (!this.current() || read.signal.aborted || generation !== this.generation) return;
      if (titleId) {
        const review = value as SegmentTitleReview;
        if (review.id !== titleId || review.libraryId !== libraryId) throw new Error('segment_review_target_mismatch');
        this.publish({...this.getSnapshot(), review, loading: false});
      } else if (libraryId) {
        const page = value as SegmentTitlePage;
        if (page.libraryId !== libraryId) throw new Error('segment_review_library_mismatch');
        const titles = append ? [...new Map([...(before.page?.titles ?? []), ...page.titles].map(title => [title.id, title])).values()] : page.titles;
        this.publish({...this.getSnapshot(), page: {...page, titles}, loading: false});
      } else this.publish({...this.getSnapshot(), status: value as SegmentStatus, loading: false});
    } catch (error) {if (this.current() && generation === this.generation && !read.signal.aborted) this.publish({...this.getSnapshot(), loading: false, error});}
  }
  reload = () => {const state = this.getSnapshot(); return this.open(state.libraryId, state.titleId);};
  async change(work: () => Promise<unknown>, notice?: 'approved' | 'redetected'): Promise<void> {
    if (!this.current() || this.getSnapshot().busy) return;
    this.publish({...this.getSnapshot(), busy: true, error: undefined, notice: undefined});
    try {
      const result = await this.lifetime.request(() => work());
      if (!this.current()) return;
      this.publish({...this.getSnapshot(), busy: false});
      await this.reload();
      if (this.current() && !this.getSnapshot().error && notice && typeof result === 'number') this.publish({...this.getSnapshot(), notice: {kind: notice, count: result}});
    } catch (error) {if (this.current()) this.publish({...this.getSnapshot(), busy: false, error}); throw error;}
  }
  edit(entry: SegmentReviewEntry, edit: SegmentEdit) {return this.change(() => editSegmentMarker(this.api, entry, edit));}
  approve(season?: number) {const id = this.getSnapshot().review?.id; return id ? this.change(() => approveSegments(this.api, id, ['intro', 'recap', 'credits', 'commercial', 'outro'], season), 'approved') : Promise.resolve();}
  redetect() {const id = this.getSnapshot().review?.id; return id ? this.change(() => redetectSegments(this.api, id), 'redetected') : Promise.resolve();}
  dispose() {this.disposed = true; this.read?.abort(); this.clearListeners();}
}
