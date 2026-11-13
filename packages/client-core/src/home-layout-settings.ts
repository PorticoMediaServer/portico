/**
 * Customize Home on the rebuilt server: the profile's row order and hidden rows are two of its own
 * settings, `home.rowOrder` and `home.hiddenRowIds`, saved with `PATCH /v1/preferences` like any
 * other (kept on the device while the server is out of reach, the later of two changes winning) and
 * applied by `GET /v1/home`. There is no revision: the later save wins.
 *
 * The editor reads `GET /v1/home?hidden=include`: every row in its place, a hidden one with its title,
 * `hidden: true` and no items. Home itself reads without the query and never sees hidden rows.
 */
import {homeDocument, homeViewResourceId, type HomeLayout, type HomeLayoutView, type HomeLayoutRowView} from './home.ts';
import {parsePreferenceSnapshot, type DeviceClass} from './preferences.ts';
import {callServer, type ServerRequester} from './query/server-api.ts';
import {CodedError} from './server-messages.ts';

const ORDER = 'home.rowOrder', HIDDEN = 'home.hiddenRowIds', VIEWS = 'home.savedViews';
/** The server keeps at most 20 saved views on one Home (`home.savedViews`; 05-browse rule 44). A longer list would fail
 * the whole save, the order and hidden rows with it, so Customize stops adding at this many and the save refuses more. */
export const HOME_SAVED_VIEWS_MAX = 20;
const kept = (error: unknown) => (error as {code?: unknown} | null)?.code === 'sync_pending';
const list = (value: unknown): readonly string[] => Array.isArray(value) ? Object.freeze(value.filter((id): id is string => typeof id === 'string')) : Object.freeze([]);

/** Home's rows and the saved layout, as the editor draws them: every row Home shows, then each hidden one. */
export async function readHomeLayoutSettings(api: ServerRequester, deviceClass: DeviceClass, signal?: AbortSignal): Promise<HomeLayoutView> {
  const [home, preferences] = await Promise.all([callServer(api, 'GET /v1/home', {query: {hidden: 'include'}, signal}), callServer(api, 'GET /v1/preferences', {query: {deviceClass}, signal})]);
  const settings = parsePreferenceSnapshot(preferences);
  const rowOrder = list(settings.effective[ORDER]), hiddenRowIds = list(settings.effective[HIDDEN]);
  const shown = new Map(homeDocument({...home, rows: home.rows.filter(row => !row.hidden)}).rows.map(row => [row.id, row]));
  const rows: HomeLayoutRowView[] = [];
  for (const raw of home.rows) {
    if (typeof raw.id !== 'string' || !raw.id || rows.some(row => row.id === raw.id)) continue;
    const row = shown.get(raw.id);
    if (row) rows.push(Object.freeze({id: row.id, title: row.title, kind: row.kind, artworkShape: row.artworkShape, required: row.required, hideable: row.hideable, reorderable: row.reorderable, hidden: false}));
    // A hidden row comes named with no items; so does a saved view's row the server sent deferred (Home fills only the
    // first few). Both are on Home, so the editor lists them: a save that left them out would take them off.
    else if ((raw.hidden === true || (raw as {deferred?: unknown}).deferred === true) && typeof raw.title === 'string' && raw.title) rows.push(Object.freeze({id: raw.id, title: raw.title, kind: raw.id, artworkShape: raw.shape === 'landscape' || raw.shape === 'square' ? raw.shape : 'poster', required: false, hideable: true, reorderable: true, hidden: raw.hidden === true}));
  }
  return Object.freeze({revision: 0, rowOrder, hiddenRowIds, rows: Object.freeze(rows)});
}

/** Saves the order and the hidden rows (`null` takes the layout away, which is Reset). The server builds a saved
 * view's row only from `home.savedViews`, so the views on Home are saved there too, in Home's order: a view added
 * in Customize stays on Home, and one taken out goes. */
export async function saveHomeLayoutSettings(api: ServerRequester, deviceClass: DeviceClass, layout: Readonly<{rowOrder: readonly string[]; hiddenRowIds: readonly string[]}> | null, signal?: AbortSignal): Promise<HomeLayout> {
  const views = layout?.rowOrder.flatMap(id => homeViewResourceId(id) ?? []);
  if (views && views.length > HOME_SAVED_VIEWS_MAX) throw Object.assign(new CodedError('home_views_full', `Home already has ${HOME_SAVED_VIEWS_MAX} of your saved views.`), {limit: HOME_SAVED_VIEWS_MAX});
  const values = layout ? {[ORDER]: [...layout.rowOrder], [HIDDEN]: [...layout.hiddenRowIds], [VIEWS]: views} : {[ORDER]: null, [HIDDEN]: null, [VIEWS]: null};
  try {await callServer(api, 'PATCH /v1/preferences', {body: {scope: 'profile-server', deviceClass, values}, signal});}
  // Kept on this device while the server is out of reach: it is sent later, and Home follows then.
  catch (error) {if (!kept(error)) throw error;}
  return Object.freeze({revision: 0, rowOrder: Object.freeze([...(layout?.rowOrder ?? [])]), hiddenRowIds: Object.freeze([...(layout?.hiddenRowIds ?? [])])});
}
