// Test-only historical model; no production consumer mounts this module.
import type {ListeningSnapshot} from '../../../../../packages/client-core/src/listening';
import type {NativeListeningStatus} from '../../../../../packages/client-core/src/playback/native-listening';

/** No lease/credential values enter a display model. The caller captures observed
 * alongside the ListeningService snapshot under its playback authority, then
 * passes the current identity at render time. Context alone is not authority. */
export type NativeNoticeIdentity = Readonly<Pick<NonNullable<NativeListeningStatus['binding']>, 'sessionId' | 'generation' | 'itemId' | 'intentId'>>;
export type NativeListeningNotice = Readonly<{
  identity: NativeNoticeIdentity; key: 'listening.rewound'; values: Readonly<{seconds: number}>;
}>;

function validIdentity(value: NativeNoticeIdentity | null): value is NativeNoticeIdentity {
  return !!value && !!value.sessionId && !!value.itemId && Number.isSafeInteger(value.generation) && value.generation >= 0
    && Number.isSafeInteger(value.intentId) && value.intentId >= 0;
}

/** A projection of the existing rewind snapshot, never a new notice store. The
 * host retains responsibility for authority invalidation, announcement and time
 * limits. Pause reasons cannot be inferred from free-form message/error text. */
export function nativeListeningNotice(snapshot: ListeningSnapshot, observed: NativeNoticeIdentity | null, current: NativeNoticeIdentity | null): NativeListeningNotice | undefined {
  if (!validIdentity(observed) || !validIdentity(current) || observed.sessionId !== current.sessionId
    || observed.generation !== current.generation || observed.itemId !== current.itemId || observed.intentId !== current.intentId
    || snapshot.context?.itemId !== current.itemId || snapshot.stopped || snapshot.error || snapshot.phase === 'disposed' || snapshot.phase === 'error') return;
  const rewind = snapshot.rewind;
  if (!rewind || rewind.messageKey !== 'listening.rewound' || !Number.isFinite(rewind.seconds) || rewind.seconds <= 0
    || !Number.isFinite(rewind.fromSeconds) || !Number.isFinite(rewind.toSeconds) || rewind.toSeconds < 0
    || rewind.fromSeconds < rewind.toSeconds || Math.abs(rewind.fromSeconds - rewind.toSeconds - rewind.seconds) > .001) return;
  return Object.freeze({identity: Object.freeze({...current}), key: rewind.messageKey, values: Object.freeze({seconds: rewind.seconds})});
}
