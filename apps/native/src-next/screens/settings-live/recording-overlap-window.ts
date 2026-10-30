import type {Recording, RecordingDetail, RecordingOverlap} from '../../../../../packages/client-core/src/dvr';
import {DashboardCursorWindow, type DashboardCursorSnapshot} from './dashboard-cursor-window';
import type {NativeRecordingsController} from './recordings-controller';

export type OverlapSnapshot = DashboardCursorSnapshot<RecordingOverlap, RecordingDetail>;

/** One detail opening: all continuations carry the original recording and viewer. */
export class RecordingOverlapWindow {
  private window: DashboardCursorWindow<RecordingOverlap, RecordingDetail>;
  constructor(recording: Recording, detail: RecordingDetail, controller: NativeRecordingsController,
    current: () => boolean, changed: (state: OverlapSnapshot) => void) {
    const original = Object.freeze({id: recording.id, revision: recording.revision});
    if (detail.recording.id !== original.id || detail.recording.revision !== original.revision || !current()) throw new Error('recording_opening_changed');
    this.window = new DashboardCursorWindow(async (cursor, signal) => {
      if (!current() || signal.aborted) throw new Error('recording_opening_changed');
      const page = await controller.detail(original.id, cursor);
      if (!current() || signal.aborted) throw new Error('recording_opening_changed');
      if (page.recording.id !== original.id || page.recording.revision !== original.revision) throw new Error('dashboard_refresh_required');
      return {items: page.overlaps, nextCursor: page.nextOverlap, metadata: page};
    }, row => row.id, current, changed);
  }
  seed = (detail: RecordingDetail): void => {this.window.seed('', {items: detail.overlaps, nextCursor: detail.nextOverlap, metadata: detail});};
  next = (expected: OverlapSnapshot): Promise<void> => this.window.next(expected);
  previous = (expected: OverlapSnapshot): Promise<void> => this.window.previous(expected);
  retry = (expected: OverlapSnapshot): Promise<void> => this.window.retry(expected);
  dispose = (): void => this.window.dispose();
}
