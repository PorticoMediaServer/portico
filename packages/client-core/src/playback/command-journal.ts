import type { Command, CreateRequest, DesiredState, ID, IntentRequest, StopRequest, UInt64 } from './protocol.ts';
import { canonicalJSON, canonicalUTF8 } from './canonical.ts';

/** Local persistence API, not another public wire contract. */
export type LaneScope = Readonly<{
  serverOrigin: string; accountId: ID; profileId: ID;
  controllerId: ID; controllerEpoch: ID; commandLaneId: ID;
}>;
export type IntentDraft =
  | Readonly<{ operation: 'create'; playbackId: null; body: Omit<CreateRequest, 'command'> }>
  | Readonly<{ operation: 'intent'; playbackId: ID; body: Omit<IntentRequest, 'command'> }>
  | Readonly<{ operation: 'stop'; playbackId: ID; body: Omit<StopRequest, 'command'> }>;
export type PreparedCommand = Readonly<{
  localMutationId: ID; requestId: ID; sequence: UInt64; localRevision: UInt64;
  operation: IntentDraft['operation']; playbackId: ID | null;
  draftCanonical: string; envelopeCanonical: string;
}>;
export type LaneState = Readonly<{
  scope: LaneScope; revision: UInt64; highestAllocated: UInt64; retired: boolean; terminal: boolean;
  desired: DesiredState | null; currentRequestId: ID | null;
}>;
export type JournalTransaction = {
  readonly lane: LaneState | null;
  findMutation(id: ID): PreparedCommand | null;
  findRequest(id: ID): PreparedCommand | null;
  putLane(lane: LaneState): void;
  addCommand(command: PreparedCommand): void;
};
/** Must execute one synchronous callback and commit all writes atomically/durably. */
export interface AtomicCommandStore {
  transaction<T>(key: string, work: (tx: JournalTransaction) => T, lookup?: Readonly<{ mutationId: ID; requestId: ID }>): Promise<T>;
}
export class JournalConflict extends Error {
  constructor(readonly code: 'local_revision' | 'mutation_identity' | 'request_identity' | 'retired' | 'terminal' | 'lane_occupied' | 'uninitialized',
              readonly currentRevision: UInt64 | null) { super('Playback journal conflict: ' + code); }
}
const counter = (value: string): bigint => {
  if (!/^(0|[1-9][0-9]{0,18})$/.test(value) || BigInt(value) > 9223372036854775807n)
    throw new Error('Invalid durable counter');
  return BigInt(value);
};
const next = (value: string) => {
  const n = counter(value) + 1n;
  if (n > 9223372036854775807n) throw new Error('Retire controller before counter exhaustion');
  return String(n);
};
function identity(value: string) {
  if (!/^[A-Za-z0-9_-]{1,128}$/.test(value)) throw new Error('Invalid playback identity');
}
export function laneStorageKey(scope: LaneScope): string {
  for (const [name, value] of Object.entries(scope)) if (name !== 'serverOrigin') identity(value);
  const url = new URL(scope.serverOrigin);
  if (!['http:', 'https:'].includes(url.protocol) || url.origin !== scope.serverOrigin)
    throw new Error('Journal authority must have an exact normalized server origin');
  return JSON.stringify([scope.serverOrigin,scope.accountId,scope.profileId,scope.controllerId,scope.controllerEpoch,scope.commandLaneId]);
}
/** No transport, timers or recovery work runs in construction or persistence callbacks. */
export class ClientCommandJournal {
  constructor(private readonly store: AtomicCommandStore) {}
  initialize(scope: LaneScope): Promise<LaneState> {
    const key = laneStorageKey(scope);
    return this.store.transaction(key, tx => {
      if (tx.lane) return tx.lane; // Never reset an existing epoch/lane watermark.
      const lane: LaneState = { scope: JSON.parse(canonicalJSON(scope)), revision: '0', highestAllocated: '0',
        retired: false, terminal: false, desired: null, currentRequestId: null };
      tx.putLane(lane); return lane;
    });
  }
  /** Draft must already pass public schema+semantic validation. A stale snapshot is never rebased. */
  prepare(scope: LaneScope, expectedLocalRevision: UInt64, localMutationId: ID, requestId: ID,
          draft: IntentDraft): Promise<PreparedCommand> {
    const key = laneStorageKey(scope);
    counter(expectedLocalRevision); identity(localMutationId); identity(requestId);
    const frozenDraft = JSON.parse(canonicalJSON(draft)) as IntentDraft;
    const draftCanonical = canonicalJSON(frozenDraft);
    return this.store.transaction(key, tx => {
      const lane = tx.lane;
      if (!lane) throw new JournalConflict('uninitialized', null);
      const replay = tx.findMutation(localMutationId);
      // Exact local replay survives later intent; caller reads current state separately.
      if (replay) {
        if (replay.requestId !== requestId || replay.draftCanonical !== draftCanonical)
          throw new JournalConflict('mutation_identity', lane.revision);
        return replay;
      }
      if (lane.retired) throw new JournalConflict('retired', lane.revision);
      // A local Close fence suppresses resumed playback immediately. A rejected Stop may
      // still reconcile public revisions and submit a newer Stop; never reopen the lane.
      if (lane.terminal && frozenDraft.operation !== 'stop') throw new JournalConflict('terminal', lane.revision);
      if (frozenDraft.operation === 'create' && lane.currentRequestId !== null && frozenDraft.body.replaces === null)
        throw new JournalConflict('lane_occupied', lane.revision);
      if (lane.revision !== expectedLocalRevision) throw new JournalConflict('local_revision', lane.revision);
      if (tx.findRequest(requestId)) throw new JournalConflict('request_identity', lane.revision);
      const sequence = next(lane.highestAllocated), revision = next(lane.revision);
      const command: Command = { controllerId:scope.controllerId,controllerEpoch:scope.controllerEpoch,
        commandLaneId:scope.commandLaneId,sequence,requestId };
      const body = { ...frozenDraft.body, command };
      // Check actual request body's 64KiB limit, then record the public canonical envelope.
      canonicalUTF8(body);
      const envelopeCanonical = canonicalJSON({ operation:frozenDraft.operation, targetId:frozenDraft.playbackId, body });
      const prepared: PreparedCommand = { localMutationId,requestId,sequence,localRevision:revision,
        operation:frozenDraft.operation,playbackId:frozenDraft.playbackId,draftCanonical,envelopeCanonical };
      tx.addCommand(prepared);
      tx.putLane({ ...lane,revision,highestAllocated:sequence,currentRequestId:requestId,
        terminal: frozenDraft.operation === 'stop',
        desired: frozenDraft.operation === 'stop' ? lane.desired : frozenDraft.body.desired });
      return prepared;
    }, { mutationId: localMutationId, requestId });
  }
  read(scope: LaneScope): Promise<LaneState | null> {
    return this.store.transaction(laneStorageKey(scope), tx => tx.lane);
  }
  retire(scope: LaneScope, expectedLocalRevision: UInt64): Promise<void> {
    counter(expectedLocalRevision);
    return this.store.transaction(laneStorageKey(scope), tx => {
      if (!tx.lane) throw new JournalConflict('uninitialized', null);
      if (tx.lane.retired) return;
      if (tx.lane.revision !== expectedLocalRevision) throw new JournalConflict('local_revision', tx.lane.revision);
      tx.putLane({ ...tx.lane,retired:true,revision:next(tx.lane.revision),currentRequestId:null });
    });
  }
}
