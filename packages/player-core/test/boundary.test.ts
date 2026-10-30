import test from 'node:test';import assert from 'node:assert/strict';import {mkdtempSync,mkdirSync,copyFileSync,writeFileSync,rmSync} from 'node:fs';import {tmpdir} from 'node:os';import path from 'node:path';import {spawnSync} from 'node:child_process';
test('headless boundary rejects renderer imports, external modules, process state and backend paths',()=>{
 const dir=mkdtempSync(path.join(tmpdir(),'player-boundary-'));try{
 mkdirSync(path.join(dir,'scripts'));mkdirSync(path.join(dir,'src'));copyFileSync(new URL('../scripts/check-boundary.mjs',import.meta.url),path.join(dir,'scripts/check-boundary.mjs'));
 const run=()=>spawnSync(process.execPath,[path.join(dir,'scripts/check-boundary.mjs')],{encoding:'utf8'});
 writeFileSync(path.join(dir,'src/index.ts'),"export {x} from './state.ts';\n");assert.equal(run().status,0);
 for(const source of ["import '../web/Player.tsx';","export {x} from '@core/index';","const x=import('react');","const p=process.env;","const p=localStorage.getItem('choice');","const p='/v1/playback';","const p=Date.now();","setTimeout(()=>{},0);"]){writeFileSync(path.join(dir,'src/index.ts'),source);assert.notEqual(run().status,0,source);}
 }finally{rmSync(dir,{recursive:true,force:true});}
});
