import test from 'node:test';
import assert from 'node:assert/strict';
import {checkGeneratedRoutes, pathLiterals} from '../scripts/check-generated-routes.mjs';
import {routePath, encodedRoutePath} from '../../contracts/src/routes.ts';

test('client source admits no new hand-written API path', () => {
 assert.deepEqual(checkGeneratedRoutes(), []);
 assert.equal(pathLiterals(`const path='/v1/new-action';`).length, 1);
 assert.equal(pathLiterals('const path=`/v1/items/${id}/new-action`;').length, 1);
 assert.equal(pathLiterals('const path=`${origin}/v1/new-action`;').length, 1);
 assert.equal(pathLiterals("const path='https://example.test/v1/new-action';").length, 1);
 assert.deepEqual(pathLiterals(`// '/v1/comment'\n/* '/v1/comment' */\nconst label='A library';`), []);
});
test('generated builders encode raw parameters once and preserve existing encodings', () => {
 assert.equal(routePath('get_items_id_detail', {id:'a b'}), '/v1/items/a%20b/detail');
 assert.equal(encodedRoutePath('get_items_id_detail', {id:'a%20b'}), '/v1/items/a%20b/detail');
 assert.throws(() => routePath('get_items_id_detail', {id:''}));
 assert.throws(() => routePath('get_items_id_detail', {id:'a', extra:'b'} as never));
});

test('the migrated manual paths are the canonical generated routes', () => {
 const encoded=(value:string)=>encodeURIComponent(value);
 assert.equal(routePath('social_post_v1_cast_controllers_validate'),'/v1/cast/controllers/validate');
 assert.equal(routePath('get_reading_search'),'/v1/reading/search');
 assert.equal(encodedRoutePath('post_items_itemId_reading_reread',{itemId:encoded('item/one two')}),'/v1/items/item%2Fone%20two/reading/reread');
 assert.equal(encodedRoutePath('get_libraries_libraryId_reading_mark_all',{libraryId:encoded('lib/one')}),'/v1/libraries/lib%2Fone/reading/mark-all');
 assert.equal(encodedRoutePath('post_libraries_libraryId_reading_mark_all',{libraryId:encoded('lib/one')}),'/v1/libraries/lib%2Fone/reading/mark-all');
 assert.equal(encodedRoutePath('test_storage_source',{id:encoded('source/one')}),'/v1/storage/sources/source%2Fone/test');
});

test('development routes have their own contract and none is a production route',async()=>{
 const development=await import('../../contracts/src/development/routes.ts');
 const production=await import('../../contracts/src/routes.ts');
 assert.equal(development.routes.length,2);
 assert.equal(development.routePath('post_dev_e2e_sign_in'),'/v1/dev/e2e/sign-in');
 for(const route of development.routes) assert.equal(production.routes.some(p=>p.method===route.method&&p.path===route.path),false);
});

test('generated parameters cannot change URL path segments or inject a query', async () => {
 const modules = [
  {api:await import('../../contracts/src/routes.ts'), operation:'get_items_id_detail'},
  {api:await import('../../contracts/src/hosted/routes.ts'), operation:'account_notification_get'},
 ];
 for (const {api,operation} of modules) {
  // Exercise the shared runtime through separately generated server/Hosted modules.
  const raw = api.routePath as (id:string, parameters:{id:string})=>string;
  const encoded = api.encodedRoutePath as (id:string, parameters:{id:string})=>string;
  for (const id of ['.', '..']) assert.throws(() => raw(operation,{id}), `${operation}: raw ${id}`);
  for (const id of ['.', '..', '%2e', '%2E%2e', '.%2e', '%2e.', 'a/b', 'a\\b', 'a?x=1', 'a#b', 'a\nb', 'a b', '%', '%ff']) {
   assert.throws(() => encoded(operation,{id}), `${operation}: encoded ${id}`);
  }
  const parameter='a/b?c#d\\e% f';
  const path=raw(operation,{id:parameter});
  const url=new URL(path,'https://server.example');
  assert.equal(url.search,''); assert.equal(url.hash,'');
  assert.ok(url.pathname.includes(encodeURIComponent(parameter)));
  assert.equal(encoded(operation,{id:encodeURIComponent(parameter)}),path);
  assert.ok(new URL(raw(operation,{id:'%2e%2e'}),'https://server.example').pathname.includes('%252e%252e'));
 }
});
