import {test} from 'node:test';
import assert from 'node:assert/strict';
import {parseWorkspaceLink,workspaceLinkPath} from '../src/workspace-links.ts';
import type {LibraryRoute} from '../src/library-navigation.ts';
const parse=(path:string)=>parseWorkspaceLink(new URL(path,'https://web.getportico.tv'));
test('workspace destinations round trip without losing settings, library or resource context',()=>{
 const routes:LibraryRoute[]=[{kind:'home'},{kind:'settings',section:'maintenance'},{kind:'account',section:'account'},{kind:'downloads'},{kind:'library',libraryId:'lib-1',tab:'movies'},{kind:'detail',libraryId:'lib-1',itemId:'movie1'},{kind:'entity',libraryId:'lib-1',entityId:'album1',view:'album'},{kind:'entity',libraryId:'lib-1',entityId:'show1',view:'show'},{kind:'playlist',playlistId:'list1'},{kind:'saved',view:'resource',resourceId:'list1'},{kind:'dvr',view:'recorded'}];
 for(const route of routes)assert.deepEqual(parse(workspaceLinkPath('server1',route))?.route,route);
});
test('untrusted routes cannot introduce credentials, unknown settings, paths or repeated parameters',()=>{
 for(const path of ['/servers/server1/settings?section=runtime&section=support','/servers/server1/settings?section=admin-root','/servers/server1/home?token=secret','/servers/server1/libraries/lib/items/%2Fetc','/servers/server1/libraries/lib/items/%ZZ','/servers/server1/saved?view=resource','/servers/server1/saved?view=watchlist&resource=x','/servers/server1/home#token','https://user:pass@web.getportico.tv/servers/server1/home'])assert.equal(parse(path),null,path);
});
test('links retain server identity and selected episode without choosing an account',()=>{
 const result=parse('/servers/other-server/libraries/lib/shows/show?season=season&episode=episode');
 assert.equal(result?.serverId,'other-server');assert.deepEqual(result?.showTarget,{libraryId:'lib',showId:'show',selectedSeasonId:'season',episodeId:'episode'});
});

test('account subpages round trip through the unified settings destination registry',()=>{for(const section of ['account-profiles','account-sessions','account-membership'] as const){const route={kind:'account' as const,section};assert.deepEqual(parse(workspaceLinkPath('server',route)),{serverId:'server',route});}});
