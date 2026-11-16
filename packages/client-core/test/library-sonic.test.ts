import test from 'node:test';
import assert from 'node:assert/strict';
import {readLibrarySonic,saveLibrarySonic} from '../src/server-admin/library-sonic.ts';
import type {LibraryContentApi} from '../src/library-content.ts';

// A music library's Sonic analysis on the rebuilt server: the switch is the library's soundAnalysis option and
// the status line is GET .../sound-analysis (shapes as the demo server answered, 16 Nov). Without this the
// owner's music library page cannot show or change it.
const library=(revision='4',soundAnalysis=true)=>({id:'music',name:'Music',kind:'music',revision,folders:[],
 options:{depth:'basic',trash:{mode:'days',days:30},allowDelete:false,watch:true,rescanMinutes:0,lyrics:'lrclib',soundAnalysis}});
function server(state:{library:ReturnType<typeof library>;status:Record<string,unknown>}){
 const patches:unknown[]=[];
 const api={
  async request<T>(path:string):Promise<T>{return (path.endsWith('/sound-analysis')?state.status:state.library) as T;},
  async requestRaw(path:string,method:string,options:{headers?:Record<string,string>;body?:any}={}){
   patches.push({path,method,ifMatch:options.headers?.['If-Match'],body:options.body});
   if(options.headers?.['If-Match']!==`"${state.library.revision}"`)return {status:412,headers:{},body:{error:{code:'changed_elsewhere',message:'Changed elsewhere.'}}};
   state.library={...state.library,...options.body,revision:String(Number(state.library.revision)+1)};
   state.status={...state.status,on:options.body.options.soundAnalysis,state:options.body.options.soundAnalysis?'analyzing':'off'};
   return {status:200,headers:{},body:state.library};
  },
 } as unknown as LibraryContentApi;
 return {api,patches};
}

test('Sonic reads the library revision and the server status line in the page words',async()=>{
 const {api}=server({library:library(),status:{available:true,on:true,state:'paused',songs:20,analyzed:5,pausedUntil:'2026-11-17T01:00:00Z',secondsLeft:600}});
 const doc=await readLibrarySonic(api,'music');
 assert.deepEqual({...doc,observedAtMs:0},{libraryId:'music',revision:4,enabled:true,defaultEnabled:true,available:true,state:'paused',done:5,total:20,observedAtMs:0,
  pausedUntilMs:Date.parse('2026-11-17T01:00:00Z'),estimatedRemainingSeconds:600});
 const {api:cant}=server({library:library(),status:{available:false,on:true,state:'cant_run',songs:0,analyzed:0}});
 assert.equal((await readLibrarySonic(cant,'music')).state,'unavailable');
});

test('turning Sonic off patches only that option at the revision read, and a changed library is refused',async()=>{
 const state={library:library(),status:{available:true,on:true,state:'analyzing',songs:20,analyzed:5}};
 const {api,patches}=server(state);
 const doc=await readLibrarySonic(api,'music');
 const saved=await saveLibrarySonic(api,doc,false);
 assert.deepEqual(patches,[{path:'/v1/admin/libraries/music',method:'PATCH',ifMatch:'"4"',body:{options:{...library().options,soundAnalysis:false}}}]);
 assert.equal(saved.enabled,false);assert.equal(saved.revision,5);assert.equal(saved.state,'disabled');
 await assert.rejects(saveLibrarySonic(api,doc,true),(error:any)=>error.code==='changed_elsewhere');
});
