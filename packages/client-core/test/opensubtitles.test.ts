import {test} from 'node:test';
import assert from 'node:assert/strict';
import {defaultI18n} from '../../i18n/src/index.ts';
import {metadataSettingsService, openSubtitlesSummary, parseMetadataSettings, parseOpenSubtitlesStatus} from '../src/metadata-settings.ts';
import {providerProblem} from '../src/subtitles.ts';
import {ApiError} from '../src/index.ts';

const base={keySource:'owner',accountSource:'owner',account:'signed_in',problem:'',checkedAt:'2026-10-18T20:00:00Z',retryAt:'',downloadsRemaining:17,downloadsAllowed:20,downloadsResetAt:'2026-10-19T00:00:00Z'};
const t=defaultI18n.t;

test('the OpenSubtitles status is read tolerantly and never carries more than it says',()=>{
 const out=parseMetadataSettings({revision:1,artworkMaxEdge:1920,localArtworkUsedBytes:0,localArtworkLimitBytes:2**31,includeAdult:false,tmdbCredentialConfigured:false,tvdbCredentialConfigured:false,openSubtitlesKeyConfigured:true,openSubtitlesUsername:'me',openSubtitlesPasswordConfigured:true,openSubtitles:{...base,token:'secret'}});
 assert.equal(out.openSubtitles.account,'signed_in');assert.equal('token' in out.openSubtitles,false);
 // An older server sends none: nothing is known, and the page still opens.
 assert.deepEqual(parseOpenSubtitlesStatus(undefined),{keySource:'none',accountSource:'none',account:'none',problem:'',checkedAt:'',retryAt:'',downloadsRemaining:-1,downloadsAllowed:-1,downloadsResetAt:''});
 const odd=parseOpenSubtitlesStatus({keySource:'somewhere',account:'maybe',problem:'Not A Word',downloadsRemaining:1.5,checkedAt:'yesterday'});
 assert.equal(odd.keySource,'none');assert.equal(odd.account,'none');assert.equal(odd.problem,'');assert.equal(odd.downloadsRemaining,-1);assert.equal(odd.checkedAt,'');
});

test('the owner is told whether OpenSubtitles is set up, working, or refusing and why',()=>{
 const now=Date.parse('2026-10-18T20:00:00Z');
 const say=(s:Partial<typeof base>)=>openSubtitlesSummary(parseOpenSubtitlesStatus({...base,...s}),defaultI18n,now);
 assert.deepEqual(say({keySource:'none'}),{tone:'neutral',text:t('server.metadata.openSubtitlesNotSetUp')});
 assert.equal(say({}).text,t('server.metadata.openSubtitlesSignedIn'));assert.equal(say({}).tone,'healthy');
 assert.equal(say({}).downloads,'17 of 20 downloads left today');
 assert.equal(say({account:'none',accountSource:'none',downloadsAllowed:-1,downloadsRemaining:-1}).text,t('server.metadata.openSubtitlesKeyWorks'));
 assert.equal(say({checkedAt:'',account:'not_checked'}).text,t('server.metadata.openSubtitlesNotTested'));
 assert.equal(say({account:'rejected'}).tone,'danger');
 assert.equal(say({problem:'provider_key_rejected'}).text,t('server.metadata.openSubtitlesKeyRejected'));
 assert.equal(say({problem:'provider_sign_in_required',account:'none'}).text,t('server.metadata.openSubtitlesNeedsAccount'));
 assert.equal(say({account:'incomplete',checkedAt:''}).text,t('server.metadata.openSubtitlesNoPassword'));
 const quota=say({problem:'provider_quota_exhausted',downloadsRemaining:0});
 assert.equal(quota.tone,'warning');assert.match(quota.text,/start again at/);assert.equal(quota.downloads,'0 of 20 downloads left today');
 assert.match(say({problem:'provider_rate_limited',retryAt:'2026-10-18T20:00:07Z'}).text,/wait until/);
 assert.equal(say({problem:'provider_unreachable'}).text,t('server.metadata.openSubtitlesUnreachable'));
});

test('Test connection asks the server, which asks OpenSubtitles',async()=>{
 const calls:unknown[]=[];
 const service=metadataSettingsService(async <T,>(path:string,method?:string)=>{calls.push([path,method]);return {...base,problem:'provider_key_rejected'} as T;});
 const status=await service.checkOpenSubtitles();
 assert.deepEqual(calls,[['/v1/admin/metadata/settings/opensubtitles/check','POST']]);assert.equal(status.problem,'provider_key_rejected');
});

test('a viewer hears why online subtitles were refused, and when the quota comes back',()=>{
 const now=Date.parse('2026-10-18T20:00:00Z');
 const quota=providerProblem(new ApiError(429,'subtitle_provider_quota','x',false,4*3600),now);
 assert.ok(quota&&!quota.canRetry);assert.equal(quota.text,t('subtitles.error.providerQuota',{time:defaultI18n.time(now+4*3600_000)}));
 assert.equal(providerProblem(new ApiError(429,'subtitle_provider_quota','x'),now)?.text,t('subtitles.error.providerQuotaLater'));
 assert.equal(providerProblem(new ApiError(503,'subtitle_provider_sign_in_required','x'))?.text,t('subtitles.error.providerSignInRequired'));
 assert.equal(providerProblem(new ApiError(503,'subtitle_provider_not_configured','x'))?.canRetry,false);
 assert.equal(providerProblem(new ApiError(429,'subtitle_provider_busy','x',true,7))?.canRetry,true);
 assert.equal(providerProblem(new ApiError(503,'subtitle_unavailable','x')),undefined);
});
