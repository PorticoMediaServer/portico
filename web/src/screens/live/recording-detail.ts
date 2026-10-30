import {DVRClient, type RecordingDetail} from '@core/dvr.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';

const randomHex = async () => Array.from(crypto.getRandomValues(new Uint8Array(24)), value => value.toString(16).padStart(2, '0')).join('');

/** One bounded detail read for a conflict's real overlaps. The reader is disposed with the
 * component that asked for it; nothing is retained between panels. */
export async function readRecordingDetail(context: ScreenContext, recordingId: string, signal: AbortSignal): Promise<RecordingDetail> {
  const life = screenLifetime(context);
  const client = new DVRClient(life.api, context.scope.serverId, randomHex);
  const active = life.signal(signal);
  try {
    const detail = await client.detail(recordingId);
    if (active.aborted || !context.isCurrent()) throw new DOMException('Recording detail retired', 'AbortError');
    return detail;
  } finally {
    client.dispose();
  }
}
