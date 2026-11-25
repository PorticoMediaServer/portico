import type {RecordingDetail} from '@core/dvr.ts';
import {NextDVRClient} from '@core/next-dvr.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';

/** One bounded detail read for a conflict's real overlaps. Both the screen lifetime and
 * client are disposed on completion or abort, and the caller's AbortSignal is forwarded
 * into every underlying fetch so cancelled reads never leak or race. */
export async function readRecordingDetail(context: ScreenContext, recordingId: string, signal: AbortSignal): Promise<RecordingDetail> {
  const combined = context.libraryRouteSignal ? AbortSignal.any([context.libraryRouteSignal, signal]) : signal;
  const life = screenLifetime({...context, libraryRouteSignal: combined});
  const client = new NextDVRClient(life.api);
  const retire = () => {client.dispose(); life.dispose();};
  if (signal.aborted || !life.current()) {
    retire();
    throw new DOMException('Recording detail retired', 'AbortError');
  }
  signal.addEventListener('abort', retire, {once: true});
  try {
    const detail = await client.detail(recordingId);
    if (signal.aborted || !life.current()) throw new DOMException('Recording detail retired', 'AbortError');
    return detail;
  } finally {
    signal.removeEventListener('abort', retire);
    retire();
  }
}
