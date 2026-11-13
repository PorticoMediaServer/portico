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

/** Check now: the server asks its release feed once and answers with the same shape. */
export function checkServerUpdates(api: ServerRequester, signal?: AbortSignal): Promise<ServerUpdates> {
  return callServer(api, 'POST /v1/admin/updates/check', {signal});
}

/**
 * The "Latest version" line, in the app's words: the server's own words when its last check failed, else the newer
 * build when one is out, else up to date.
 */
export function latestVersionLine(updates: ServerUpdates, t: (id: 'next.dashboard.updateAvailable' | 'web.maintenance.updateAvailable' | 'web.maintenance.upToDate', values?: Record<string, string>) => string): string {
  if (updates.error) return updates.error;
  if (updates.newer) return updates.newest?.version ? t('next.dashboard.updateAvailable', {version: updates.newest.version}) : t('web.maintenance.updateAvailable');
  if (!updates.checkedAt && !updates.error) return '';
  return t('web.maintenance.upToDate');
}

/** The Settings card's Update pill: on only when the server says a newer build is out. */
export const updatePillShown = (updates: unknown): boolean =>
  typeof updates === 'object' && updates !== null && (updates as {newer?: unknown}).newer === true;
