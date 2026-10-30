import {test} from 'node:test';
import assert from 'node:assert/strict';
import {desktopInstallation} from '../src/installation.ts';
test('installation claim projects only the actual native identity and never fabricates a browser or hardware ID',()=>{
 const claim={installationId:'1'.repeat(64),name:'Studio',platform:'linux',app:'Portico',appVersion:'0.1.0'};
 assert.deepEqual(desktopInstallation({...claim,path:'/private',hardwareId:'not-identity'}),claim);
 assert.equal(desktopInstallation({...claim,name:''}).name,'');
 for(const value of [{...claim,installationId:''},{...claim,platform:'web'},{...claim,app:'Portico Web'},{...claim,appVersion:''},{...claim,name:'private\nname'}])assert.throws(()=>desktopInstallation(value),/device_unavailable/);
});
