import test from 'node:test';
import assert from 'node:assert/strict';
import {LiveSourceController} from '../src/screens/live/owner/source-controller.ts';
import {RemoteSourceController} from '../src/screens/live/owner/remote-source-controller.ts';
import {UploadSourceController} from '../src/screens/live/owner/upload-source-controller.ts';
import {DiscoveryController} from '../src/screens/live/owner/discovery-controller.ts';
import {discovery, confirmRoots} from '../src/screens/live/owner/checked.ts';
import {rebuiltDirectory as document, rebuiltSource as storedSource, rebuiltPreview as upload, rebuiltPreview as remote, actionReceipt} from './live-owner-fixtures.ts';
const initial = {name: 'Stored source', locator: 'https://provider.example/playlist'};
const discover = {discoveryEnabled: true, devices: [{deviceId: 'tuner', model: 'Tuner', friendlyName: 'Stored tuner', address: '192.168.1.50', baseUrl: 'http://192.168.1.50/', tunerCount: 2, configured: false}]};

test('list retirement clears and notifies, including current=false snapshot reads', async () => {
  let current = true, notifications = 0;
  const controller = new LiveSourceController({async request<T>() {return document as T;}}, 'server', () => current, () => {});
  await controller.load();controller.subscribe(() => {notifications++;assert.equal(controller.getSnapshot().document, undefined);});
  current = false;assert.equal(controller.getSnapshot().document, undefined);assert.equal(notifications, 1);controller.dispose();
});
test('uploaded text is absent after explicit or implicit retirement', async () => {
  for (const explicit of [false, true]) {
    let current = true, notifications = 0;
    const controller = new UploadSourceController({async request<T>() {return upload as T;}}, 'server', () => current, () => {});
    await controller.read('playlist', async () => 'private-playlist');await controller.read('guide', async () => 'private-guide');
    controller.subscribe(() => {notifications++;assert.equal(controller.getSnapshot().draft.playlist, '');assert.equal(controller.getSnapshot().draft.guide, '');});
    if (explicit) controller.dispose();else current = false;
    assert.equal(controller.getSnapshot().draft.playlist, '');assert.equal(controller.getSnapshot().preview, undefined);assert.equal(notifications, 1);
  }
});
for (const error of [{status: 401}, {statusCode: 403}, {code: 'session_expired'}, {code: 'not_permitted'}, {code: 'invalid_token'}, {code: 'SESSION EXPIRED'}, {status: 503, statusCode: 401}]) {
  test(`terminal boundary ${JSON.stringify(error)} clears before notifying/reporting`, async () => {
    let deny = false, reports = 0;
    const api = {async request<T>() {if (deny) throw {...error, password: 'secret'};return document as T;}};
    const controller = new LiveSourceController(api, 'server', () => true, value => {reports++;assert.equal(controller.getSnapshot().document, undefined);assert.equal(JSON.stringify(value).includes('secret'), false);});
    await controller.load();deny = true;await controller.load();assert.equal(reports, 1);assert.equal(controller.getSnapshot().document, undefined);
  });
}
test('synchronous loading and command subscribers can retire with zero subsequent I/O', async () => {
  let reads = 0, writes = 0;
  const api = {async request<T>(_path: string, method?: string) {if (method === 'POST') writes++;else reads++;return (method === 'POST' ? actionReceipt : document) as T;}};
  const list = new LiveSourceController(api, 'server', () => true, () => {});
  list.subscribe(() => {if (list.getSnapshot().loading) list.dispose();});await list.load();assert.equal(reads, 0);
  const command = new LiveSourceController(api, 'server', () => true, () => {});await command.load();
  command.subscribe(() => {if (command.getSnapshot().busy) command.dispose();});await command.act(storedSource, 'remove');assert.equal(writes, 0);
});
test('remote preview/save busy publication can retire before I/O', async () => {
  let calls = 0;
  const api = {async request<T>() {calls++;return remote as T;}};
  const checking = new RemoteSourceController(api, 'server', () => true, () => {});checking.edit(initial);
  checking.subscribe(() => {if (checking.getSnapshot().busy) checking.dispose();});await checking.check();assert.equal(calls, 0);
  const saving = new RemoteSourceController(api, 'server', () => true, () => {});saving.edit(initial);await saving.check();
  saving.subscribe(() => {if (saving.getSnapshot().busy) saving.dispose();});assert.equal(await saving.save(), false);assert.equal(calls, 1);
});
test('upload has zero file I/O when either publication retires its owner', async () => {
  for (const stage of ['before-file', 'reading']) {
    let reads = 0;
    const controller = new UploadSourceController({async request<T>() {throw new Error('unexpected');}}, 'server', () => true, () => {});
    controller.subscribe(() => {if (stage === 'before-file' || controller.getSnapshot().reading) controller.dispose();});
    await controller.read('playlist', async () => {reads++;return 'private';});assert.equal(reads, 0);assert.equal(controller.getSnapshot().draft.playlist, '');
  }
});
test('upload preview/save and discovery cannot invoke API after busy retirement', async () => {
  let calls = 0;
  const api = {async request<T>() {calls++;return upload as T;}};
  const checking = new UploadSourceController(api, 'server', () => true, () => {});checking.edit({name: 'Stored'});await checking.read('playlist', async () => '#EXTM3U');
  checking.subscribe(() => {if (checking.getSnapshot().busy) checking.dispose();});await checking.check();assert.equal(calls, 0);
  const saving = new UploadSourceController(api, 'server', () => true, () => {});saving.edit({name: 'Stored'});await saving.read('playlist', async () => '#EXTM3U');await saving.check();
  saving.subscribe(() => {if (saving.getSnapshot().busy) saving.dispose();});assert.equal(await saving.save(), false);assert.equal(calls, 1);
  const searching = new DiscoveryController(api, 'server', () => true, () => {});searching.subscribe(() => {if (searching.getSnapshot().busy) searching.dispose();});await searching.search();assert.equal(calls, 1);
});
test('a quiet read does not cancel an independent owner command', async () => {
  let complete!:(value:unknown)=>void, signal:AbortSignal|undefined;
  const controller=new LiveSourceController({async request<T>(){return document as T;},requestRaw(_path,_method,options){signal=options?.signal;return new Promise(done=>{complete=done;});}},'server',()=>true,()=>{});
  await controller.load();const command=controller.act(storedSource,'toggle');await controller.refreshQuietly();assert.equal(signal?.aborted,false);
  complete({status:200,headers:{},body:{...storedSource,paused:true,revision:'8'}});assert.equal(await command,true);controller.dispose();
});
test('checked projections reconstruct and drop credential-bearing extras', async () => {
  const {ownerSource,previewOwnerSource}=await import('../../packages/client-core/src/server-admin/live-sources.ts');
  const poison={password:'SECRET',locator:'http://user:SECRET@host/'};
  const source=ownerSource({...storedSource,...poison,report:{...storedSource.report,...poison},config:{...storedSource.config,...poison}});
  const preview=await previewOwnerSource({async request<T>(){return {...upload,...poison,channels:upload.channels.map(row=>({...row,...poison}))} as T;}},{kind:'playlist'});
  const d=discovery({...discover,...poison,devices:discover.devices.map(x=>({...x,...poison}))},'server');
  assert.equal(JSON.stringify([source,preview,d]).includes('SECRET'),false);
  assert.throws(()=>ownerSource({...storedSource,revision:'0'}));
  assert.throws(()=>discovery({...discover,devices:[{...discover.devices[0],baseUrl:'http://user:SECRET@host'}]},'server'));
});
for (const root of ['http://user:SECRET@host', 'http://host/path', 'https://host/?secret=1', 'http://host/#secret', 'file:///tmp/private', 'http://host/\n']) {
  test(`untrusted confirmation root is rejected: ${JSON.stringify(root)}`, async () => {
    assert.throws(() => confirmRoots([root]));
    const controller = new RemoteSourceController({async request<T>(): Promise<T> {throw {code: 'source_lan_confirmation_required', confirmRoots: [root]};}}, 'server', () => true, () => {});
    controller.edit(initial);await controller.check();assert.equal(JSON.stringify(controller.getSnapshot()).includes('SECRET'), false);controller.dispose();
  });
}
test('invalid create responses cannot publish success', async () => {
  const api={async request<T>(){return remote as T;},async requestRaw(){return {status:201,headers:{},body:{saved:true}};}};
  const remoteController=new RemoteSourceController(api,'server',()=>true,()=>{});remoteController.edit(initial);await remoteController.check();assert.equal(await remoteController.save(),false);remoteController.dispose();
  const uploadController=new UploadSourceController(api,'server',()=>true,()=>{});uploadController.edit({name:'Stored'});await uploadController.read('playlist',async()=>'#EXTM3U');await uploadController.check();assert.equal(await uploadController.save(),false);uploadController.dispose();
});

test('terminal auth codes retire upload, remote and discovery before any retained result', async () => {
  for (const make of [
    (api: any) => new UploadSourceController(api, 'server', () => true, () => {}),
    (api: any) => new RemoteSourceController(api, 'server', () => true, () => {}),
    (api: any) => new DiscoveryController(api, 'server', () => true, () => {}),
  ]) {
    const controller = make({async request() {throw {statusCode: 403, password: 'SECRET'};}});
    if (controller instanceof UploadSourceController) {controller.edit({name: 'Stored'});await controller.read('playlist', async () => 'PRIVATE');await controller.check();assert.equal(controller.getSnapshot().draft.playlist, '');}
    else if (controller instanceof RemoteSourceController) {controller.edit({...initial, password: 'PRIVATE'});await controller.check();assert.equal(controller.getSnapshot().draft.password, '');}
    else {await controller.search();assert.equal(controller.getSnapshot().result, undefined);}
    assert.equal(controller.isActive(), false);assert.equal(JSON.stringify(controller.getSnapshot()).includes('SECRET'), false);
  }
});
