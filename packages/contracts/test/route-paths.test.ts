import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import * as paths from '../src/route-paths.ts';
import * as metadata from '../src/routes.ts';
import * as developmentPaths from '../src/development/route-paths.ts';
import * as developmentMetadata from '../src/development/routes.ts';

test('path-only builders preserve the metadata module API and encoding', () => {
 assert.equal(paths.routePaths, metadata.routePaths);
 assert.equal(paths.routePath, metadata.routePath);
 assert.equal(paths.encodedRoutePath, metadata.encodedRoutePath);
 assert.equal(paths.routePath('get_system'), '/v1/system');
 assert.equal(metadata.routePath('get_system'), paths.routePath('get_system'));
 const raw='a/b?c#d\\e% café', encoded=encodeURIComponent(raw);
 assert.equal(paths.routePath('get_items_id_detail',{id:raw}), `/v1/items/${encoded}/detail`);
 assert.equal(paths.routePath('get_items_id_detail',{id:raw}), metadata.routePath('get_items_id_detail',{id:raw}));
 assert.equal(paths.encodedRoutePath('get_items_id_detail',{id:encoded}), paths.routePath('get_items_id_detail',{id:raw}));
 assert.equal(paths.encodedRoutePath('get_items_id_detail',{id:encoded}), metadata.encodedRoutePath('get_items_id_detail',{id:encoded}));
 assert.equal(paths.routePath('remove_queue_entry',{id:42,entryId:raw}), `/v1/queues/42/entries/${encoded}`);
 assert.equal(paths.routePath('remove_queue_entry',{id:42,entryId:raw}), metadata.routePath('remove_queue_entry',{id:42,entryId:raw}));
 assert.equal(paths.encodedRoutePath('remove_queue_entry',{id:42,entryId:encoded}), metadata.encodedRoutePath('remove_queue_entry',{id:42,entryId:encoded}));
 for(const api of [paths,metadata]) {
  assert.throws(()=>api.routePath('get_items_id_detail',{id:''}));
  assert.throws(()=>api.routePath('get_items_id_detail',{id:'..'}));
  assert.throws(()=>api.encodedRoutePath('get_items_id_detail',{id:'%2e%2e'}));
  assert.throws(()=>api.encodedRoutePath('get_items_id_detail',{id:'a/b'}));
  assert.throws(()=>api.routePath('get_system',{extra:'value'} as never));
  assert.throws(()=>api.routePath('unknown' as never));
 }
});

test('development builders share the same path-only module', () => {
 assert.equal(developmentPaths.routePaths, developmentMetadata.routePaths);
 assert.equal(developmentPaths.routePath, developmentMetadata.routePath);
 assert.equal(developmentPaths.routePath('post_dev_e2e_sign_in'), '/v1/dev/e2e/sign-in');
 assert.equal(developmentPaths.routePath('post_dev_e2e_sign_in'), developmentMetadata.routePath('post_dev_e2e_sign_in'));
 assert.equal(developmentPaths.encodedRoutePath('post_dev_e2e_sign_in'), developmentMetadata.encodedRoutePath('post_dev_e2e_sign_in'));
});

test('path-only modules load and build paths with metadata and DTO runtime imports blocked', () => {
 for(const directory of ['', 'development/']) {
  const pathURL=new URL(`../src/${directory}route-paths.ts`,import.meta.url).href;
  const metadataURL=new URL(`../src/${directory}routes.ts`,import.meta.url).href;
  const result=spawnSync(process.execPath,['--experimental-strip-types','--input-type=module','-e',`
   import {registerHooks} from 'node:module';
   import assert from 'node:assert/strict';
   registerHooks({load(url,context,nextLoad) {
    if(url.endsWith('/routes.ts') || url.endsWith('/types.ts')) throw new Error('Blocked runtime contract dependency');
    return nextLoad(url,context);
   }});
   await assert.rejects(import(${JSON.stringify(metadataURL)}), /Blocked runtime contract dependency/);
   const api=await import(${JSON.stringify(pathURL)});
   assert.equal(api.routePath(${JSON.stringify(directory ? 'post_dev_e2e_sign_in' : 'get_system')}), ${JSON.stringify(directory ? '/v1/dev/e2e/sign-in' : '/v1/system')});
   assert.equal(api.encodedRoutePath(${JSON.stringify(directory ? 'post_dev_e2e_sign_in' : 'get_system')}), ${JSON.stringify(directory ? '/v1/dev/e2e/sign-in' : '/v1/system')});
  `],{encoding:'utf8'});
  assert.ifError(result.error);
  assert.equal(result.status,0,result.stderr || result.stdout);
 }
});
