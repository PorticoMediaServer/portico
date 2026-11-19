import assert from 'node:assert/strict';
import test from 'node:test';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const entry = {id: 'movie', kind: 'movie', libraryId: 'movies', title: 'Actual credit movie (Legacy cut)', displayTitle: 'Stored Edition — The Actual Cut', year: 1994,
  navigation: {view: 'item', entityId: 'movie'}, posterUrl: '/v1/items/movie/poster'};

const fallback = {id: 'fallback-film', kind: 'movie', libraryId: 'movies', title: 'Fallback Film (Extended Cut) [2026]', navigation: {view: 'item', entityId: 'fallback-film'}};

test('real App mounts the handheld Person route: the rebuilt page reads once, draws the person and filters, and Crew re-reads', async () => {
  const reads: string[] = [];
  const app = await standardHomeApp({mountUI: false, nativeResponse: async (request: {path: string}, fixture: {pin: {serverId: string}}) => {
    if (request.path.startsWith('/v1/people/')) return {status: 200, body: {id: 'person-no-library', name: 'Actual Person', biography: 'Actual canonical biography', images: {}, titleCount: 2, crewTitleCount: 0, roles: [{role: 'actor', titleCount: 2}]}};
    if (!request.path.startsWith('/v1/items?person=')) return;
    reads.push(request.path);
    const crew = new URL(request.path, 'https://fixture.invalid').searchParams.get('creditGroup') === 'crew';
    return {status: 200, body: {items: crew ? [] : [entry, fallback].map((media, i) => ({...media, title: 'displayTitle' in media ? media.displayTitle : media.title,
      images: {poster: 'posterUrl' in media ? media.posterUrl : undefined}, credits: [{personId: 'person-no-library', name: 'Actual Person', role: 'actor', character: i === 0 ? 'Actual character' : ''}]})), total: crew ? 0 : 2}};
  }});
  let runtime: RestoredNativeShellRuntime | undefined;
  try {
    const {restoreNativeShellRuntime} = await import('../src-next/shell/bootstrap');
    const {createNativeShellBindings} = await import('../src-next/shell/assembly');
    let context!: NativeShellBootstrapContext;
    runtime = await restoreNativeShellRuntime(value => {context = value; return createNativeShellBindings(value);}, {onAuthorityEnded() {}});
    assert.ok(runtime);
    const route = {kind: 'person', personId: 'person-no-library'} as const;
    assert.equal(context.navigation.open(route, context.authority()), true);
    assert.equal(reads.length, 0);
    await app.mount({runtime}); await app.settle();
    // Phones and tablets draw the rebuilt person page (pages/touch/person): TitleFrame with the person's head and a windowed
    // credit grid (`posterGridScroller`).
    const hosts = () => app.hosts() as any[];
    const page = () => hosts().find(host => host.props.testID === 'native-person');
    const press = (host: any) => host.props.onPress({nativeEvent: {}, defaultPrevented: false, preventDefault() {}});
    const card = (label: string) => hosts().find(host => host.name === 'Pressable' && host.props.accessibilityLabel === label);
    assert.ok(page(), 'the handheld person route draws the rebuilt page');
    assert.equal(reads.length, 1);
    // The words (name, departments, biography, the empty states) are held by next-use-person: this harness keeps no text hosts.
    assert.ok(hosts().some(host => host.props.testID === 'person-credits'), 'the windowed credit grid');
    for (const role of ['all', 'cast', 'crew']) assert.ok(hosts().some(host => host.props.testID === `person-role-${role}`), `the ${role} filter`);
    // A credit card says the title as the server stores it and the character, and opens the title; Back returns to the
    // same page without reading again.
    const first = card(`${entry.displayTitle}, as Actual character`); assert.ok(first, 'the credit card with its character');
    assert.ok(card(`${fallback.title}, Actor`), 'a credit with no character shows the job');
    press(first); await app.settle();
    const title = context.navigation.getSnapshot().route; assert.equal(title.kind, 'title');
    if (title.kind === 'title') assert.equal(title.entry.id, entry.id);
    assert.equal(context.navigation.back(context.authority()), true); await app.settle();
    assert.equal(context.navigation.getSnapshot().route, route); assert.ok(page()); assert.equal(reads.length, 1);
    const crew = hosts().find(host => host.props.testID === 'person-role-crew');
    press(crew); await app.settle();
    assert.equal(new URL(reads.at(-1)!, 'https://fixture.invalid').searchParams.get('creditGroup'), 'crew');
    assert.equal(card(`${entry.displayTitle}, as Actual character`), undefined, 'Crew shows only crew credits (none here)');
    assert.equal(hosts().find(host => host.props.testID === 'person-role-crew')?.props.accessibilityState?.selected, true, 'Crew is the chosen filter');
    assert.equal(context.navigation.back(context.authority()), true); await app.settle();
    assert.equal(page(), undefined, 'Back leaves the person page');
    assert.deepEqual(app.errors, []);
  } finally {
    try {
      await app.unmount();
    } finally {
      try {
        runtime?.dispose();
      } finally {
        app.disposeLoader();
      }
    }
  }
});
