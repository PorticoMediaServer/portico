import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi} from '../src/index.ts';
test('a report log is authenticated plain text; an HTML response is refused',async()=>{
 let path='',auth:string|null=null;
 const api=new HttpLocalApi('http://127.0.0.1:19447','fixture-token',async(input,init)=>{path=String(input);auth=new Headers(init?.headers).get('Authorization');return new Response('consented log',{headers:{'Content-Type':'text/plain; charset=utf-8'}});});
 assert.equal(await api.readReportLog('rpt_a/b'),'consented log');assert.match(path,/reports\/rpt_a%2Fb\/log$/);assert.equal(auth,'Bearer fixture-token');
 const wrong=new HttpLocalApi('http://127.0.0.1:19447','fixture-token',async()=>new Response('<html/>',{headers:{'Content-Type':'text/html'}}));await assert.rejects(wrong.readReportLog('rpt_one'));
});

test('Messages renews a refused stream opening without consuming the event body', async () => {
 const sent: string[] = [];
 const api = new HttpLocalApi('https://fixture.invalid', 'expired', async (_url, init) => {
  const headers = new Headers(init?.headers);
  assert.equal(headers.get('Accept'), 'text/event-stream');
  sent.push(headers.get('Authorization')!);
  return sent.length === 1 ? new Response(null, {status: 401}) : new Response('event: line\n\n');
 });
 const signal = new AbortController().signal;
 api.setAuthRecovery({token: async token => token, recover: async () => 'renewed'});
 const response = await api.openOwnerLogEvents({Accept: 'text/event-stream'}, signal);
 assert.deepEqual(sent, ['Bearer expired', 'Bearer renewed']);
 assert.equal(await response.text(), 'event: line\n\n');
});
