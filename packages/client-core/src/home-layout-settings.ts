/**
 * Customize Home on the rebuilt server: the profile's row order and hidden rows are two of its own
 * settings, `home.rowOrder` and `home.hiddenRowIds`, saved with `PATCH /v1/preferences` like any
 * other (kept on the device while the server is out of reach, the later of two changes winning) and
 * applied by `GET /v1/home`. There is no revision: the later save wins.
 *
 * `GET /v1/home` leaves a hidden row out altogether, so the editor names a hidden row by the title
 * the app last saw it under, and by its id when it never saw it.
 */
import {homeDocument, type HomeLayout, type HomeLayoutView, type HomeLayoutRowView} from './home.ts';
import {readHomeRows} from './items.ts';
import {parsePreferenceSnapshot, type DeviceClass} from './preferences.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';

const ORDER = 'home.rowOrder', HIDDEN = 'home.hiddenRowIds';
/** Titles of rows seen on Home in this run of the app, so a row hidden later can still be named. */
const seenTitles = new Map<string, string>();
const kept = (error: unknown) => (error as {code?: unknown} | null)?.code === 'sync_pending';
const list = (value: unknown): readonly string[] => Array.isArray(value) ? Object.freeze(value.filter((id): id is string => typeof id === 'string')) : Object.freeze([]);

/** Home's rows and the saved layout, as the editor draws them: every row Home shows, then each hidden one. */
export async function readHomeLayoutSettings(api: ServerRequester, deviceClass: DeviceClass, signal?: AbortSignal): Promise<HomeLayoutView> {
  const [home, preferences] = await Promise.all([readHomeRows(api, signal), callServer(api, 'GET /v1/preferences', {query: {deviceClass}, signal})]);
  const settings = parsePreferenceSnapshot(preferences), document = homeDocument(home);
  const rowOrder = list(settings.effective[ORDER]), hiddenRowIds = list(settings.effective[HIDDEN]);
  const rows: HomeLayoutRowView[] = document.rows.map(row => {
    seenTitles.set(row.id, row.title);
    return Object.freeze({id: row.id, title: row.title, kind: row.kind, artworkShape: row.artworkShape, required: row.required, hideable: row.hideable, reorderable: row.reorderable, hidden: false});
  });
  for (const id of hiddenRowIds) if (!rows.some(row => row.id === id)) {
    rows.push(Object.freeze({id, title: seenTitles.get(id) ?? id, kind: id, artworkShape: 'poster', required: false, hideable: true, reorderable: true, hidden: true}));
  }
  return Object.freeze({revision: 0, rowOrder, hiddenRowIds, rows: Object.freeze(rows)});
}

/** Saves the order and the hidden rows (`null` for both takes the layout away, which is Reset). */
export async function saveHomeLayoutSettings(api: ServerRequester, deviceClass: DeviceClass, layout: Readonly<{rowOrder: readonly string[]; hiddenRowIds: readonly string[]}> | null, signal?: AbortSignal): Promise<HomeLayout> {
  const values = layout ? {[ORDER]: [...layout.rowOrder], [HIDDEN]: [...layout.hiddenRowIds]} : {[ORDER]: null, [HIDDEN]: null};
  try {await callServer(api, 'PATCH /v1/preferences', {body: {scope: 'profile-server', deviceClass, values}, signal});}
  // Kept on this device while the server is out of reach: it is sent later, and Home follows then.
  catch (error) {if (!kept(error)) throw error;}
  return Object.freeze({revision: 0, rowOrder: Object.freeze([...(layout?.rowOrder ?? [])]), hiddenRowIds: Object.freeze([...(layout?.hiddenRowIds ?? [])])});
}
