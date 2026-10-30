import {decoderProbes} from './decoder-probes';
import {openDemuxer} from './demux';
import {createDecoder, type Planar} from './decoder';
import {browserEngine, recordDecoderSkip, WEBCODECS_SKIP} from './track';
import {u16le, type TrackConfig} from './types';

async function bounded<T>(work: Promise<T>, late?: (value: T) => void): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined, expired = false;
  void work.then(value => { if (expired) late?.(value); }, () => {});
  try { return await Promise.race([work, new Promise<T>((_, reject) => { timer = setTimeout(() => { expired = true; reject(new Error('Decoder probe timed out.')); }, 1500); })]); }
  finally { clearTimeout(timer); }
}

let qualification: Promise<void> | undefined;
export const qualifiedConfigs: Record<string, TrackConfig> = {};

/** Admit new browser paths only after a bounded real decode agrees with known PCM and alignment. */
export function qualifyBrowserDecoders(): Promise<void> {
  return qualification ??= (async () => {
    const engine = browserEngine();
    for (const family of Object.keys(decoderProbes)) delete (WEBCODECS_SKIP[engine] as Record<string, unknown>)[family];
    for (const [family, probe] of Object.entries(decoderProbes)) {
      let decoder: Awaited<ReturnType<typeof createDecoder>>;
      try {
        const bytes = Uint8Array.from(atob(probe.data), c => c.charCodeAt(0));
        const demux = await openDemuxer(probe.container, {size: bytes.length, read: async (o, n) => bytes.slice(o, o + n)});
        decoder = await bounded(createDecoder(demux.config), d => d?.close());
        if (!decoder) continue;
        const pcm: number[] = [];
        const take = (blocks: Planar[]) => {
          for (const b of blocks) {
            if (pcm.length + b.frames > 24000) throw new Error('Decoder output exceeds its probe.');
            for (const v of b.channels[0]!) pcm.push(v);
          }
        };
        for await (const packet of demux.packets()) { decoder.decode(packet); take(await bounded(decoder.drain(8))); }
        take(await bounded(decoder.flush()));
        let best = Infinity, found = -1;
        // The anchor cannot move forward: decoders can discard priming, never add it.
        for (let i = 0; i <= probe.anchor && i + probe.reference.length <= pcm.length; i++) {
          let error = 0;
          for (let j = 0; j < probe.reference.length; j++) error += (pcm[i + j]! - probe.reference[j]!) ** 2;
          if (error < best) { best = error; found = i; }
        }
        if (found < 0 || Math.sqrt(best / probe.reference.length) > 0.002) continue;
        const skip = probe.anchor - found;
        // Count and correlation must agree; a decoder clipping the end is not a gapless path.
        if (pcm.length < probe.rawFrames - skip || pcm.length > probe.rawFrames - skip + 4096) continue;
        if (family === 'opus' && skip !== u16le(demux.config.description!, 10)) continue;
        recordDecoderSkip(engine, family, skip);
        qualifiedConfigs[family] = demux.config;
      } catch { /* unsupported codec, output mismatch or decode failure: retain conversion */ }
      finally { decoder?.close(); }
    }
  })();
}
