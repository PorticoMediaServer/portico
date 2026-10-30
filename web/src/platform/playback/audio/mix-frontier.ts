/** A decoded buffer's immutable position in the renderer's logical mix timeline. */
export type MixPacket = Readonly<{start: number; end: number; buffer: AudioBuffer; offset: number}>;
export type MixVoice = Readonly<{id: object; start: number; end: number; packets: readonly MixPacket[]}>;
export type MixSlice = Readonly<{voice: object; buffer: AudioBuffer; start: number; offset: number; frames: number}>;
export type MixAdmission = Readonly<{frontier: number; slices: readonly MixSlice[]}>;

/**
 * Plan the contiguous interval for which every voice in the accepted mix has actual PCM.
 * A future voice begins constraining the mix exactly at its start, even at zero fade gain.
 * Callers retain packets and schedule the returned slices only after the whole plan is accepted.
 */
export function planMix(voices: readonly MixVoice[], from: number, limit: number): MixAdmission {
  if (!Number.isSafeInteger(from) || !Number.isSafeInteger(limit) || limit < from) throw new Error('invalid mix interval');
  const identities = new Set<object>();
  for (const voice of voices) {
    if (identities.has(voice.id) || !Number.isSafeInteger(voice.start) || !Number.isSafeInteger(voice.end) || voice.end < voice.start) throw new Error('invalid mix voice');
    identities.add(voice.id);
    let previousEnd = voice.start;
    for (const packet of voice.packets) {
      if (!Number.isSafeInteger(packet.start) || !Number.isSafeInteger(packet.end) || !Number.isSafeInteger(packet.offset) || packet.offset < 0 || packet.start < previousEnd || packet.end <= packet.start || packet.end > voice.end || packet.offset + packet.end - packet.start > packet.buffer.length) throw new Error('invalid PCM packet');
      previousEnd = packet.end;
    }
  }
  const slices: MixSlice[] = [];
  let cursor = from;
  while (cursor < limit) {
    const active = voices.filter(voice => voice.start <= cursor && cursor < voice.end);
    if (!active.length) break;
    let end = limit;
    for (const voice of voices) {
      if (voice.start > cursor) end = Math.min(end, voice.start);
      if (voice.start <= cursor && voice.end > cursor) end = Math.min(end, voice.end);
    }
    const packets: {voice: MixVoice; packet: MixPacket}[] = [];
    for (const voice of active) {
      const packet = voice.packets.find(item => item.start <= cursor && cursor < item.end);
      if (!packet) return {frontier: cursor, slices};
      end = Math.min(end, packet.end);
      packets.push({voice, packet});
    }
    if (end <= cursor) throw new Error('invalid mix boundary');
    for (const {voice, packet} of packets) slices.push({voice: voice.id, buffer: packet.buffer, start: cursor, offset: packet.offset + cursor - packet.start, frames: end - cursor});
    cursor = end;
  }
  return {frontier: cursor, slices};
}
