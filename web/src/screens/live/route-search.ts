export type LiveScreenSearch = Readonly<{sourceId?: string; /** Owners: hidden channels are drawn so they can be shown again. */ hidden?: boolean; channelId?: string; kind?: 'all' | 'live-source' | 'library-channel'; group?: string; q?: string; view?: 'guide' | 'list'; tab?: 'guide' | 'recordings'; dvr?: 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage'; favorites?: boolean; sort?: 'number' | 'name' | 'favorites'}>;
function text(raw: Record<string, unknown>, key: string, max = 256): string | undefined {
  const value = raw[key];
  if (value === undefined) return undefined;
  if (typeof value !== 'string' || !value || value.length > max || /[\x00-\x1f\x7f]/.test(value)) throw new Error('invalid-live-route');
  return value;
}
/** A channel identity never implies a source or provenance. */
export function parseLiveScreenSearch(raw: Record<string, unknown>): LiveScreenSearch {
  const sourceId = text(raw, 'sourceId'), channelId = text(raw, 'channelId'), group = text(raw, 'group', 120), q = text(raw, 'q', 120);
  const view = (['guide', 'list'] as const).find(value => value === raw.view);
  const tab = (['guide', 'recordings'] as const).find(value => value === raw.tab);
  const dvr = (['upcoming', 'recorded', 'rules', 'history', 'storage'] as const).find(value => value === raw.dvr);
  const sort = (['number', 'name', 'favorites'] as const).find(value => value === raw.sort);
  const kind = (['all', 'live-source', 'library-channel'] as const).find(value => value === raw.kind);
  if (raw.view !== undefined && !view || raw.tab !== undefined && !tab || raw.dvr !== undefined && !dvr || raw.sort !== undefined && !sort || raw.kind !== undefined && !kind || raw.favorites !== undefined && typeof raw.favorites !== 'boolean' || raw.hidden !== undefined && typeof raw.hidden !== 'boolean') throw new Error('invalid-live-route');
  return {...(sourceId ? {sourceId} : {}), ...(channelId ? {channelId} : {}), ...(group ? {group} : {}), ...(q ? {q} : {}),
    ...(view ? {view} : {}), ...(tab ? {tab} : {}), ...(dvr ? {dvr} : {}), ...(sort ? {sort} : {}), ...(kind ? {kind} : {}), ...(typeof raw.favorites === 'boolean' ? {favorites: raw.favorites} : {}), ...(raw.hidden === true ? {hidden: true} : {})};
}
