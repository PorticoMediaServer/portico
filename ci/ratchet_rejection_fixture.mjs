// Fail-closed diagnostics must precede cold, cached and invariant-zero answers.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {dirname,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {TypedRatchet} from './typed_ratchet.mjs';
const root=resolve(dirname(fileURLToPath(import.meta.url)),'..');
const config=JSON.stringify({compilerOptions:{target:'ES2022',types:[]},include:['src/**/*']});
const sources={'web/tsconfig.json':config,'web/src/main.ts':"import {value} from './dependency'; export const output=value;",'web/src/dependency.ts':'export const value=1;'};
const request=()=>({files:['web/src/main.ts','web/src/dependency.ts'],configs:['web/tsconfig.json'],
  sources:Object.fromEntries(Object.entries(sources).map(([path,text])=>[path,{text,oid:createHash('sha256').update(text).digest('hex')}]))});
let assertions=0;
for(const kind of ['census','query']) {
  const worker=new TypedRatchet(root,kind);
  try {
    worker.advance(request());worker.advance(request());
    assert.ok(worker.stats.reusedFiles>0);assertions++;
    // The caller is unchanged, syntactically contains no request and already
    // has a cached zero; its imported file is now malformed.
    sources['web/src/dependency.ts']='export const value = "unterminated\n';
    for(let retry=0;retry<2;retry++) {
      assert.throws(()=>worker.advance(request()),/parse\/config diagnostic:.*1002/s);assertions++;
    }
    const cold=new TypedRatchet(root,kind);
    try {assert.throws(()=>cold.advance(request()),/parse\/config diagnostic:.*1002/s);assertions++;}
    finally {cold.close();}
    sources['web/src/dependency.ts']='export const value=1;';
    worker.advance(request());
    if(kind==='query') {
      for(const invalid of ['{"compilerOptions": {"target": "invalid"}}','{"compilerOptions": {']) {
        sources['web/tsconfig.json']=invalid;
        for(let retry=0;retry<2;retry++) {
          assert.throws(()=>worker.advance(request()),/parse\/config diagnostic|project/i);assertions++;
        }
        sources['web/tsconfig.json']=config;worker.advance(request());
      }
    }
  } finally {worker.close();sources['web/src/dependency.ts']='export const value=1;';sources['web/tsconfig.json']=config;}
}
console.log(JSON.stringify({assertions,result:'fresh, cached, zero and retry diagnostics fail closed'}));
