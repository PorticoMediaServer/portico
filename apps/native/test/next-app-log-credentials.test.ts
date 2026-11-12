import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

test('the native log clears when the Portico Account sign-in ends, not on initial empty credentials',async()=>{
 let storage: any, cleared=0, stored: unknown;
 const {createCredentials}=await screenModule(new URL('../src-next/platform/credentials.ts',import.meta.url),{
  react:{default:{}},'./app-log':{clearNativeAppLog:async()=>{cleared++;}},'./server-connections':{nativeNetworkFetch:()=>{}},
  'react-native':{NativeModules:{PorticoRuntime:{loadCredentials:async()=>JSON.stringify(stored),saveCredentials:async(_origin:string,text:string)=>{stored=JSON.parse(text);},newRequestId:()=>''}}},
  '../../../../packages/client-core/src/credential-format':{parseNativeCredentialJSON:(raw:string)=>raw?JSON.parse(raw):undefined,parseNativeCredentialRecord:(raw:unknown)=>raw},
  '../../../../packages/client-core/src/index':{CredentialSessionService:class{constructor(_api:unknown,port:unknown){storage=port;}}},
  '../../../../packages/client-core/src/portico-services-native':{PorticoServicesCredentialApi:class{}},
 });
 createCredentials('https://services.example');
 await storage.save({version:1,revocations:[]});assert.equal(cleared,0);
 await storage.save({version:1,active:{session:{}},revocations:[]});
 await storage.save({version:1,revocations:[]});assert.equal(cleared,1);
});
