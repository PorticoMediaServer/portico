import assert from 'node:assert/strict';
import test from 'node:test';
import type {NativeShellBootstrapContext, RestoredNativeShellRuntime} from '../src-next/shell/bootstrap';
import {standardHomeApp} from './helpers/standard-home-app-harness.mjs';

const elements = (node: any): any[] => Array.isArray(node) ? node.flatMap(elements) : node?.props ? [node, ...elements(node.props.children)] : [];
const style = (value: any): any => Array.isArray(value) ? Object.assign({}, ...value.map(style)) : value ?? {};
const entry = {id: 'movie', kind: 'movie', libraryId: 'movies', title: 'Actual credit movie (Legacy cut)', displayTitle: 'Stored Edition — The Actual Cut', year: 1994,
  navigation: {view: 'item', entityId: 'movie'}, posterUrl: '/v1/items/movie/poster'};

const fallback = {id: 'fallback-film', kind: 'movie', libraryId: 'movies', title: 'Fallback Film (Extended Cut) [2026]', navigation: {view: 'item', entityId: 'fallback-film'}};

test('real App mounts the handheld Person route, exact credit actions and retained native list through Title/Back', async () => {
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
    const list = () => app.hosts().find((host: {name: string}) => host.name === 'VirtualizedList');
    const arrived = list(); assert.ok(arrived); assert.ok(app.hosts().some((host: any) => host.props.testID === 'native-person'));
    assert.equal(reads.length, 1); assert.equal(arrived.props.getItemCount(arrived.props.data), 1);
    const collection = arrived.props.data, original = collection.itemAt(0).media;
    const header = elements(arrived.props.ListHeaderComponent);
    const heading = header.find(node => node.props.children === 'Actual Person'); assert.ok(heading);
    assert.equal(style(heading.props.style).fontSize, 30, 'phone-detail7 prh h1');
    assert.ok(header.some(node => node.props.children === 'Actual canonical biography'));
    const circle = header.find((node: any) => node.props?.initials === 'AP');
    assert.ok(circle, 'the phone person keeps the drawn initials fallback when the server has no portrait');
    assert.equal(circle.props.size, 96); assert.equal(circle.props.source, undefined, 'no invented photo');
    assert.equal(header.some(node => node.props.testID === 'person-known-movie'), false, 'the server does not invent Known for titles');
    assert.ok(header.some(node => node.props.testID === 'person-role-all' && node.props.selected));
    const row = arrived.props.renderItem({item: 0});
    assert.equal(style(row.props.style).paddingHorizontal, 16); assert.equal(style(row.props.style).gap, 10);
    const cardElement = elements(row).find(node => node.props.testID === 'person-credit-0'); assert.ok(cardElement);
    // Resolve the driver after the App loader installs its production React mapping.
    const {createHookHost} = await import('./hook-driver.ts');
    let card: any; const cardHost = createHookHost(() => {card = cardElement.type(cardElement.props);}); cardHost.mount(); cardHost.unmount();
    assert.equal(card.props.title, entry.displayTitle);
    assert.equal(elements(row).find(node => node.props.testID === 'person-credit-1')!.props.title, fallback.title);
    assert.equal(card.props.density, 'grid'); assert.equal(card.props.width, (390 - 32 - 20) / 3);
    assert.equal(card.props.caption[0].text, 'as Actual character');
    card.props.onNavigate(); await app.settle();
    const title = context.navigation.getSnapshot().route; assert.equal(title.kind, 'title');
    if (title.kind === 'title') assert.equal(title.entry, original, 'actual current resident object reaches genuine title');
    assert.equal(context.navigation.back(context.authority()), true); await app.settle();
    assert.equal(context.navigation.getSnapshot().route, route); assert.equal(list()!.host, arrived.host);
    assert.equal(list()!.props.data, collection); assert.equal(reads.length, 1);
    const crew = elements(list()!.props.ListHeaderComponent).find(node => node.props.testID === 'person-role-crew');
    assert.ok(crew); crew.props.onPress(); await app.settle();
    assert.equal(new URL(reads.at(-1)!, 'https://fixture.invalid').searchParams.get('creditGroup'), 'crew');
    assert.equal(list()!.props.getItemCount(list()!.props.data), 0);
    assert.equal(list()!.props.ListEmptyComponent.props.body, 'No crew credits on this server.');
    const before = context.navigation.getSnapshot(); card.props.onNavigate(); assert.equal(context.navigation.getSnapshot(), before, 'replaced resident handle cannot navigate');
    assert.equal(context.navigation.back(context.authority()), true); await app.settle();
    assert.equal(list(), undefined); assert.equal(collection.resident().items, 0);
    const dropped = context.navigation.getSnapshot(); card.props.onNavigate(); assert.equal(context.navigation.getSnapshot(), dropped);
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
