import test from 'node:test';
import assert from 'node:assert/strict';
import {SettingsController} from '../src/screens/settings/controller.ts';
import {PreferencesReader} from '@core/preferences-reader.ts';
import {settingsRowIndex, settingsSpec} from '@core/presentation/settings-spec.ts';

test('the web backup rows load and save through the existing rebuilt backup source', async () => {
 let values = {days:[0,1,2,3,4,5,6],hour:3,keep:7,folder:'',includeArtwork:false};
 const writes: unknown[] = [];
 const api = {request: async <T>(path:string,method?:string,body?:unknown) => {
  if (path === '/v1/admin/backups') return {items:[],folder:'/backups',folderWritable:true} as T;
  if (path === '/v1/admin/settings/backups') {
   if (method==='PUT') {writes.push(body);values={...values,...body as object};}
   return {values} as T;
  }
  throw Object.assign(new Error('Not on rebuilt server'),{status:404,code:'not_found'});
 }};
 const reader = new PreferencesReader(api), preferences = {deviceClass:'web' as const,registry:{revision:'test',fields:[]},documents:[],effective:{},effectiveSource:{},clampedFields:[],registryRevision:'test'};
 const controller = new SettingsController({api,reader,preferences,serverId:'s',viewerId:'v',role:'owner',mainProfile:true,
  capabilities:{manageServer:true,managePeople:true,manageSignIn:true},environment:{platform:'web',os:'browser'},isCurrent:()=>true,reportError:assert.fail,
  reading:{read:()=>undefined,patch:()=>({})},eligibility:{library:()=>undefined,hasEngine:()=>true}});
 const section = settingsSpec.find(section=>section.scope==='server'&&section.id==='storage')!, row=(id:string)=>settingsRowIndex.get(`storage.${id}`)!.row;
 try {
  await controller.load(section);
  assert.equal(controller.rowState(row('backupTime')).value,'3');assert.equal(controller.rowState(row('backupKeep')).value,7);
  const change = (id:string,value:any) => new Promise<void>(resolve=>{
   const off=controller.subscribe(()=>{if (!controller.rowState(row(id)).saving) {off();resolve();}});
   controller.change(row(id),value);
  });
  await change('backupSchedule','custom');assert.equal(controller.rowState(row('backupSchedule')).value,'custom');
  assert.deepEqual(writes,[],'opening the day picker is not a server schedule');
  const days=controller.rowState(row('backupDays')).value as Record<string,string>;
  await change('backupDays',{...days,sunday:'skips'});assert.deepEqual(writes.at(-1),{days:[1,2,3,4,5,6]});
  await change('backupTime','22');await change('backupKeep',365);await change('backupSchedule','never');
  assert.equal(values.hour,22);assert.equal(values.keep,365);assert.deepEqual(values.days,[]);
  assert.equal(controller.rowState(row('backupSchedule')).value,'never');
 } finally {controller.dispose();reader.dispose();}
});
