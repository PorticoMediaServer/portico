/**
 * One engine-failure command, adapted the same way on every client: the browser tab and the
 * native apps tell the server that the engine could not play the current session's presentation,
 * and the server re-plans that same session another way. The detail is display-ready text and the
 * position is the picture's place in milliseconds; neither may carry control characters or a
 * negative position. The result says only whether playback escalates (the server re-planned it)
 * or not; a transient failure returns `null` so the caller keeps its own retry policy.
 *
 * Everything platform-specific stays in the caller's ports: which controller holds the session,
 * how the re-planned session is remembered, how listeners are told and how the service's session
 * shape is produced. The command is not a licence to change failure codes, to replay a
 * noncurrent session, or to move engine attachment, progress and end reporting in here.
 */
import {PlaybackApiError} from './playback-v1/http.ts';
import type {PlayerFailureCode} from './client-profile.ts';

export type PlayerFailureCommandResult<P> = Readonly<{escalates: boolean; session?: P}>;

export type PlayerFailureCommand = Readonly<{
  /** The session the caller captured; a different current session is not commanded. */
  sessionId: string;
  code: PlayerFailureCode;
  detail: string;
  positionSeconds: number;
}>;

export type PlayerFailureCommandPorts<S, P> = Readonly<{
  /** The session the controller holds now, if it holds one. */
  current: () => string | undefined;
  /** The controller's re-plan of that captured session: `undefined` when it no longer holds it. */
  change: (failure: Readonly<{code: PlayerFailureCode; detail: string}>, positionMs: number) => Promise<S | undefined>;
  /** Remember the re-planned session before anyone is notified. */
  retain: (session: S) => S;
  /** Tell the change listeners; after `retain`, before the result reaches the caller. */
  changed: (session: S) => void;
  /** The public playback shape for that session. */
  playback: (session: S) => P;
}>;

/** Control characters become spaces and the detail is bounded: it is stored and shown as text. */
export function playerFailureDetail(detail: string): string {
  return detail.replace(/[\x00-\x1f\x7f]/g, ' ').slice(0, 500);
}

/** A non-negative millisecond position, rounded as the engines report it. */
export function playerFailurePositionMs(positionSeconds: number): number {
  return Math.max(0, Math.round(positionSeconds * 1000));
}

/** The exact adaptation: current session, one re-plan, retain, notify, map; refusals stay refused. */
export async function playerFailureCommand<S, P>(ports: PlayerFailureCommandPorts<S, P>, command: PlayerFailureCommand): Promise<PlayerFailureCommandResult<P> | null> {
  if (ports.current() !== command.sessionId) return {escalates: false};
  try {
    const updated = await ports.change({code: command.code, detail: playerFailureDetail(command.detail)}, playerFailurePositionMs(command.positionSeconds));
    if (!updated) return {escalates: false};
    const session = ports.retain(updated);
    ports.changed(session);
    return {escalates: true, session: ports.playback(session)};
  } catch (e) {
    // unsupported_media: that was the last way to deliver the file.
    if (e instanceof PlaybackApiError && (e.code === 'unsupported_media' || e.status === 404 || e.status === 410)) return {escalates: false};
    return null;
  }
}
