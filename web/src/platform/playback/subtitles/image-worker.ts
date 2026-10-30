/**
 * The worker that reads image subtitles. It keeps the decoded track; the page asks for one cue's
 * pictures at a time, a little before they are due, so the page never decodes anything itself.
 */
import type {BitmapTrack} from './bitmap.ts';
import {parseDvb} from './dvb.ts';
import type {FrameRect, ImageWorkerReply, ImageWorkerRequest} from './image-protocol.ts';
import {parsePgs} from './pgs.ts';
import {parseVobSub} from './vobsub.ts';

const scope = self as unknown as {postMessage(message: ImageWorkerReply, transfer?: Transferable[]): void; onmessage: ((event: {data: ImageWorkerRequest}) => void) | null};
let track: BitmapTrack | null = null;
let painting = false;
const pending: number[] = [];
let bitmaps = typeof createImageBitmap === 'function' && typeof ImageData === 'function';

async function paint(index: number): Promise<void> {
  const rects: FrameRect[] = [], transfer: Transferable[] = [];
  for (const rect of track?.cues[index]?.paint() ?? []) {
    const placed = {x: rect.x, y: rect.y, width: rect.width, height: rect.height};
    if (bitmaps) {
      try {
        const bitmap = await createImageBitmap(new ImageData(rect.rgba as unknown as Uint8ClampedArray<ArrayBuffer>, rect.width, rect.height));
        rects.push({...placed, bitmap});
        transfer.push(bitmap);
        continue;
      } catch { bitmaps = false; }
    }
    const rgba = rect.rgba.buffer as ArrayBuffer;
    rects.push({...placed, rgba});
    transfer.push(rgba);
  }
  scope.postMessage({type: 'frame', index, rects}, transfer);
}

async function drain(): Promise<void> {
  if (painting) return;
  painting = true;
  try {
    while (pending.length) {
      const index = pending.shift()!;
      await paint(index).catch(() => scope.postMessage({type: 'frame', index, rects: []}));
    }
  } finally { painting = false; }
}

scope.onmessage = event => {
  const message = event.data;
  try {
    if (message.type === 'load') {
      const data = new Uint8Array(message.data);
      track = message.kind === 'pgs' ? parsePgs(data) : message.kind === 'vobsub' ? parseVobSub(message.index ?? '', data) : parseDvb(data);
      if (!track.cues.length || !track.width || !track.height) { scope.postMessage({type: 'failed', code: 'no_pictures'}); return; }
      const starts = Float64Array.from(track.cues, cue => cue.startMs), ends = Float64Array.from(track.cues, cue => cue.endMs);
      // Which cues the disc marks forced (foreign dialogue, signs), for the viewer's "Forced only".
      const forced = Uint8Array.from(track.cues, cue => (cue.forced ? 1 : 0));
      scope.postMessage({type: 'ready', width: track.width, height: track.height, starts, ends, forced, skipped: track.skipped}, [starts.buffer, ends.buffer, forced.buffer]);
    } else if (message.type === 'paint') {
      if (!Number.isInteger(message.index) || message.index < 0 || !track || message.index >= track.cues.length) return;
      // One paint runs at a time; excessive queued seeks are answered without allocating pixels.
      if (pending.length >= 2) scope.postMessage({type: 'frame', index: pending.shift()!, rects: []});
      pending.push(message.index);
      void drain();
    }
  } catch {
    scope.postMessage({type: 'failed', code: 'unreadable'});
  }
};
