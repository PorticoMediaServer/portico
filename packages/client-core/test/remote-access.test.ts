import test from 'node:test';
import assert from 'node:assert/strict';
import {parseRemoteAccess} from '../src/remote-access.ts';

const config={revision:'3',enabled:true,mapping:true,pcp:true,natpmp:true,upnp:true,publicPort:32500,gateway:''};
const candidate={baseUrl:'https://media.example.com',class:'public',state:'verified'};
const mapping={protocol:'upnp',state:'active',externalPort:32500,expiresAt:'2026-10-13T12:00:00Z'};
const status=(patch:Record<string,unknown>={})=>({authorityId:'a',generation:'1',state:'lan_only',config,topology:{lan:[],public:[],bind:''},candidates:[candidate],mappings:[mapping],...patch});

test('an address or a mapping this app cannot read is left out and the rest of the status is kept',()=>{
 const parsed=parseRemoteAccess(status({state:'orbiting',futureField:1,candidates:[candidate,{...candidate,baseUrl:'https://relay.example.com',class:'relay'},{...candidate,baseUrl:'https://media.example.com/path'},{...candidate,baseUrl:'http://192.168.1.4:32500',class:'lan',futureField:1},'nonsense'],mappings:[mapping,{...mapping,externalPort:'32500'},null]}));
 assert.deepEqual(parsed.candidates.map(row=>[row.baseUrl,row.class]),[['https://media.example.com','public'],['http://192.168.1.4:32500','lan']]);
 assert.deepEqual(parsed.mappings,[mapping]);
 assert.equal(parsed.state,'orbiting');
 assert.equal(parsed.config.revision,'3');
});

test('the settings a save sends back, the generation and the list sizes still refuse the whole status',()=>{
 for(const patch of [{config:{...config,revision:'0'}},{config:{...config,enabled:'yes'}},{config:{...config,publicPort:70000}},{generation:'-1'},{authorityId:7},{candidates:new Array(81).fill(candidate)},{mappings:new Array(33).fill(mapping)},{candidates:{}},{topology:{lan:[7],public:[],bind:''}},{topology:{lan:new Array(9).fill('192.168.1.4'),public:[],bind:''}}])assert.throws(()=>parseRemoteAccess(status(patch)),JSON.stringify(patch));
 assert.throws(()=>parseRemoteAccess(null));
});
