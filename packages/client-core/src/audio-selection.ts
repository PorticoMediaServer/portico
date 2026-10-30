/** In-memory commands and observations for an already-authorized session plan. */
export type AudioRendition = Readonly<{
  id: string;
  sourceStreamIndex: number;
  manifestIdentity: Readonly<{ groupId: string; name: string; playlistFile: string }>;
  label: string;
  language?: string;
  manifestLanguage?: string;
  sampleRate?: 48000;
  codec: 'aac' | 'ac3' | 'eac3' | 'mp3';
  channels: number;
  unavailable?: boolean;
}>;
export type AudioPlan = Readonly<{
  policyVersion: 1 | 2 | 3;
  revision: string;
  sessionId: string;
  generation: number;
  sourceId: string;
  factsRevision: number;
  defaultRenditionId: string;
  renditions: readonly AudioRendition[];
}>;
export type AudioBinding = Readonly<{
  intentId: number;
  sessionId: string;
  generation: number;
  sourceId: string;
  planRevision: string;
}>;
export type AudioSelectionSnapshot = Readonly<{
  binding: AudioBinding | null;
  plan: AudioPlan | null;
  availability: 'loading' | 'available' | 'unavailable';
  reason: string;
  observedRenditionId: string | null;
  revision: number;
  pending?: Readonly<{ revision: number; renditionId: string }>;
  unavailableRenditionIds?: readonly string[];
  failed?: Readonly<{ revision: number; renditionId: string; reason: string; unavailable?: boolean }>;
}>;
export const emptyAudio = (): AudioSelectionSnapshot =>
  Object.freeze({
    binding: null,
    plan: null,
    availability: 'unavailable',
    reason: 'Audio selection is not available for this playback.',
    observedRenditionId: null,
    revision: 0,
  });
