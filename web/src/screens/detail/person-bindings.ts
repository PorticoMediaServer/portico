import {createWindowedCollection, type WindowedCollection, type PageResult} from '@core/collections/index.ts';
import {readPerson, type PersonPage, type PersonCredit, type PersonRole} from '@core/people.ts';
import type {CardFactPresentation} from '../../kit/cards/types';
import {t} from './text';
import {screenLifetime, type ScreenContext} from './context';

const castDepartment = (department: string) => /^(acting|cast)$/i.test(department);

/** One drawn credit row: acting reads "as {character}", every other department keeps its role.
 * The character stays a separate run so the frame's brighter weight is presentation, not copy. */
export function creditRole(role: Readonly<{role: string; department: string}>, id: string) {
  const cast = castDepartment(role.department);
  const text = cast ? t('web.person.as', {character: role.role}) : role.role;
  if (!cast || !role.role) return {part: {id, text, role: 'context' as const}, presentation: undefined};
  const at = text.indexOf(role.role);
  if (at < 0) return {part: {id, text, role: 'context' as const}, presentation: undefined};
  const runs = [
    ...(at > 0 ? [{text: text.slice(0, at)}] : []),
    {text: role.role, emphasis: 'secondary' as const},
    ...(at + role.role.length < text.length ? [{text: text.slice(at + role.role.length)}] : []),
  ];
  return {part: {id, text, role: 'context' as const}, presentation: {[id]: runs} satisfies CardFactPresentation};
}

