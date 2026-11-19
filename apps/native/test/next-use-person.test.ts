import test from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../../packages/i18n/src';
import * as errors from '../../../packages/client-core/src/presentation/errors';
import {hooks, screenModule} from './helpers/screen-harness.mjs';

// `usePerson` is what a person's page shows (phone-detail d3 frame 7), from the old person screen's reads: this holds
// what it shows and what each control does.

const t = defaultI18n.t;
const dune = {id: 'm1', kind: 'movie', title: 'Dune', libraryId: 'movies', year: 2021, posterUrl: '/dune.jpg', backdropUrl: '/dune-bd.jpg'} as any;
const silo = {id: 's1', kind: 'show', title: 'Silo', libraryId: 'tv', year: 2023, posterUrl: '/silo.jpg'} as any;

async function person(over: {person?: Record<string, unknown>; role?: string; credits?: Record<string, unknown>; share?: boolean; form?: 'phone' | 'wide'} = {}) {
  const calls: unknown[][] = [], h = hooks();
  const record = (name: string) => (...args: unknown[]) => {calls.push([name, ...args]);};
  const {usePerson} = await screenModule(new URL('../src-next/pages/shared/person/usePerson.ts', import.meta.url), {
    react: h.react, '../../../../../../packages/i18n/src': {defaultI18n},
    '../../../../../../packages/client-core/src/presentation/errors': errors,
    '../../../kit/theme': {useTheme: () => ({form: over.form === 'wide' ? 'expanded' : 'phone'})},
    '../../../screens/share/share-actions': {shareLinkActions: () => over.share === false ? [] : [{id: 'share', label: 'Share', icon: 'i-share', onSelect: () => {}}]}});
  const page = {pageInfo: {total: 2}, person: {id: 'p1', name: 'Rebecca Ferguson', roles: ['Acting'], birthDate: '1983-10-19', deathDate: '', biography: 'A Swedish actress.',
    portraitUrl: '', knownFor: [dune, silo], ...over.person}};
  const snapshot = {total: 2, status: 'idle', ...over.credits};
  const collection = {subscribe: () => () => {}, getSnapshot: () => snapshot, retry: record('retry')};
  const props = {page, role: over.role ?? 'all', collection, focusScope: 'person:x',
    runtime: {artwork: (path?: string) => path ? {uri: path} : undefined, ambient: (path?: string) => path ? {url: path} : undefined, reportError: record('error')},
    actions: {menuState: {subscribe: () => () => {}, getSnapshot: () => 0}, open: record('open'), menu: record('menu')},
    selection: {subscribe: () => () => {}, getSnapshot: () => 0, item: () => undefined, start: record('select')}, onRole: record('role'), onBack: record('back')};
  return {view: h.render(() => usePerson(props)), calls, again: (more: Record<string, unknown>) => h.render(() => usePerson({...props, ...more}))};
}

test('a person shows their departments and birth, the biography that opens, Known for, and the glow from their films', async () => {
  const {view, calls} = await person();
  assert.equal(view.name, 'Rebecca Ferguson');
  assert.equal(view.initials, 'RF');
  assert.deepEqual(view.facts.map((fact: any) => fact.role), ['identity', 'context']);
  assert.equal(view.facts[1].text, t('web.person.born', {date: defaultI18n.date('1983-10-19', 'long')}));
  assert.equal(view.biography.readMore.label, t('web.person.readMore'));
  assert.equal(view.biography.readMore.expanded, false);
  assert.deepEqual(view.glow, {url: '/dune-bd.jpg'});
  assert.deepEqual(view.knownFor.items.map((item: any) => [item.title, item.year]), [['Dune', '2021'], ['Silo', '2023']]);
  view.knownFor.items[0].onNavigate(); assert.deepEqual(calls.at(-1), ['open', dune]);
});

