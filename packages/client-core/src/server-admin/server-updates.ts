import type {State} from '../../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from '../query/server-api.ts';

/**
 * Updates on the rebuilt server: the build it runs, whether a newer one is out (`newer`), and how it is installed.
 * Reading it never contacts the release feed.
 */
export type ServerUpdates = State;

export function readServerUpdates(api: ServerRequester, signal?: AbortSignal): Promise<ServerUpdates> {
  return callServer(api, 'GET /v1/admin/updates', {signal});
}

/** The Settings card's Update pill: on only when the server says a newer build is out. */
export const updatePillShown = (updates: unknown): boolean =>
  typeof updates === 'object' && updates !== null && (updates as {newer?: unknown}).newer === true;