type PersonSnapshot = Readonly<{phase: 'idle' | 'loading' | 'ready' | 'error'; page?: PersonPage; role: PersonRole; error?: unknown}>;
/** People and credits are server identities. Paging keeps at most five resident pages per group. */
export function createPersonBindings(context: ScreenContext) {
  const life = screenLifetime(context), listeners = new Set<() => void>();
  let generation = 0, personId = '', state: PersonSnapshot = {phase: 'idle', role: 'all'};
  let read: AbortController | undefined;
  const collections = new Map<string, WindowedCollection<PersonCredit>>();
  const collectionUsers = new Map<WindowedCollection<PersonCredit>, number>();
  const publish = (next: PersonSnapshot) => { state = Object.freeze(next); for (const listener of listeners) listener(); };
  const clear = () => { for (const collection of collections.values()) collection.dispose(); collections.clear(); collectionUsers.clear(); };
  const current = (page: PersonPage) => { life.assert(); if (state.phase !== 'ready' || state.page !== page) throw new DOMException('Person changed', 'AbortError'); };
  const select = async (id: string, role: PersonRole = 'all') => {
    life.assert(); const request = ++generation; read?.abort(); clear(); personId = id;
    const controller = new AbortController(); read = controller;
    publish({phase: 'loading', role});
    try {
      const page = await readPerson(life.api, context.scope, id, {role, limit: 1, signal: life.signal(controller.signal)});
      if (request !== generation || !life.current()) return;
      publish({phase: 'ready', role, page});
    } catch (error) { if (request === generation && life.current() && !controller.signal.aborted) publish({phase: 'error', role, error}); }
  };
  const credits = (page: PersonPage, kind?: string) => {
    life.assert(); const header = state.page, role = state.role, request = generation, id = personId;
    if (!header || page !== header || state.phase !== 'ready') throw new Error('Person required');
    const key = JSON.stringify([request, kind, role]);
    const known = collections.get(key); if (known) return known;
    // Cursor addresses are retained; media pages belong only to the sparse collection.
    const cursors = new Map<number, string | null>([[0, null]]), pageSize = 40;
    let total = kind ? header.groups.find(group => group.kind === kind)?.total ?? header.pageInfo.total : header.pageInfo.total;
    let addressablePage = 0;
    const pending = new Map<number, Promise<PageResult<PersonCredit>>>();
    const fetch = async (page: number, signal: AbortSignal): Promise<PageResult<PersonCredit>> => {
      const existing = pending.get(page); if (existing) return existing;
      const work = (async () => {
        // A cursor is an address, not permission to read every intervening page on a jump.
        if (!cursors.has(page)) throw new DOMException('Person cursor unavailable', 'AbortError');
        if (cursors.get(page) === '') return {items: [], total};
        const value = await readPerson(life.api, context.scope, id, {...(kind ? {kind} : {role}), limit: pageSize, cursor: cursors.get(page), signal: life.signal(signal)});
        life.assert();
        if (request !== generation || state.page !== header || value.viewerFence !== header.viewerFence || value.revision.catalog !== header.revision.catalog || value.revision.viewer !== header.revision.viewer) throw new DOMException('Person credits changed', 'AbortError');
        if (value.pageInfo.nextCursor && [...cursors].some(([address, cursor]) => address !== page + 1 && cursor === value.pageInfo.nextCursor)) throw new Error('Repeated person cursor');
        total = value.pageInfo.total; cursors.set(page + 1, value.pageInfo.nextCursor);
        if (value.pageInfo.nextCursor) addressablePage = Math.max(addressablePage, page + 1);
        const exposed = value.pageInfo.nextCursor ? Math.min(total, (addressablePage + 1) * pageSize) : total;
        // The header retains the real group total; the scroll surface grows as addresses arrive.
        return {items: value.credits, total: exposed, totalIsLowerBound: exposed < total};
      })();
      pending.set(page, work);
      try { return await work; } finally { if (pending.get(page) === work) pending.delete(page); }
    };
    const collection = createWindowedCollection<PersonCredit>({pageSize, maxConcurrent: 1, maxResidentPages: 5, keyOf: credit => JSON.stringify([credit.media.libraryId, credit.media.id]), fetchPage: (start, _count, signal) => fetch(Math.floor(start / pageSize), signal)});
    collections.set(key, collection); return collection;
  };
  const acquireCredits = (page: PersonPage, kind?: string) => {
    current(page);
    const collection = credits(page, kind), request = generation;
    collectionUsers.set(collection, (collectionUsers.get(collection) ?? 0) + 1);
    let active = true;
    return {
      collection,
      current: () => active && life.current() && generation === request && state.phase === 'ready' && state.page === page,
      release() {
        if (!active) return;
        active = false;
        const users = collectionUsers.get(collection);
        // A select/dispose already retired this exact object. An old cleanup
        // must never remove or dispose a successor collection for the same key.
        if (users === undefined) return;
        if (users > 1) {collectionUsers.set(collection, users - 1);return;}
        collectionUsers.delete(collection);
        for (const [key, held] of collections) if (held === collection) collections.delete(key);
        collection.dispose();
      },
    };
  };
  return {
    context, select, credits, acquireCredits, current,
    getSnapshot: () => state,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    openKnownFor(page: PersonPage, entry: PersonCredit['media']) {
      current(page);
      if (!page.person.knownFor.includes(entry)) throw new DOMException('Person title changed', 'AbortError');
      context.effects.openEntry(entry);
    },
    open(page: PersonPage, credit: PersonCredit) {
      current(page);
      if (state.page !== page || ![...collections.values()].some(collection => collection.residentItems().includes(credit))) throw new DOMException('Person title changed', 'AbortError');
      context.effects.openEntry(credit.media);
    },
    retry: (expected = state) => {
      life.assert();
      if (state !== expected) throw new DOMException('Person request changed', 'AbortError');
      return select(personId, expected.role);
    },
    cancel() { generation++; read?.abort(); clear(); personId = ''; publish({phase: 'idle', role: 'all'}); },
    dispose() { generation++; life.dispose(); read?.abort(); clear(); personId = ''; publish({phase: 'idle', role: 'all'}); listeners.clear(); },
  };
}
export type PersonBindings = ReturnType<typeof createPersonBindings>;