test('Credits: the count, All / Cast / Crew, a credit as its character, and Share when the server has a link', async () => {
  const {view, calls} = await person();
  assert.equal(view.credits.count, t('web.person.titlesOnServer', {count: 2}));
  assert.deepEqual(view.credits.choices.map((choice: any) => [choice.id, !!choice.selected]), [['all', true], ['cast', false], ['crew', false]]);
  view.credits.choices[1].onPress(); assert.deepEqual(calls.at(-1), ['role', 'cast']);
  const credit = {media: dune, character: 'Lady Jessica', role: '', creditKind: 'cast'} as any;
  assert.equal(view.credits.grid.item(credit).caption, t('web.person.as', {character: 'Lady Jessica'}));
  assert.equal(view.credits.grid.item({...credit, character: '', role: 'Producer', creditKind: 'crew'}).caption, 'Producer');
  assert.equal(view.share.items.length, 1);
  assert.equal((await person({share: false})).view.share, undefined);
});

test('on TV the credits are a row per department, one row of all when there is none, and the head names roles, place and a poster blur', async () => {
  const {view, again} = await person({person: {roles: ['Directing', 'Production'], birthPlace: 'Cincinnati, Ohio'}});
  assert.deepEqual(view.credits.rows.map((row: any) => [row.id, row.title, row.count]), [['all', t('web.person.credits'), t('web.person.titles', {count: 2})]]);
  const directing = {subscribe: () => () => {}, getSnapshot: () => ({total: 5})}, production = {subscribe: () => () => {}, getSnapshot: () => ({total: 3})};
  const grouped = again({groups: [{id: 'dir', department: 'Directing', total: 5, collection: directing}, {id: 'prod', department: 'Production', total: 3, collection: production}]});
  assert.deepEqual(grouped.credits.rows.map((row: any) => [row.title, row.count, row.collection === directing || row.collection === production]),
    [['Directing', t('web.person.titles', {count: 5}), true], ['Production', t('web.person.titles', {count: 3}), true]]);
  const credit = {media: dune, character: 'Lady Jessica', role: '', creditKind: 'cast'} as any;
  assert.equal(grouped.credits.rows[0].item(credit).caption, t('web.person.as', {character: 'Lady Jessica'}), 'a row draws a credit as the grid does');
  assert.equal(view.roles, defaultI18n.list(['Directing', 'Production']));
  assert.equal(view.birthPlace, 'Cincinnati, Ohio');
  assert.deepEqual(view.mosaic, [{url: '/dune.jpg'}, {url: '/silo.jpg'}]);
});

test('the heading counts the person\'s titles: a filter keeps the All total, not its own count', async () => {
  // The route keeps the All read's total and hands it over when Cast or Crew is drawn again.
  const {again} = await person();
  const crew = again({role: 'crew', total: 2, page: {pageInfo: {total: 0}, person: {id: 'p1', name: 'Rebecca Ferguson', roles: ['Acting'], birthDate: '', deathDate: '', biography: '', portraitUrl: '', knownFor: []}}});
  assert.equal(crew.credits.count, t('web.person.titlesOnServer', {count: 2}));
});

test('no credits under a filter says so; a failed read offers Try again; a tablet leads with Select', async () => {
  const empty = await person({role: 'crew', credits: {total: 0}});
  assert.equal(empty.view.credits.state.kind, 'empty');
  assert.equal(empty.view.credits.state.reason, t('web.person.noCrew'));
  const failed = await person({credits: {status: 'error', error: new Error('boom')}});
  assert.equal(failed.view.credits.state.kind, 'error');
  failed.view.credits.state.action.onPress(); assert.deepEqual(failed.calls.at(-1), ['retry']);
  const tablet = await person({form: 'wide'});
  assert.equal(tablet.view.credits.choices[0].id, 'select');
  tablet.view.credits.choices[0].onPress(); assert.deepEqual(tablet.calls.at(-1), ['select']);
  assert.equal((await person()).view.credits.choices[0].id, 'all');
});
