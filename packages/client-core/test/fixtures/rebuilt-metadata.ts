/**
 * A rebuilt server's metadata documents and `edit_items` jobs, for bulk editor tests: `GET /v1/admin/items/{id}/metadata`
 * answers each title's document; `POST /v1/jobs` records the job and answers it done (or failed, naming no titles, with
 * `fail` titles left unchanged).
 */
export type FakeDoc = Readonly<{kind?: string; title?: string; fields?: Record<string, unknown>; lists?: Record<string, readonly string[]>; revision?: string; locked?: readonly string[]}>;
export function rebuiltMetadata(docs: Record<string, FakeDoc>, options: Readonly<{fail?: readonly string[]; lose?: number}> = {}) {
  const jobs: {kind: string; target: {items?: string[]}; params?: any}[] = [];
  const reads: string[] = [];
  let losses = options.lose ?? 0;
  const doc = (id: string) => {
    const d = docs[id];
    if (!d) throw Object.assign(new Error('not found'), {status: 404, code: 'not_found'});
    const locked = new Set(d.locked ?? []);
    return {itemId: id, kind: d.kind ?? 'movie', revision: d.revision ?? '1',
      fields: Object.fromEntries(Object.entries({title: d.title ?? id, year: 2000, edition: '', contentRating: '', ...d.fields}).map(([k, v]) => [k, {value: v, source: 'file', locked: locked.has(k)}])),
      lists: Object.fromEntries(Object.entries({genres: [], studios: [], countries: [], labels: [], ...d.lists}).map(([k, v]) => [k, {values: [...v], source: 'file', locked: locked.has(k)}])),
      match: {state: 'found', locked: false, ids: {}}};
  };
  const apply = (id: string, params: any) => {
    const d = docs[id] as {fields?: Record<string, unknown>; lists?: Record<string, readonly string[]>};
    const fields = {...d.fields}, lists: Record<string, readonly string[]> = {genres: [], studios: [], countries: [], labels: [], ...d.lists};
    for (const name of ['year', 'edition', 'contentRating']) if (name in params) fields[name] = params[name];
    for (const [name, values] of Object.entries(params.add ?? {}) as [string, string[]][]) lists[name] = [...new Set([...(lists[name] ?? []), ...values])];
    for (const [name, values] of Object.entries(params.remove ?? {}) as [string, string[]][]) lists[name] = (lists[name] ?? []).filter(value => !values.includes(value));
    const edited = [...Object.keys(params).filter(name => ['year', 'edition', 'contentRating'].includes(name)), ...Object.keys(params.add ?? {}), ...Object.keys(params.remove ?? {})].filter(name => name !== 'labels');
    const locked = [...new Set([...(docs[id]!.locked ?? []), ...(params.lock ? edited : [])])];
    docs[id] = {...docs[id], fields, lists, locked, revision: String(Number(docs[id]!.revision ?? '1') + 1)};
  };
  const answer = (path: string, method = 'GET', body?: any): unknown => {
    const read = /^\/v1\/admin\/items\/([^/]+)\/metadata$/.exec(path);
    if (read && method === 'GET') {reads.push(decodeURIComponent(read[1]!)); return doc(decodeURIComponent(read[1]!));}
    if (path === '/v1/jobs' && method === 'POST' && body?.kind === 'edit_items') {
      jobs.push(body);
      if (losses-- > 0) throw new TypeError('connection ended');
      const failed = (body.target.items as string[]).filter(id => options.fail?.includes(id));
      for (const id of body.target.items as string[]) if (!failed.includes(id)) apply(id, body.params);
      return {id: 'job_' + jobs.length, kind: 'edit_items', state: failed.length ? 'failed' : 'done', ...(failed.length ? {error: `Saved ${body.target.items.length - failed.length} of ${body.target.items.length}. Not saved: ${failed.join('; ')}.`} : {})};
    }
    return undefined;
  };
  return {docs, jobs, reads, answer};
}
