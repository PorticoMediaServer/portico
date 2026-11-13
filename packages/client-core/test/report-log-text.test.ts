import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
test('a report log is authenticated plain text; an HTML response is refused',async()=>{
 let path='',auth:string|null=null;
 const api=new HttpLocalApi('http://127.0.0.1:19447','fixture-token',async(input,init)=>{path=String(input);auth=new Headers(init?.headers).get('Authorization');return new Response('consented log',{headers:{'Content-Type':'text/plain; charset=utf-8'}});});
 assert.equal(await api.readReportLog('rpt_a/b'),'consented log');assert.match(path,/reports\/rpt_a%2Fb\/log$/);assert.equal(auth,'Bearer fixture-token');
 const wrong=new HttpLocalApi('http://127.0.0.1:19447','fixture-token',async()=>new Response('<html/>',{headers:{'Content-Type':'text/html'}}));await assert.rejects(wrong.readReportLog('rpt_one'));
});
