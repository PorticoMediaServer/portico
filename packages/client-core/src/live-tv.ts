/**
 * Live TV and recordings on the new server, for every app (definition 11): On now, the guide,
 * one programme with its other airings, a profile's favourite and hidden channels, reminders,
 * and the DVR: bookings (upcoming, with conflicts first, and history), series rules and the
 * counts and space. Watching a channel is the ordinary player (`POST /v1/playback/sessions` with
 * `channel`) and lives with playback; a recorded programme is a title of the Recorded TV library.
 */
import type {LiveChannel, LiveChannelMarks, LiveChannelPage, DvrOverview, LiveGuide, LiveGuideRow, LiveOnNow, LiveOnNowPage, LiveProgrammeDetail, DvrRecording, DvrRecordingBook, DvrRecordingChange,
  DvrRecordingPage, LiveReminderOut, DvrRule, DvrRulePage, DvrRuleSave} from '../../contracts/src/next/api.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';

export type {LiveChannel, DvrOverview, LiveGuideRow, LiveOnNow, LiveProgrammeDetail, DvrRecording, LiveReminderOut, DvrRule};
/** Which channels, in what order: favourites only, hidden ones too, by number (the default) or by name. */
export type ChannelChoice = Readonly<{favorites?: boolean; hidden?: boolean; sort?: 'number' | 'name'}>;
const choice = (c: ChannelChoice = {}) => ({...(c.favorites ? {favorites: true} : {}), ...(c.hidden ? {hidden: true} : {}), ...(c.sort === 'name' ? {sort: 'name'} : {})});

/** On now: favourites first, then by number, with what is on and what is next. One page at a time. */
export const readOnNow = (api: ServerRequester, cursor?: string, signal?: AbortSignal): Promise<LiveOnNowPage> =>
  callServer(api, 'GET /v1/live/now', {query: {cursor}, signal});
/** The channels this profile may watch. */
export const readChannels = (api: ServerRequester, channels: ChannelChoice = {}, cursor?: string, signal?: AbortSignal): Promise<LiveChannelPage> =>
  callServer(api, 'GET /v1/live/channels', {query: {...choice(channels), cursor}, signal});
/**
 * The guide for a run of channels between two instants (up to a day; the server starts at the
 * current half hour and shows three hours when none is given), and whether it is out of date.
 */
export const readGuide = (api: ServerRequester, window: Readonly<{from?: Date; to?: Date}> = {}, channels: ChannelChoice = {}, cursor?: string, signal?: AbortSignal): Promise<LiveGuide> =>
  callServer(api, 'GET /v1/live/guide', {query: {...choice(channels), ...(window.from ? {from: window.from.toISOString()} : {}), ...(window.to ? {to: window.to.toISOString()} : {}), cursor}, signal});
/** One programme, its other airings, and whether it is booked or has a reminder. */
export const readProgramme = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<LiveProgrammeDetail> =>
  callServer(api, 'GET /v1/live/programmes/{id}', {id, signal});
/** Favourite and hide a channel, for this profile. */
export const markChannel = (api: ServerRequester, id: string, marks: LiveChannelMarks, signal?: AbortSignal): Promise<LiveChannelMarks> =>
  callServer(api, 'PUT /v1/live/channels/{id}/state', {id, body: marks, signal});

/** Reminders, each named by its programme; the app shows them when they come due. */
export const readReminders = (api: ServerRequester, cursor?: string, signal?: AbortSignal) =>
  callServer(api, 'GET /v1/live/reminders', {query: {cursor}, signal});
export const setReminder = (api: ServerRequester, programme: string, on: boolean, signal?: AbortSignal): Promise<void> =>
  callServer(api, on ? 'PUT /v1/live/reminders/{id}' : 'DELETE /v1/live/reminders/{id}', {id: programme, signal}).then(() => undefined);

/** Counts, space and hours left, the keep rule, and whether this profile may record. */
export const readDvr = (api: ServerRequester, signal?: AbortSignal): Promise<DvrOverview> => callServer(api, 'GET /v1/dvr', {signal});
/** Upcoming bookings (conflicts first) or History; this profile's own, or everyone's for the owner. */
export const readRecordings = (api: ServerRequester, which: Readonly<{history?: boolean; everyone?: boolean}> = {}, cursor?: string, signal?: AbortSignal): Promise<DvrRecordingPage> =>
  callServer(api, 'GET /v1/dvr/recordings', {query: {...(which.history ? {state: 'history'} : {}), ...(which.everyone ? {scope: 'everyone'} : {}), cursor}, signal});
/** Books an airing. One someone else booked answers that booking, with this profile added to it. */
export const bookRecording = (api: ServerRequester, book: DvrRecordingBook, signal?: AbortSignal): Promise<DvrRecording> =>
  callServer(api, 'POST /v1/dvr/recordings', {body: book, signal});
export const readRecording = (api: ServerRequester, id: string, signal?: AbortSignal): Promise<DvrRecording> =>
  callServer(api, 'GET /v1/dvr/recordings/{id}', {id, signal});
/** Padding, priority, Keep, or a conflict's choice ("Record this instead", "Skip this one"). */
export const changeRecording = (api: ServerRequester, id: string, change: DvrRecordingChange, signal?: AbortSignal): Promise<DvrRecording> =>
  callServer(api, 'PATCH /v1/dvr/recordings/{id}', {id, body: change, signal});
/**
 * Cancels a booking (it passes to the next profile that asked for it), stops one that is
 * recording (what was captured is kept), or deletes a recording and its file.
 */
export const endRecording = (api: ServerRequester, id: string, how: 'cancel' | 'stop' | 'delete', signal?: AbortSignal): Promise<void> =>
  callServer(api, 'DELETE /v1/dvr/recordings/{id}', {id, query: {action: how === 'stop' ? 'stop' : undefined}, signal}).then(() => undefined);

/** Series rules: this profile's own, or everyone's for the owner. */
export const readRules = (api: ServerRequester, everyone = false, cursor?: string, signal?: AbortSignal): Promise<DvrRulePage> =>
  callServer(api, 'GET /v1/dvr/rules', {query: {...(everyone ? {scope: 'everyone'} : {}), cursor}, signal});
/** Makes a rule from a programme; with `preview` it answers what it would record and makes nothing. */
export const saveRule = (api: ServerRequester, rule: DvrRuleSave, preview = false, signal?: AbortSignal): Promise<DvrRule> =>
  callServer(api, 'POST /v1/dvr/rules', {query: {...(preview ? {preview: true} : {})}, body: rule, signal});
/** Edits, pauses or resumes a rule. */
export const changeRule = (api: ServerRequester, id: string, rule: DvrRuleSave, signal?: AbortSignal): Promise<DvrRule> =>
  callServer(api, 'PATCH /v1/dvr/rules/{id}', {id, body: rule, signal});
/** Deletes a rule, keeping or cancelling what it has booked. */
export const removeRule = (api: ServerRequester, id: string, bookings: 'keep' | 'cancel', signal?: AbortSignal): Promise<void> =>
  callServer(api, 'DELETE /v1/dvr/rules/{id}', {id, query: {bookings}, signal}).then(() => undefined);

/** Upcoming bookings in the order the screen shows them: conflicts first, then by start. */
export function upcomingOrder(recordings: readonly DvrRecording[]): DvrRecording[] {
  return [...recordings].sort((a, b) => Number(!!b.conflict) - Number(!!a.conflict) || a.startsAt.localeCompare(b.startsAt));
}