export class AudioSelection {
  private state = emptyAudio();
  private epoch = 0;
  private sequence = 0;
  private timer?: ReturnType<typeof setTimeout>;
  private publish: (state: AudioSelectionSnapshot) => void;
  private current: () => {
    intentId: number;
    phase: string;
    session?: { id: string; generation: number; mode: string };
  };
  private timeoutMs: number;
  constructor(
    publish: AudioSelection['publish'],
    current: AudioSelection['current'],
    timeoutMs = 15000
  ) {
    this.publish = publish;
    this.current = current;
    this.timeoutMs = timeoutMs;
    if (!Number.isFinite(timeoutMs) || timeoutMs < 1 || timeoutMs > 60000)
      throw new Error('Invalid audio timeout');
  }
  private update(p: Partial<AudioSelectionSnapshot>) {
    this.state = Object.freeze({ ...this.state, ...p });
    this.publish(this.state);
  }
  private clear() {
    if (this.timer) clearTimeout(this.timer);
    this.timer = undefined;
  }
  reset() {
    this.clear();
    this.epoch++;
    this.sequence = 0;
    this.state = emptyAudio();
    return this.state;
  }
  private valid(b: AudioBinding, epoch?: number) {
    const c = this.current(),
      s = this.state.binding;
    return (
      !!s &&
      c.intentId === b.intentId &&
      c.session?.id === b.sessionId &&
      c.session.generation === b.generation &&
      ['starting', 'ready'].includes(c.phase) &&
      s.intentId === b.intentId &&
      s.sessionId === b.sessionId &&
      s.generation === b.generation &&
      s.sourceId === b.sourceId &&
      s.planRevision === b.planRevision &&
      (epoch === undefined || epoch === this.epoch)
    );
  }
  install(intentId: number, plan: AudioPlan): boolean {
    try {
      plan = validateAudioPlan(plan);
    } catch {
      return false;
    }
    const c = this.current();
    if (
      c.intentId !== intentId ||
      c.session?.id !== plan.sessionId ||
      c.session.generation !== plan.generation ||
      c.session.mode !== 'hls' ||
      !['starting', 'ready'].includes(c.phase)
    )
      return false;
    const b = {
      intentId,
      sessionId: plan.sessionId,
      generation: plan.generation,
      sourceId: plan.sourceId,
      planRevision: plan.revision,
    };
    if (this.state.binding)
      return this.valid(b) && JSON.stringify(this.state.plan) === JSON.stringify(plan);
    this.reset();
    this.update({
      binding: Object.freeze(b),
      plan,
      availability: 'loading',
      reason: 'Checking available audio…',
    });
    return true;
  }
  invalidate(intentId: number, reason: string) {
    if (this.current().intentId !== intentId) return;
    this.reset();
    this.update({ availability: 'unavailable', reason });
  }
  unavailable(intentId: number, reason: string) {
    if (this.current().intentId !== intentId || this.state.plan) return;
    this.reset();
    this.update({ availability: 'unavailable', reason });
  }
  attach(b: AudioBinding): number | null {
    if (!this.valid(b)) return null;
    this.clear();
    this.epoch++;
    this.sequence = 0;
    this.update({
      availability: 'loading',
      observedRenditionId: null,
      pending: undefined,
      failed: undefined,
    });
    return this.epoch;
  }
  detach(b: AudioBinding, epoch: number) {
    if (!this.valid(b, epoch)) return;
    this.clear();
    this.epoch++;
    this.sequence = 0;
    this.update({
      availability: 'loading',
      reason: 'Checking available audio…',
      observedRenditionId: null,
      pending: undefined,
      failed: undefined,
    });
  }
  mapped(b: AudioBinding, epoch: number, ids: readonly string[]): boolean {
    if (!this.valid(b, epoch)) return false;
    const expected = this.state.plan!.renditions.map((r) => r.id);
    if (
      ids.length !== expected.length ||
      new Set(ids).size !== ids.length ||
      ids.some((id) => !expected.includes(id))
    ) {
      this.clear();
      this.update({
        availability: 'unavailable',
        reason: 'This player could not identify these audio tracks.',
        pending: undefined,
        observedRenditionId: null,
      });
      return false;
    }
    if (this.state.availability !== 'available')
      this.update({ availability: 'available', reason: '' });
    return true;
  }
  select(id: string): boolean {
    const b = this.state.binding;
    if (
      !b ||
      !this.valid(b) ||
      this.state.availability !== 'available' ||
      this.state.unavailableRenditionIds?.includes(id) ||
      !this.state.plan?.renditions.some((r) => r.id === id && !r.unavailable)
    )
      return false;
    if (this.state.pending?.renditionId === id) return true;
    this.clear();
    const revision = this.state.revision + 1,
      epoch = this.epoch;
    this.timer = setTimeout(
      () =>
        this.failed(
          b,
          epoch,
          revision,
          'The player did not confirm the audio change in time. Try again.'
        ),
      this.timeoutMs
    );
    (this.timer as any)?.unref?.();
    this.update({
      revision,
      pending: Object.freeze({ revision, renditionId: id }),
      failed: undefined,
    });
    return true;
  }
  observed(b: AudioBinding, epoch: number, sequence: number, id: string | null): boolean {
    if (
      !this.valid(b, epoch) ||
      !Number.isSafeInteger(sequence) ||
      sequence <= this.sequence ||
      (id !== null && !this.state.plan!.renditions.some((r) => r.id === id))
    )
      return false;
    this.sequence = sequence;
    if (this.state.observedRenditionId !== id) this.update({ observedRenditionId: id });
    return true;
  }
  applied(b: AudioBinding, epoch: number, revision: number, id: string): boolean {
    if (
      !this.valid(b, epoch) ||
      this.state.pending?.revision !== revision ||
      this.state.pending.renditionId !== id ||
      this.state.observedRenditionId !== id
    )
      return false;
    this.clear();
    this.update({ pending: undefined, failed: undefined });
    return true;
  }
  renditionUnavailable(b:AudioBinding,epoch:number,id:string):boolean {
    if(!this.valid(b,epoch)||!this.state.plan?.renditions.some(r=>r.id===id))return false;
    if(this.state.pending?.renditionId===id)return this.failed(b,epoch,this.state.pending.revision,'This audio track is unavailable.',true);
    this.update({unavailableRenditionIds:Object.freeze([...new Set([...(this.state.unavailableRenditionIds??[]),id])])});return true;
  }
  failed(b: AudioBinding, epoch: number, revision: number, reason: string, unavailable=false): boolean {
    if (!this.valid(b, epoch) || this.state.pending?.revision !== revision) return false;
    this.clear();
    const safe =
      typeof reason === 'string' && reason.length <= 256 && !/[\x00-\x1f\x7f]/.test(reason)
        ? reason
        : 'The audio change could not be confirmed. Try again.';
    this.update({
      failed: Object.freeze({ ...this.state.pending, reason: safe, unavailable }),
      ...(unavailable?{unavailableRenditionIds:Object.freeze([...new Set([...(this.state.unavailableRenditionIds??[]),this.state.pending!.renditionId])])}:{}),
      pending: undefined,
    });
    return true;
  }
}

