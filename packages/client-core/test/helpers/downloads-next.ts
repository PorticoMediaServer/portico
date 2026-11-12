import type {Download, DownloadedItem, DownloadOptions} from '../../../contracts/src/next/api.ts';
export const request = (id = 'dl_one', items = ['itm_film']): Download => ({id, deviceId: 'device', target: {id: items[0]!, kind: items.length > 1 ? 'show' : 'movie', title: 'Film'}, quality: 'original', selection: 'all', paused: false, counts: {titles: items.length, ready: items.length, waiting: 0, failed: 0}, createdAt: '2026-11-12T18:00:00Z', items: items.map(itemId => ({itemId, kind: 'movie', title: itemId, state: 'ready', sizeBytes: 100}))});
export const menu: DownloadOptions = {itemId: 'itm_film', kind: 'movie', titles: 1, default: 'original', roomBytes: 1000, items: [
  {quality: 'original', label: 'Original', sizeBytes: 100, estimated: false, prepared: false, available: true},
  {quality: '720p-low', label: '720p', sizeBytes: 50, estimated: true, prepared: true, available: true},
]};
export const downloaded: DownloadedItem = {item: {id: 'itm_film', kind: 'movie', title: 'Film', libraryId: 'library', images: {}, addedAt: '', durationMs: 60000, state: {positionMs: 12000, watched: false, playCount: 0, favorite: false, inList: false, notInterested: false}}, versionId: 'media_one', quality: 'original', files: [{url: '/v1/download-media/token/file/1', sizeBytes: 100, container: 'mp4'}], audio: [], subtitles: [], chapters: [{startMs: 0, title: 'Beginning'}], markers: []};
export function server(initial: Download[] = [request()]) {
  let items = initial, failure: unknown;
  const calls: {path: string; method: string; body?: any}[] = [];
  const api = {async request<T>(path: string, method = 'GET', body?: any): Promise<T> {
    calls.push({path, method, body}); if (failure) throw failure;
    const url = new URL(path, 'https://server.invalid');
    if (url.pathname === '/v1/downloads/usage') return {copiesBytes: 100, roomBytes: 1000} as T;
    if (url.pathname === '/v1/items/itm_film/download-options') return menu as T;
    if (url.pathname === '/v1/downloads') {
      if (method === 'POST') {const d = {...request(), quality: body.quality}; items = [d]; const {items: _titles, ...summary} = d; return summary as T;}
      return {items} as T;
    }
    const id = url.pathname.split('/')[3], d = items.find(value => value.id === id);
    if (!d) throw Object.assign(new Error('missing'), {status: 404});
    if (url.pathname.includes('/items/')) {
      if (method === 'DELETE') {items = items.map(value => value.id === id ? {...value, items: value.items?.filter(t => t.itemId !== url.pathname.split('/')[5])} : value); return undefined as T;}
      return downloaded as T;
    }
    if (method === 'PATCH') {Object.assign(d, {paused: body.paused ?? d.paused});}
    return d as T;
  }};
  return {api, calls, set: (next: Download[]) => {items = next;}, fail: (error?: unknown) => {failure = error;}};
}
