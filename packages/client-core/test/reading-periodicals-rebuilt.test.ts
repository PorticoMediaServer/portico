import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, type Viewer} from '../src/index.ts';
const viewer: Viewer = {accountId:'account', profileId:'profile', serverId:'server', authority:'local', role:'member'};
test('periodical groups, years and calendar days use the rebuilt lists and facets', async () => {
 const calls: URL[] = [];
 const wire = {id:'itm_issue', kind:'book', title:'Issue', libraryId:'lib_books', releaseDate:'2026-11-03', images:{}, state:{watched:false}};
 const api = new HttpLocalApi('https://server.example', 'token', async input => {
  const url = new URL(String(input)); calls.push(url);
  const answer = url.pathname === '/v1/items/itm_run' ? {id:'itm_run',title:'Run'}
   : url.pathname === '/v1/facets' ? {facet:url.searchParams.get('facet'),values:[{value:url.searchParams.get('facet')==='issueYear'?'2026':'2026-11-03',label:'',count:1}]}
   : url.searchParams.get('kind')==='series' ? {items:[{id:'itm_run',title:'Run'}],nextCursor:'more'} : {items:[wire],total:1};
  return new Response(JSON.stringify(answer), {headers:{'Content-Type':'application/json'}});
 });
 const client = api.reading({viewer,isCurrent:()=>true});
 const groups = await client.periodicalGroups('lib_books','newspaper');
 assert.equal(groups.groups[0].latest.item.id,'itm_issue'); assert.equal(groups.nextCursor,'more');
 const issues = await client.periodicalIssues('lib_books','itm_run','newspaper',{month:'2026-11'});
 assert.deepEqual(issues.yearChoices,[{value:'2026',count:1}]);
 assert.deepEqual(issues.dayChoices,[{value:'2026-11-03',count:1}]);
 assert.ok(calls.every(url=>url.pathname.startsWith('/v1/')));
 assert.equal(calls.find(url=>url.searchParams.get('limit')==='50')?.searchParams.get('released'),'2026-11..2026-11');
 // A month's calendar asks for its real days: November ends on the 30th (the server refuses the 31st), February 2024 on the 29th.
 assert.equal(calls.find(url=>url.searchParams.get('facet')==='issueDay')?.searchParams.get('released'),'2026-11-01..2026-11-30');
 calls.length = 0; await client.periodicalIssues('lib_books','itm_run','newspaper',{month:'2024-02'});
 assert.equal(calls.find(url=>url.searchParams.get('facet')==='issueDay')?.searchParams.get('released'),'2024-02-01..2024-02-29');
});
test('a magazine archive carries its publisher, how often it comes out, and each issue\'s volume and number', async () => {
 const wire = {id:'itm_issue', kind:'book', title:'Issue', libraryId:'lib_books', releaseDate:'1929-12', index:9, parentIndex:4, images:{}, state:{watched:false}};
 const api = new HttpLocalApi('https://server.example', 'token', async input => {
  const url = new URL(String(input));
  const answer = url.pathname === '/v1/items/itm_run' ? {id:'itm_run',title:'Amazing Stories',publisher:'Experimenter Publishing',frequency:'monthly'}
   : url.pathname === '/v1/facets' ? {facet:url.searchParams.get('facet'),values:[]} : {items:[wire],total:1};
  return new Response(JSON.stringify(answer), {headers:{'Content-Type':'application/json'}});
 });
 const issues = await api.reading({viewer,isCurrent:()=>true}).periodicalIssues('lib_books','itm_run','magazine');
 assert.deepEqual([issues.publisher, issues.frequency], ['Experimenter Publishing', 'monthly']);
 assert.deepEqual([issues.items[0].volume, issues.items[0].number], [4, 9]);
});