/** Strict session-plan grammar; transport scope is checked by PlayerOffersService. */
export function validateAudioPlan(raw: unknown): AudioPlan {
  const o = (v: unknown): v is Record<string, unknown> =>
    !!v && typeof v === 'object' && !Array.isArray(v);
  const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,256}$/.test(v);
  const n = (v: unknown, min = 0): v is number => Number.isSafeInteger(v) && Number(v) >= min;
  const text = (v: unknown): v is string =>
    typeof v === 'string' && v.length > 0 && v.length <= 256 && !/[\x00-\x1f\x7f]/.test(v);
  function bad(): never {
    throw new Error('Invalid audio rendition plan');
  }
  if (
    !o(raw) ||
    (raw.policyVersion !== 1 && raw.policyVersion !== 2 && raw.policyVersion !== 3) ||
    !id(raw.revision) ||
    !id(raw.sessionId) ||
    !n(raw.generation, 1) ||
    !id(raw.sourceId) ||
    !n(raw.factsRevision, 1) ||
    !id(raw.defaultRenditionId) ||
    !Array.isArray(raw.renditions) ||
    (raw.renditions.length < 1 || raw.renditions.length > 16)
  )
    bad();
  const renditions = (raw.renditions as unknown[]).map((v) => {
    if (
      !o(v) ||
      !id(v.id) ||
      !n(v.sourceStreamIndex) ||
      !text(v.label) ||
      (raw.policyVersion===3 ? !['aac','ac3','eac3','mp3'].includes(String(v.codec)) : v.codec!=='aac') ||
      (raw.policyVersion===3 ? !n(v.channels,1)||Number(v.channels)>64 : v.channels!==2) ||
      (v.language !== undefined && !text(v.language)) ||
      !o(v.manifestIdentity)
    )
      bad();
    if (
      raw.policyVersion === 2
        ? !validManifestLanguage(v.manifestLanguage) || v.sampleRate !== 48000
        : v.manifestLanguage !== undefined || v.sampleRate !== undefined
    )
      bad();
    const m = v.manifestIdentity as Record<string, unknown>;
    if (
      !id(m.groupId) ||
      !text(m.name) ||
      typeof m.playlistFile !== 'string' ||
      !/^(?:rendition|audio)-[0-9]+\.m3u8$/.test(m.playlistFile)
    )
      bad();
    return Object.freeze({
      id: v.id as string,
      ...(v.unavailable===true?{unavailable:true}:{}),
      sourceStreamIndex: v.sourceStreamIndex as number,
      label: v.label as string,
      codec: v.codec as AudioRendition['codec'],
      channels: v.channels as number,
      ...(v.language === undefined ? {} : { language: v.language as string }),
      ...(v.manifestLanguage === undefined
        ? {}
        : { manifestLanguage: v.manifestLanguage as string }),
      ...(v.sampleRate === undefined ? {} : { sampleRate: 48000 as const }),
      manifestIdentity: Object.freeze({
        groupId: m.groupId as string,
        name: m.name as string,
        playlistFile: m.playlistFile as string,
      }),
    });
  });
  if (
    new Set(renditions.map((r) => r.id)).size !== renditions.length ||
    new Set(renditions.map((r) => r.sourceStreamIndex)).size !== renditions.length ||
    new Set(renditions.map((r) => r.manifestIdentity.playlistFile)).size !== renditions.length ||
    new Set(renditions.map((r) => r.manifestIdentity.groupId + '|' + r.manifestIdentity.name))
      .size !== renditions.length ||
    !renditions.some((r) => r.id === raw.defaultRenditionId)
  )
    bad();
  return Object.freeze({
    policyVersion: raw.policyVersion as 1 | 2 | 3,
    revision: raw.revision as string,
    sessionId: raw.sessionId as string,
    generation: raw.generation as number,
    sourceId: raw.sourceId as string,
    factsRevision: raw.factsRevision as number,
    defaultRenditionId: raw.defaultRenditionId as string,
    renditions: Object.freeze(renditions),
  });
}

