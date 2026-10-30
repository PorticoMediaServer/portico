/** Safe measured catalogue facts. Missing/invalid facts stay absent; a codec or
 * filename never supplies a guessed bit depth, rate, channel count or losslessness. */
export type MusicTechnicalFacts = Readonly<{
 codec: string; sampleRate?: number; bitDepth?: number; channels?: number; layout?: string; lossless?: boolean; atmos?: boolean;
}>;
export function parseMusicTechnicalFacts(value: unknown): MusicTechnicalFacts | undefined {
 if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
 const raw = value as Record<string, unknown>;
 if (typeof raw.codec !== 'string' || !/^[A-Za-z0-9_.+-]{1,64}$/.test(raw.codec)) return undefined;
 const facts: { -readonly [K in keyof MusicTechnicalFacts]: MusicTechnicalFacts[K] } = {codec: raw.codec};
 for (const key of ['sampleRate', 'bitDepth', 'channels'] as const) {
  const amount = raw[key];
  if (typeof amount === 'number' && Number.isSafeInteger(amount) && amount > 0) facts[key] = amount;
 }
 // Layout is the measured stream label, never inferred from channel count.
 if (typeof raw.layout === 'string' && raw.layout.length > 0 && raw.layout.length <= 128 && !/[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/.test(raw.layout)) facts.layout = raw.layout;
 if (typeof raw.lossless === 'boolean') facts.lossless = raw.lossless;
 if (raw.atmos === true && ['truehd', 'eac3'].includes(raw.codec.toLowerCase())) facts.atmos = true;
 return Object.freeze(facts);
}
