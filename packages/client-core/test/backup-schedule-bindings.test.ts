import test from 'node:test';
import assert from 'node:assert/strict';
import {readSettingsBinding, settingsBinding, settingsBindingPatch} from '../src/presentation/settings-bindings.ts';
import {ServerBackupSource} from '../src/server-admin/server-backups.ts';

const binding = (id: string) => settingsBinding(`storage.${id}`)!;
test('backup days, hour and keep use only the rebuilt backup document, including Never and a single chosen day', async () => {
 let settings = {days:[0,1,2,3,4,5,6],hour:3,keep:7,folder:'',includeArtwork:false};
 const calls: {path:string;method?:string;body?:unknown}[] = [];
 const source = new ServerBackupSource({request: async <T>(path:string,method?:string,body?:unknown) => {
  calls.push({path,method,body});assert.ok(!path.includes('maintenance'));
  if (path === '/v1/admin/backups') return {items:[],folder:'/backups',folderWritable:true} as T;
  assert.equal(path,'/v1/admin/settings/backups');
  if (method==='PUT') settings={...settings,...body as object};
  return {values:settings} as T;
 }},()=>true,()=>{});
 try {
  await source.load();
  const read = (id:string) => readSettingsBinding(binding(id),{forms:{backups:source.getSnapshot().settings!}},`storage.${id}`)?.current;
  const save = async (id:string,value:unknown) => source.saveSettings(settingsBindingPatch(binding(id),value,{forms:{backups:source.getSnapshot().settings!}}).values);
  assert.equal(read('backupSchedule'),'nightly');assert.equal(read('backupTime'),'3');assert.equal(read('backupKeep'),7);
  await save('backupSchedule','never');assert.deepEqual(settings.days,[]);assert.equal(read('backupSchedule'),'never');
  const days = read('backupDays') as Record<string,string>;
  await save('backupDays',{...days,sunday:'runs'});assert.deepEqual(settings.days,[0]);assert.equal(read('backupSchedule'),'custom');
  await save('backupTime','23');await save('backupKeep',365);assert.equal(settings.hour,23);assert.equal(settings.keep,365);
  await save('backupSchedule','nightly');assert.deepEqual(settings.days,[0,1,2,3,4,5,6]);
  assert.equal(settingsBinding('storage.backupWindowLength'),undefined);assert.equal(settingsBinding('storage.backupKeepExact'),undefined);
  for(const [id,value] of [['backupTime','180'],['backupTime',1.5],['backupKeep',0],['backupKeep',366],['backupSchedule','weekly']] as const) assert.throws(()=>settingsBindingPatch(binding(id),value));
  assert.ok(calls.some(call=>call.method==='PUT'));
 } finally {source.dispose();}
});