/** Structural BCP47 validation only; canonicalization/ISO aliases belong to the server. */
export function validManifestLanguage(value: unknown): value is string {
  if (
    typeof value !== 'string' ||
    value.length > 63 ||
    !value.length ||
    !/^[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$/.test(value)
  )
    return false;
  const parts = value.toLowerCase().split('-');
  let i = 0;
  const privateUse = () => {
    i++;
    const start = i;
    while (i < parts.length && /^[a-z0-9]{1,8}$/.test(parts[i])) i++;
    return i > start && i === parts.length;
  };
  if (parts[0] === 'x') return privateUse();
  if (!/^[a-z]{2,8}$/.test(parts[0])) return false;
  const baseLength = parts[0].length;
  i++;
  if (baseLength <= 3) {
    let n = 0;
    while (n < 3 && i < parts.length && /^[a-z]{3}$/.test(parts[i])) {
      i++;
      n++;
    }
  }
  if (i < parts.length && /^[a-z]{4}$/.test(parts[i])) i++;
  if (i < parts.length && /^(?:[a-z]{2}|[0-9]{3})$/.test(parts[i])) i++;
  const variants = new Set<string>();
  while (i < parts.length && /^(?:[a-z0-9]{5,8}|[0-9][a-z0-9]{3})$/.test(parts[i])) {
    if (variants.has(parts[i])) return false;
    variants.add(parts[i++]);
  }
  const extensions = new Set<string>();
  while (i < parts.length && /^[0-9a-wy-z]$/.test(parts[i])) {
    if (extensions.has(parts[i])) return false;
    extensions.add(parts[i++]);
    const start = i;
    while (i < parts.length && /^[a-z0-9]{2,8}$/.test(parts[i])) i++;
    if (i === start) return false;
  }
  if (parts[i] === 'x') return privateUse();
  return i === parts.length;
}

/** The generic selection handshake is populated from the current delivery manifest. */
export function renditionPlan(session:{id:string;generation:number},source:{id:string;factsRevision:number},renditions:readonly import('./delivery.ts').DeliveryAudioRendition[]):AudioPlan {
 return validateAudioPlan({policyVersion:3,revision:session.id+'-'+session.generation,sessionId:session.id,generation:session.generation,sourceId:source.id,factsRevision:Math.max(1,source.factsRevision),defaultRenditionId:'audio-'+renditions.find(r=>r.default)!.ordinal,renditions:renditions.map(r=>({id:'audio-'+r.ordinal,unavailable:!!r.failure,sourceStreamIndex:r.streamIndex,label:r.label,language:r.language,codec:r.codec,channels:Math.max(1,r.channels),manifestIdentity:{groupId:'audio',name:r.label,playlistFile:'audio-'+r.ordinal+'.m3u8'}}))});
}
