/**
 * The notification inbox on the rebuilt server: `GET /v1/notifications?view=unread|all|archived` (50 a page, with the
 * unread count), `PATCH /v1/notifications/{id}` (read or unread, archived or not) and `POST /v1/notifications/read-all`.
 * A notice carries its kind and the facts it is worded from (`params`), or text a person wrote (`body`); the app words
 * it here, in the shape the inbox screens already draw. There is one inbox per profile: the owner's and administrators'
 * server notices arrive in it too, so the old separate administrators' audience is empty.
 */
import type {Notification as ServerNotification, Notifications} from '../../contracts/src/next/api.ts';
import {defaultI18n, type MessageId, type MessageValues} from '../../i18n/src/index.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import type {Notification, NotificationInbox, NotificationSeverity} from './notifications.ts';

export type NoticeView = 'unread' | 'all' | 'archived';
type Wording = Readonly<{title: MessageId; body?: MessageId; severity: NotificationSeverity}>;
/** How each kind the server sends is worded. A kind this build does not know is said plainly, never dropped. */
const wordings: Readonly<Record<string, Wording>> = {
  storage_nearly_full: {title: 'next.shell.issue.storageTitle', body: 'next.shell.issue.storageDetail', severity: 'warning'},
  backup_failed: {title: 'next.shell.issue.backupTitle', body: 'next.shell.issue.backupDetail', severity: 'critical'},
  remote_unreachable: {title: 'next.shell.issue.remoteTitle', body: 'next.shell.issue.remoteDetail', severity: 'critical'},
  update_available: {title: 'web.maintenance.updateAvailable', body: 'notifications.notice.updateBody', severity: 'info'},
  message: {title: 'notifications.notice.messageTitle', severity: 'info'},
  tool_message: {title: 'notifications.notice.toolMessageTitle', severity: 'info'},
  report_reply: {title: 'notifications.notice.reportReplyTitle', severity: 'info'},
};
const other: Wording = {title: 'notifications.notice.otherTitle', severity: 'info'};
const facts = (params: unknown): Record<string, string> => {
  if (!params || typeof params !== 'object' || Array.isArray(params)) return {};
  return Object.fromEntries(Object.entries(params as Record<string, unknown>).filter(([, value]) => typeof value === 'string' || typeof value === 'number').map(([key, value]) => [key, String(value)]).slice(0, 12));
};
const at = (instant: string) => {const ms = Date.parse(instant); return Number.isFinite(ms) ? ms : 0;};

/** One notice as the inbox draws it. */
export function noticeOf(notice: ServerNotification): Notification {
  const wording = wordings[notice.kind] ?? other, args = facts(notice.params);
  const values: MessageValues = {...args, subject: args.source ?? args.library ?? ''};
  // A person's own words (the owner's message, a reply) are shown as they are; a server fact the server gave a reason for
  // says that reason; otherwise the kind's own sentence.
  const body = notice.body?.trim() || (notice.kind === 'remote_unreachable' ? args.problem : notice.kind === 'backup_failed' ? args.reason : '') || (wording.body ? defaultI18n.t(wording.body, values) : '');
  const created = at(notice.createdAt);
  return {id: notice.id, audience: 'profile', severity: wording.severity, source: 'server', category: notice.kind, title: defaultI18n.t(wording.title, values), body: body.slice(0, 2000),
    arguments: args, actions: [], dedupeKey: '', revision: 1, createdAt: created, updatedAt: created, expiresAt: 0,
    readAt: notice.read ? created : null, archivedAt: notice.archived ? created : null, read: notice.read, archived: notice.archived, cursor: ''};
}
/** One page of a view, in the shape the inbox reads. The server keeps no revision for the inbox, so every page reads 1. */
export function inboxOf(page: Notifications, view: NoticeView): NotificationInbox {
  const items = (Array.isArray(page?.items) ? page.items : []).map(noticeOf), unread = Number.isSafeInteger(page?.unreadCount) && page.unreadCount >= 0 ? page.unreadCount : 0;
  return {revision: 1, audience: 'profile', audiences: ['profile'], state: view, counts: {unread, read: 0, archived: 0, total: unread}, items, nextCursor: page?.nextCursor ?? '', observedAt: Date.now(), retentionDays: 0};
}
/** One page of the profile's inbox. */
export async function readNotices(api: ServerRequester, view: NoticeView, cursor?: string, signal?: AbortSignal): Promise<NotificationInbox> {
  return inboxOf(await callServer(api, 'GET /v1/notifications', {query: {view, cursor}, signal}), view);
}
/** The badge's number: the unread count the inbox answers with. */
export async function readUnreadNotices(api: ServerRequester, signal?: AbortSignal): Promise<number> {
  return (await readNotices(api, 'unread', undefined, signal)).counts.unread;
}
export type NoticeChange = 'read' | 'unread' | 'archive' | 'unarchive';
/** Marks each notice; the server answers nothing, so the inbox reads its page again for the counts. */
export async function changeNotices(api: ServerRequester, change: NoticeChange, ids: readonly string[], signal?: AbortSignal): Promise<void> {
  const body = change === 'read' ? {read: true} : change === 'unread' ? {read: false} : {archived: change === 'archive'};
  for (const id of ids) await callServer(api, 'PATCH /v1/notifications/{id}', {id, body, signal});
}
export const readAllNotices = (api: ServerRequester, signal?: AbortSignal): Promise<void> => callServer(api, 'POST /v1/notifications/read-all', {signal});
