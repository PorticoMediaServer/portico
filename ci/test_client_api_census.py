import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from client_api_census import ROOT, production, native_sites, classify, compare, base_guard, scan, counts, main, transport_rehash_budgets, BASELINE, ALLOWLIST, APPROVALS, DETECTIONS


class ClientApiCensus(unittest.TestCase):
    def test_all_platform_languages_and_writes_are_in_scope(self):
        for p in ['web/src/request.tsx','apps/native/platform/request.ts','apps/native/Native/Transport.swift',
                  'apps/native/Native/Transport.m','apps/native/android/app/src/main/java/x/Transport.kt',
                  'apps/desktop/src-tauri/src/request.rs','apps/vega/src/bridge/runtime.ts']:
            self.assertTrue(production(p),p)
        for p in ['packages/client-core/src/x.ts','web/test/x.test.ts','apps/native/scripts/tool.ts',
                  'apps/native/test/probe.kt','server/receiver/cast/contracts.js']:
            self.assertFalse(production(p),p)

    def test_native_comments_strings_and_validation_are_not_requests(self):
        source='''// Request.Builder().url("/v1/items")
/* outer /* nested */ requestWithURL:url */
val example = "Request.Builder() /v1/items"
val raw = r###"URLRequest(url: url)"###
if (path.startsWith("/v1/")) return
val api = Request.Builder().url(url).method("DELETE", body).build()
let request = URLRequest(url: url)
NSMutableURLRequest *wire = [NSMutableURLRequest requestWithURL:target];
let sessionTask = session.dataTask(with: request)
'''
        sites=native_sites('apps/native/test-fixture.kt',source)
        self.assertEqual([s['callee'] for s in sites],['Builder','URLRequest','requestWithURL','dataTask'])

    def test_native_dynamic_requests_and_url_construction_count(self):
        sites=native_sites('apps/native/x.kt','PorticoTransport.request(target, method, headers, body)\nval path = String.format("/v1/items/%s", id)\n')
        self.assertEqual([s['kind']for s in sites],['request','url'])
        self.assertEqual(native_sites('apps/desktop/x.rs','#[cfg(test)] mod tests { client.request("/v1/x", "POST"); }'),[])
        tail=native_sites('apps/desktop/x.rs','#[cfg(test)] mod tests { client.request("/v1/test", "POST"); } client.request("/v1/prod", "POST");')
        self.assertEqual(len(tail),1)
        self.assertEqual(native_sites('apps/desktop/x.rs', 'secret.bytes();'),[])

    def test_exact_multiset_ratchet_cannot_move_or_replace_one_site(self):
        self.assertTrue(compare({'new':1},{'old':1})[0])
        self.assertTrue(compare({'old':2},{'old':1})[0])
        self.assertEqual(compare({}, {'old':1}),([],['old']))
        self.assertTrue(base_guard({'new':1},{},{'old':1},{}))

    def test_exceptions_are_exact_pinned_sites_and_cannot_expand_in_a_slice(self):
        e={'apps/native/x.kt':{'category':'native-listening','reason':'JS suspended','sites':['x']}}
        direct,exempt=classify([{'file':'apps/native/x.kt','id':'x'},{'file':'apps/native/x.kt','id':'y'}],e)
        self.assertEqual(len(direct),1);self.assertEqual(len(exempt),1)
        grown=json.loads(json.dumps(e));grown['apps/native/x.kt']['sites'].append('y')
        self.assertTrue(base_guard({},grown,{},e))
        for bad in [{'apps/native/*':e['apps/native/x.kt']},{'apps/native/x.kt':dict(e['apps/native/x.kt'],reason='')}]:
            with self.assertRaises(ValueError):classify([],bad)

    def test_platform_transport_pins_require_unchanged_request_arguments(self):
        e={'web/src/guard.ts':{'category':'platform-transport','reason':'Reviewed exact core-built request guard','sites':['x'],'guardHashes':{'x':'reviewed-hash'}}}
        site={'file':'web/src/guard.ts','id':'x','line':1,'guardHash':'reviewed-hash'}
        with self.assertRaises(ValueError): classify([site],e)
        self.assertEqual(len(classify([dict(site,forwardedRequest=True)],e)[1]),1)
        direct,exempt=classify([dict(site,forwardedRequest=True)]*2,e)
        self.assertEqual((len(direct),len(exempt)),(1,1))
        e['web/src/guard.ts']['additionalSites']={'url':{'category':'platform-url','reason':'core-issued grant bytes'}}
        self.assertEqual(len(classify([{'file':'web/src/guard.ts','id':'url'}],e)[1]),1)

    def test_typescript_real_requests_aliases_writes_and_url_builders(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory); (root/'src').mkdir()
            (root/'src/x.ts').write_text('''
declare function fetch(input: string, init?: object): Promise<unknown>;
declare function routePath(id: string): string;
declare const api: {request<T>(path: string, method?: string, body?: unknown): Promise<T>};
declare const navigator: {locks: {request(name: string, work: () => void): void}};
// fetch('/v1/comment'); routePath('get_comment');
const documentation = "api.request('/v1/string', 'POST')";
type DTO = {path: '/v1/type'};
fetch('/v1/read');
api.request(routePath('put_item'), 'PUT', {});
const {request: renamed} = api;
renamed('/v1/alias', 'DELETE');
const path = routePath('get_byte_url');
if (path === routePath('get_validation')) {}
navigator.locks.request('lock', () => {});
declare function nativeServerFetch(serverId: string): (input: string, init?: object) => Promise<unknown>;
const executor = nativeServerFetch('id');
function diagnose(path: string, method: string) { console.log(path, method); }
diagnose('/v1/diagnostic', 'GET');
declare class TrackStream {static open(path: string, method: string): void;}
TrackStream.open('/v1/decoder', 'GET');


''')
            r=subprocess.run(['node',str(ROOT/'ci/client_api_sites.mjs')],input=json.dumps({'root':str(root),'files':['src/x.ts']}),text=True,capture_output=True,check=True)
            sites=json.loads(r.stdout)
            self.assertEqual([s['kind'] for s in sites],['request','request','request','url'],sites)
            self.assertEqual([s['line'] for s in sites],[9,10,12,13])

    def test_resolved_core_generic_and_multipart_methods_and_aliases(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);(root/'web/src').mkdir(parents=True);(root/'packages/client-core/src').mkdir(parents=True)
            (root/'packages/client-core/src/index.ts').write_text('''export declare class HttpLocalApi {
 requestBounded<T>(path: string, maxBytes: number, signal: AbortSignal): Promise<T>;
 requestMetadataUpload<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T>;
 requestSubtitleUpload<T>(path: string, method: string, body: FormData, signal?: AbortSignal): Promise<T>;
}''')
            (root/'packages/client-core/src/playback-v1').mkdir()
            (root/'packages/client-core/src/playback-v1/http.ts').write_text('''export type V1Request = Readonly<{path: string; method: string; body?: unknown}>;
export interface V1Http {send(request: V1Request): Promise<unknown>;}''')
            (root/'web/src/x.ts').write_text('''import {HttpLocalApi as Transport} from '../../packages/client-core/src/index';
import type {V1Http, V1Request} from '../../packages/client-core/src/playback-v1/http';
declare const api: Transport, path: string, signal: AbortSignal, body: FormData;
api.requestBounded(path, 100, signal);
api.requestMetadataUpload(path, body, signal);
api.requestSubtitleUpload(path, 'PUT', body, signal);
const bounded = api.requestBounded.bind(api); bounded(path, 100, signal);
const upload = api.requestMetadataUpload; upload(path, body, signal);
const {requestSubtitleUpload: renamed} = api; renamed(path, 'PUT', body, signal);
declare const wire: V1Http;
wire.send({path,method:'GET'});
class LocalGuard {send(request: V1Request) {return wire.send(request);}}
new LocalGuard().send({path,method:'GET'});
wire.send({path,method:'POST',body:()=>api.requestSubtitleUpload(path,'PUT',body,signal)});
declare const mailbox: {send(request: {message: string}): void};
mailbox.send({message: 'local event'});
declare const disk: {requestBounded(path: string, bytes: number): string};
disk.requestBounded(path, 100);
''')
            result=subprocess.run(['node',str(ROOT/'ci/client_api_sites.mjs')],input=json.dumps({'root':str(root),'files':['web/src/x.ts']}),text=True,capture_output=True,check=True)
            sites=json.loads(result.stdout)
            self.assertEqual([s['callee'] for s in sites],['api.requestBounded','api.requestMetadataUpload','api.requestSubtitleUpload','bounded','upload','renamed','wire.send','wire.send','new LocalGuard().send','wire.send','api.requestSubtitleUpload'])

    def test_native_url_readers_async_sessions_and_fluent_http_require_transport_evidence(self):
        kotlin='''import java.net.URL
fun read(link: URL, input: String) {
 val saved = URL(input)
 val a = saved.readText()
 val b = URL(input).readBytes()
 val c = link.inputStream()
 file.readText(); file.readBytes(); cache.get(input)
}
'''
        self.assertEqual([s['callee'] for s in native_sites('apps/native/Read.kt',kotlin)],['readText','readBytes','inputStream'])
        swift='''func load(session: URLSession, url: URL, request: URLRequest) async {
 let a = try await URLSession.shared.data(from: url)
 let b = try await session.data(for: request)
 let c = try await session.bytes(from: url)
 model.data(from: url); model.bytes(for: request); secret.bytes()
}
'''
        self.assertEqual([s['callee'] for s in native_sites('apps/native/Read.swift',swift)],['data','data','bytes'])
        rust='''use reqwest::Client;
async fn load(client: Client, url: &str) {
 let a = client.get(url).header("Accept", "application/json").send().await;
 let b = reqwest::get(url).await;
 cache.get(url); mailbox.send(event);
}
'''
        sites=native_sites('apps/desktop/src-tauri/src/read.rs',rust)
        self.assertEqual([s['callee'] for s in sites],['get','get'])
        self.assertIn('. header ( "Accept" , "application/json" ) . send ( ) . await',sites[0]['expression'])

    def test_borrowed_rust_http_clients_cannot_bypass_detection(self):
        for binding in ['&Client','&mut Client',"&'a Client", "&'a mut Client", '&reqwest::Client',
                        "&'a reqwest::Client",'&reqwest::blocking::Client',"&'a mut reqwest::blocking::Client"]:
            source=f'''use reqwest::Client;
async fn load<'a>(client: {binding}, url: &str) {{
 let response = client.get(url).header("Accept", "application/json").send().await;
 cache.get(url); mailbox.send(event);
}}'''
            with self.subTest(binding=binding):
                sites=native_sites('apps/desktop/src-tauri/src/read.rs',source)
                self.assertEqual([s['callee'] for s in sites],['get'])
                self.assertIn('client . get',sites[0]['expression'])

    def test_objc_call_bytes_include_complete_receiver_message(self):
        for receiver in ['session','otherSession','[factory session]']:
            call=f'[{receiver} dataTaskWithRequest:request completionHandler:handler]'
            sites=native_sites('apps/native/Bridge.m',f'static void Fetch(void) {{ {call}; }}')
            self.assertEqual(sites[0]['callText'],call)

    def test_transport_rehash_budget_is_shared_with_retained_and_split_pins(self):
        def approval(guard,**extra):return dict(category='platform-transport',guardHash=guard,occurrences=1,**extra)
        old={'old':approval('a')}
        new={**old,'new':approval('b',previousSiteId='old',previousGuardHash='a')}
        def pins(keys):return {'web/x.ts':{'sites':keys,'category':'platform-transport','reason':'core request only',
            'guardHashes':{key:new[key]['guardHash']for key in keys}}}
        transport_rehash_budgets(new,old,pins(['new']))
        for keys in [['old','new'],['new','new']]:
            with self.assertRaises(ValueError):transport_rehash_budgets(new,old,pins(keys))
        new['second']=approval('c',previousSiteId='old',previousGuardHash='a')
        with self.assertRaises(ValueError):transport_rehash_budgets(new,old,pins(['new','second']))
        new['old']['occurrences']=2;new['new']['occurrences']=2
        # Editing the original row alongside the re-hash cannot raise its budget.
        with self.assertRaises(ValueError):transport_rehash_budgets(new,{'old':approval('a')},pins(['new','new']))


class ClientApiCensusEndToEnd(unittest.TestCase):
    def fixture(self, source, filename='web/src/guard.ts', category='platform-transport', dependencies=None):
        import os
        from contextlib import ExitStack
        self.stack = ExitStack(); self.addCleanup(self.stack.close)
        root=Path(self.stack.enter_context(tempfile.TemporaryDirectory()))
        (root/filename).parent.mkdir(parents=True)
        (root/filename).write_text(source)
        for name,body in (dependencies or {}).items():
            (root/name).parent.mkdir(parents=True,exist_ok=True);(root/name).write_text(body)
        (root/'ci').mkdir()
        self.env = dict(os.environ, GIT_AUTHOR_NAME='Justin Ehler', GIT_AUTHOR_EMAIL='justin@justinehler.com', GIT_COMMITTER_NAME='Justin Ehler', GIT_COMMITTER_EMAIL='justin@justinehler.com')
        subprocess.run(['git','init','-q',str(root)],check=True,env=self.env)
        self.git(root,'add',filename)
        sites=scan(root);self.assertTrue(sites)
        entry={'category':category,'reason':'Reviewed core-built transport only','sites':[s['id']for s in sites]}
        if category=='platform-transport':entry['guardHashes']={s['id']:s['guardHash']for s in sites}
        (root/BASELINE).write_text('{}\n');(root/ALLOWLIST).write_text(json.dumps({filename:entry}))
        self.git(root,'add','.');self.git(root,'commit','-qm','Record reviewed census fixture')
        return root,filename,sites,entry

    def git(self,root,*args):
        return subprocess.check_output(['git',*args],cwd=root,env=self.env,text=True,stderr=subprocess.PIPE)

    def check(self,root):
        import io
        from contextlib import redirect_stdout,redirect_stderr
        out=io.StringIO()
        with redirect_stdout(out),redirect_stderr(out):code=main(['--base','HEAD'],root=root)
        return code,out.getvalue()

    GUARD="""declare const api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
async function guarded(path: string, method: string, body: unknown, signal: AbortSignal) {
  const value = await api.request<unknown>(path, method, body, signal);
  return value;
}
"""

    def test_overlap_and_combined_allowance_growth_fail_through_the_real_check(self):
        root,filename,sites,entry=self.fixture(self.GUARD)
        self.assertEqual(self.check(root)[0],0)
        key=sites[0]['id']
        (root/filename).write_text(self.GUARD.replace('await api.request<unknown>(path, method, body, signal)', 'await Promise.all([api.request<unknown>(path, method, body, signal), api.request<unknown>(path, method, body, signal)])'))
        entry['additionalSites']={key:{'category':entry['category'],'reason':entry['reason'],'guardHash':entry['guardHashes'][key]}}
        (root/ALLOWLIST).write_text(json.dumps({filename:entry}))
        code,output=self.check(root);self.assertEqual(code,1);self.assertIn('overlap',output)
        # Complete combined counts are checked even for an additional-only ID.
        old={'web/x.ts':{'category':'platform-url','reason':'bytes','sites':['a'],'additionalSites':{'b':{'category':'platform-url','reason':'bytes'}}}}
        grown=json.loads(json.dumps(old));grown['web/x.ts']['sites'].append('b');del grown['web/x.ts']['additionalSites']['b'];grown['web/x.ts']['sites'].append('b')
        self.assertTrue(base_guard({},grown,{},old))

    def test_mutating_reviewed_guard_policy_invalidates_the_hash_and_rebinding_needs_review(self):
        root,filename,sites,entry=self.fixture(self.GUARD)
        (root/filename).write_text(self.GUARD.replace('  const value', "  path = '/v1/items/review855'; method = 'DELETE'; body = {review855:true};\n  const value"))
        code,output=self.check(root);self.assertEqual(code,1);self.assertIn('new/increased',output)
        mutated=scan(root)[0];self.assertNotEqual(mutated['id'],sites[0]['id']);self.assertNotEqual(mutated['guardHash'],sites[0]['guardHash'])
        entry['sites']=[mutated['id']];entry['guardHashes']={mutated['id']:mutated['guardHash']};(root/ALLOWLIST).write_text(json.dumps({filename:entry}))
        code,output=self.check(root);self.assertEqual(code,1);self.assertIn('exception expanded or changed',output)

    def test_combined_raw_cleanup_budget_binds_the_full_policy_and_rejects_rewrites(self):
        source='''declare const api: {requestRaw(path: string, method: string, options: unknown): Promise<unknown>};
function screenLifetime(selected: boolean) {
 return {requestRaw(path: string, method: string, options: unknown) {
  if (selected) return api.requestRaw(path, method, options);
  return api.requestRaw(path, method, options);
 }};
}
'''
        root,filename,sites,pin=self.fixture(source)
        self.assertEqual(len(sites),2)
        self.assertEqual(len({(s['id'],s['guardHash']) for s in sites}),1)
        self.assertEqual(self.check(root)[0],0)
        # The two identical cleanup calls spend one combined allowance of two.
        grown=sites+[sites[0]]
        self.assertEqual(len(classify(grown,{filename:pin})[0]),1)
        for old,new in [('api.requestRaw(path,','api.requestRaw(path + "/built",'),
                        ('method, options)','method, {body: options})'),
                        ('if (selected)','if (!selected)')]:
            with self.subTest(edit=new):
                (root/filename).write_text(source.replace(old,new))
                self.assertEqual(self.check(root)[0],1)
        # Argument spelling alone does not preserve a reviewed enclosing policy.
        (root/filename).write_text(source.replace('if (selected)','if (!selected)'))
        self.assertTrue(all(s['forwardedRequest'] for s in scan(root)))

    def test_distinct_spans_sharing_an_id_require_separate_count_one_bindings(self):
        from client_api_census import validate_approvals
        core='export type V1Request = {path: string; method: string; body?: unknown; signal?: AbortSignal}; export interface V1Http {send(request: V1Request): Promise<unknown>;}'
        source='''import type {V1Http,V1Request} from '../../packages/client-core/src/playback-v1/http';
declare const wire: V1Http;
function first() {return {send(request: V1Request) {const owner = 1;return wire.send(request);}};}
function second() {return {send(request: V1Request) {const owner = 2;return wire.send(request);}};}
'''
        root,filename,sites,_=self.fixture(source,dependencies={'packages/client-core/src/playback-v1/http.ts':core})
        self.assertEqual(len({s['id'] for s in sites}),1)
        self.assertEqual(len({s['guardHash'] for s in sites}),2)
        key=sites[0]['id']
        (root/ALLOWLIST).write_text('{}');(root/BASELINE).write_text(json.dumps({key:2}))
        self.git(root,'add','.');self.git(root,'commit','-qm','Record two direct spans')
        pairs=[s['id']+'@'+s['guardHash'] for s in sites]
        pin={'category':'platform-transport','reason':'Two separately reviewed core forwards',
             'sites':pairs,'guardHashes':{pair:s['guardHash'] for pair,s in zip(pairs,sites)}}
        ledger={pair:{'category':pin['category'],'reason':pin['reason'],'guardHash':s['guardHash'],
                       'condition':'Exact complete span and count one','occurrences':1,
                       'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6648755492'}
                for pair,s in zip(pairs,sites)}
        (root/ALLOWLIST).write_text(json.dumps({filename:pin}));(root/APPROVALS).write_text(json.dumps(ledger))
        (root/BASELINE).write_text('{}')
        self.assertEqual(self.check(root)[0],0)
        for bad in [dict(pin,sites=[pairs[0],pairs[0]]),dict(pin,sites=pairs+[key])]:
            with self.assertRaises(ValueError):classify(sites,{filename:bad})
        grown=json.loads(json.dumps(ledger));grown[pairs[0]]['occurrences']=2
        with self.assertRaises(ValueError):validate_approvals(grown)
        # A third unapproved span must not spend the old DIRECT budget again
        # while the other two old occurrences become exceptions in the same PR.
        (root/filename).write_text(source+source.splitlines()[2].replace('first','third').replace('owner = 1','owner = 3')+'\n')
        (root/BASELINE).write_text(json.dumps({key:2}))
        import io
        from contextlib import redirect_stdout,redirect_stderr
        with redirect_stdout(io.StringIO()),redirect_stderr(io.StringIO()):
            self.assertEqual(main(['--update','--base','HEAD'],root=root),1)

    def test_activated_pairs_cannot_respend_the_historical_detection_budget(self):
        core='export type V1Request = {path: string; method: string}; export interface V1Http {send(request: V1Request): Promise<unknown>;}'
        source='''import type {V1Http,V1Request} from '../../packages/client-core/src/playback-v1/http';
declare const wire: V1Http;
function first() {return {send(request: V1Request) {const owner=1;return wire.send(request);}};}
function second() {return {send(request: V1Request) {const owner=2;return wire.send(request);}};}
'''
        root,filename,sites,_=self.fixture(source,dependencies={'packages/client-core/src/playback-v1/http.ts':core})
        key=sites[0]['id'];self.assertEqual({s['id'] for s in sites},{key})
        original=self.git(root,'rev-parse','HEAD').strip()
        pairs=[key+'@'+s['guardHash'] for s in sites]
        pin={'category':'platform-transport','reason':'Reviewed pair','sites':pairs,
             'guardHashes':{k:s['guardHash'] for k,s in zip(pairs,sites)}}
        ledger={k:{'category':pin['category'],'reason':pin['reason'],'guardHash':s['guardHash'],
                   'condition':'Exact full span','occurrences':1,
                   'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6648755492'}
                for k,s in zip(pairs,sites)}
        (root/ALLOWLIST).write_text(json.dumps({filename:pin}));(root/APPROVALS).write_text(json.dumps(ledger))
        (root/DETECTIONS).write_text(json.dumps({'sourceCommit':original,'sites':{key:{'occurrences':2,
            'reason':'Historical detection improvement','rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646280885'}}}))
        self.git(root,'add','.');self.git(root,'commit','-qm','Activate two pairs with historical detection manifest')
        self.assertEqual(self.check(root)[0],0)
        # Keep both original pairs and the historical manifest unchanged. A third
        # call must fail even after increasing its DIRECT baseline to one.
        (root/filename).write_text(source+source.splitlines()[2].replace('first','third').replace('owner=1','owner=3')+'\n')
        (root/BASELINE).write_text(json.dumps({key:1}))
        import io
        from contextlib import redirect_stdout,redirect_stderr
        with redirect_stdout(io.StringIO()),redirect_stderr(io.StringIO()):
            self.assertEqual(main(['--update','--base','HEAD'],root=root),1)

    def test_conditional_rebinding_cannot_turn_explicit_writes_into_forwarding(self):
        core='export type V1Request = {path: string; method: string; body?: unknown; headers?: Record<string,string>; signal?: AbortSignal}; export interface V1Http {send(request: V1Request): Promise<unknown>;}'
        source='''import type {V1Http,V1Request} from '../../packages/client-core/src/playback-v1/http';
declare const wire: V1Http, life: AbortSignal;
function forward(request: V1Request, adopted: boolean) {return wire.send(adopted ? request : {...request,signal:life});}
'''
        root,filename,_,_=self.fixture(source,dependencies={'packages/client-core/src/playback-v1/http.ts':core})
        for condition in ['(request = {...request,path: request.path + "/made"})',
                          '(request.path = "/made")','(request.method = "DELETE")',
                          '(request.body = {})','(request.headers = {})','(request.signal = life)',
                          '(request.headers!["Secret"] = "made")','(delete request.body)',
                          '(request.path += "/made")']:
            for target in ['adopted','signal:life']:
                changed=(source.replace('wire.send(adopted ?', 'wire.send('+condition+' ?') if target=='adopted'
                         else source.replace('signal:life','signal:('+condition+',life)'))
                (root/filename).write_text(changed);sites=scan(root)
                self.assertTrue(sites)
                self.assertTrue(all(not s['forwardedRequest'] for s in sites),(condition,target))
                # Even a freshly rebound exact hash must not exempt construction.
                pin={filename:{'category':'platform-transport','reason':'Reviewed','sites':[s['id'] for s in sites],
                               'guardHashes':{s['id']:s['guardHash'] for s in sites}}}
                with self.assertRaises(ValueError):classify(sites,pin)

    def test_conditional_signal_and_farewell_shapes_reject_path_or_body_construction(self):
        core='export type V1Request = {path: string; method: string; body?: unknown; signal?: AbortSignal}; export interface V1Http {send(request: V1Request): Promise<unknown>;}'
        source='''import type {V1Http,V1Request} from '../../packages/client-core/src/playback-v1/http';
import type {HttpLocalApi} from '../../packages/client-core/src/index';
declare const wire: V1Http, life: AbortSignal;
declare const api: HttpLocalApi;
function conditional(request: V1Request, adopted: boolean) {return wire.send(adopted ? request : {...request,signal: life});}
function farewell(path: string, method: string, body: unknown) {return api.farewell(path,method,body);}
'''
        root,filename,sites,_=self.fixture(source,dependencies={'packages/client-core/src/playback-v1/http.ts':core,
            'packages/client-core/src/index.ts':'export declare class HttpLocalApi {farewell(path: string, method: string, body: unknown): void;}'})
        self.assertEqual([s['forwardingShape'] for s in sites],['conditional-signal','farewell'])
        self.assertEqual(self.check(root)[0],0)
        for old,new in [('{...request,signal: life}','{...request,path: request.path + "/made",signal: life}'),
                        ('{...request,signal: life}','{...request,body: {},signal: life}'),
                        ('api.farewell(path,method,body)','api.farewell(path + "/made",method,body)'),
                        ('api.farewell(path,method,body)','api.farewell(path,method,{body})')]:
            with self.subTest(edit=new):
                (root/filename).write_text(source.replace(old,new))
                changed=scan(root)
                self.assertTrue(any(not s['forwardedRequest'] for s in changed))
                self.assertEqual(self.check(root)[0],1)

    def test_cw_rulings_keep_exact_file_qualified_budgets_and_hashes(self):
        rulings={'6651098315','6654511777','6655467024','6673584916'}
        ledger=json.loads((ROOT/APPROVALS).read_text())
        approved={key:value for key,value in ledger.items()
                  if value['rulingUrl'].rsplit('-',1)[-1] in rulings}
        self.assertEqual(len(approved),40)
        self.assertEqual(sum(v['occurrences'] for v in approved.values()),41)
        exceptions=json.loads((ROOT/ALLOWLIST).read_text())
        for key,value in approved.items():
            with self.subTest(site=key):
                entry=exceptions[key.split('#')[0]]
                occurrence_budget=entry['sites'].count(key)+int(key in entry.get('additionalSites',{}))
                self.assertEqual(occurrence_budget,value['occurrences'])
                site={'file':key.split('#')[0],'id':key,'line':1,
                      'guardHash':value['guardHash'],'forwardedRequest':True}
                self.assertEqual(len(classify([site]*occurrence_budget,{site['file']:entry})[1]),occurrence_budget)
                self.assertEqual(len(classify([site]*(occurrence_budget+1),{site['file']:entry})[0]),1)
                with self.assertRaises(ValueError):
                    classify([dict(site,guardHash='0'*64)],{site['file']:entry})

    def test_request_rest_tuple_forwards_exactly_and_rejects_tuple_rewrites(self):
        source='''declare const api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
function owner() {
 return {async request<T>(...args: Parameters<typeof api.request>) {
  return await api.request<T>(...args);
 }};
}'''
        root,filename,sites,_=self.fixture(source)
        self.assertEqual(sites[0]['forwardingShape'],'request-rest')
        self.assertEqual(self.check(root)[0],0)
        for changed in [source.replace('api.request<T>(...args)','api.request<T>(args[0] + "/made",args[1],args[2],args[3])'),
                        source.replace('api.request<T>(...args)','api.request<T>(args[0],args[1],{body:args[2]},args[3])'),
                        source.replace('  return await','  args[0] = "/made"; return await'),
                        source.replace('  return await','  args.reverse(); return await'),
                        source.replace('  return await','  if ((args[0] += "/made")) {} return await'),
                        source.replace('  return await','  if ((args[2] = {})) {} return await'),
                        source.replace('  return await','  Reflect.set(args,0,"/made"); return await'),
                        source.replace('  return await','  [args[0],args[1]] = [args[1]!,args[0]]; return await'),
                        source.replace('  return await','  args.length = 1; return await'),
                        source.replace('  return await','  const copy = args; copy.reverse(); return await'),
                        source.replace('  return await','  [args] = [args.slice(1) as Parameters<typeof api.request>]; return await'),
                        source.replace('  return await','  [args] = [[...args,"extra"] as unknown as Parameters<typeof api.request>]; return await'),
                        source.replace('  return await','  args["reverse"](); return await'),
                        source.replace('  return await','  mutate(args); return await')]:
            (root/filename).write_text(changed)
            self.assertTrue(all(not s['forwardedRequest'] for s in scan(root)))
            self.assertEqual(self.check(root)[0],1)

    def test_vega_dispatch_forwards_core_url_method_body_and_header_sequence_only(self):
        helper='export declare function vegaControlHeaderEntries(headers: Record<string,string>): ReadonlyArray<readonly [string,string]>;'
        source='''import {vegaControlHeaderEntries} from '../../packages/client-core/src/vega-control-transport';
function start(pending: {url: string;method: string;body: string|null;headers: Record<string,string>;started?: boolean;xhr?: XMLHttpRequest}) {
 pending.started = true;
 const xhr = new XMLHttpRequest();
 pending.xhr = xhr;
 xhr.open(pending.method,pending.url);
 xhr.timeout = 15000;
 xhr.withCredentials = false;
 for (const [key,value] of vegaControlHeaderEntries(pending.headers)) xhr.setRequestHeader(key,value);
 xhr.send(pending.body ?? undefined);
}'''
        root,filename,sites,_=self.fixture(source,dependencies={'packages/client-core/src/vega-control-transport.ts':helper})
        self.assertEqual(sites[0]['forwardingShape'],'vega-pending')
        self.assertEqual(self.check(root)[0],0)
        for old,new in [('pending.method,pending.url','"POST",pending.url'),
                        ('pending.method,pending.url','pending.method,pending.url + "/made"'),
                        ('xhr.send(pending.body ?? undefined)','xhr.send(JSON.stringify({body:pending.body}))'),
                        ('vegaControlHeaderEntries(pending.headers)','vegaControlHeaderEntries({...pending.headers,Extra:"made"})'),
                        ('xhr.setRequestHeader(key,value)','xhr.setRequestHeader("Extra",value)'),
                        (' const xhr',' pending.url = "/made"; const xhr'),
                        (' const xhr',' if ((pending.url += "/made")) {} const xhr'),
                        (' const xhr',' if ((pending.headers.Extra = "made")) {} const xhr'),
                        (' const xhr',' if ((pending = {...pending,url:"/made"})) {} const xhr'),
                        ('xhr.timeout = 15000','xhr.timeout = ((pending.method = "POST"),15000)'),
                        (' const xhr',' delete pending.headers.Authorization; const xhr'),
                        (' const xhr',' pending["method"] = "DELETE"; const xhr'),
                        (' const xhr',' pending["body"] = "made"; const xhr'),
                        (' const xhr',' Object.assign(pending,{url:"/made"}); const xhr'),
                        (' const xhr',' const copy=pending; copy["url"]="/made"; const xhr'),
                        (' const xhr',' Object.assign(pending.headers,{Authorization:"made"}); const xhr'),
                        ('xhr.send(pending.body ?? undefined)','xhr["setRequestHeader"]("Extra","made"); xhr.send(pending.body ?? undefined)'),
                        ('xhr.send(pending.body ?? undefined)','const copy=xhr; copy.setRequestHeader("Extra","made"); xhr.send(pending.body ?? undefined)'),
                        ('xhr.withCredentials = false','xhr.withCredentials = true'),
                        ('xhr.withCredentials = false','xhr.withCredentials = Boolean(true)'),
                        ('xhr.withCredentials = false','xhr.withCredentials = false; xhr["withCredentials"] = true'),
                        ('xhr.withCredentials = false','xhr.withCredentials = false; Object.assign(xhr,{withCredentials:true})'),
                        ('for (const [key,value] of vegaControlHeaderEntries(pending.headers)) xhr.setRequestHeader(key,value);',
                         'for (let [key,value] of vegaControlHeaderEntries(pending.headers)) {value="made"; xhr.setRequestHeader(key,value);}')]:
            with self.subTest(edit=new):
                (root/filename).write_text(source.replace(old,new))
                self.assertTrue(all(not s['forwardedRequest'] for s in scan(root)))
                self.assertEqual(self.check(root)[0],1)

    def test_non_api_pin_binds_full_source_and_external_release_conditions(self):
        import hashlib
        source='''const asset = new URL('./bundled.woff2',import.meta.url).href;
async function load(signal: AbortSignal) {return fetch(asset,{credentials:'omit',signal});}
'''
        root,filename,sites,pin=self.fixture(source,category='platform-url',dependencies={'release.json':'{"debugConsole":false}'})
        key=sites[0]['id'];span=sites[0]['guardHash']
        hashes={p:hashlib.sha256((root/p).read_bytes()).hexdigest() for p in [filename,'release.json']}
        pin.update(category='non-api-platform',guardHashes={key:span},sourceHashes={key:hashes})
        approval={'category':pin['category'],'reason':pin['reason'],'condition':'Exact bundled asset with omitted credentials; release config disabled',
                  'guardHash':span,'sourceHashes':hashes,'occurrences':1,
                  'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6649923975'}
        (root/ALLOWLIST).write_text(json.dumps({filename:pin}));(root/APPROVALS).write_text(json.dumps({key:approval}))
        self.assertEqual(self.check(root)[0],0)
        with self.assertRaises(ValueError):classify(sites,{filename:pin})
        for changed in [source.replace("'./bundled.woff2'","'/v1/private'"),
                        source.replace("credentials:'omit'","credentials:'include'")]:
            (root/filename).write_text(changed)
            self.assertEqual(self.check(root)[0],1)
        (root/filename).write_text(source)
        # Changing a constant/config outside the reviewed function also voids it.
        (root/'release.json').write_text('{"debugConsole":true,"debugSink":"https://server/v1/private"}')
        self.assertEqual(self.check(root)[0],1)

    def test_native_url_method_body_and_bound_input_edits_each_invalidate_byte_pins(self):
        source='fun relay() { val method = "GET"; val request = Request.Builder().url(upstream.toString()).method(method, null).build() }\n'
        root,filename,sites,entry=self.fixture(source,'apps/native/Relay.kt','platform-url')
        self.assertIn('url ( upstream . toString ( ) ) . method ( method , null ) . build ( )',sites[0]['expression'])
        self.assertEqual(self.check(root)[0],0)
        for changed in [source.replace('upstream.toString()', '"https://portico.test/v1/users"'),source.replace('.method(method, null)', '.method("DELETE", null)'),source.replace('method, null','method, body'),source.replace('"GET"','"DELETE"')]:
            (root/filename).write_text(changed)
            code,output=self.check(root);self.assertEqual(code,1,changed);self.assertIn('new/increased',output)

    def test_new_forwarding_shapes_bind_the_whole_guard_and_reject_constructed_arguments(self):
        common="declare const api: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};\n"
        raw="declare const api: {requestRaw(path: string, method: string, options: unknown): Promise<unknown>};\nasync function guarded(path: string, method: string, options: unknown) { return api.requestRaw(path, method, options); }\n"
        alias=common+"const originalRequest = api.request.bind(api);\nasync function guarded(path: string, method: string, body: unknown, signal: AbortSignal) { return originalRequest(path, method, body, signal); }\n"
        three=common+"async function guarded(path: string, method: string, body: unknown) { return api.request(path, method, body); }\n"
        chapter="""declare function encodedRoutePath(id: string, args: object): string;
declare const options: {request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>};
function guarded(target: {sessionId: string}) {
 const chapterPath=encodedRoutePath('get_playback_sessions_id_chapters',{id:encodeURIComponent(target.sessionId)});
 return async (path: string, method: string, body: unknown, signal: AbortSignal) => {
  if (method !== 'GET' || body !== undefined || path !== chapterPath && !path.startsWith(chapterPath+'?')) throw new Error('read-only');
  return options.request(path,'GET',undefined,signal);
 };
}
"""
        fixtures=[(raw,'requestRaw','api.requestRaw(path,','api.requestRaw(path+"/constructed",','method, options)','method, {body: {constructed:true}})'),
                  (alias,'request','originalRequest(path,','originalRequest(path+"/constructed",','method, body,','method, {constructed:true},'),
                  (three,'request-without-signal','api.request(path,','api.request(path+"/constructed",','method, body)','method, {constructed:true})'),
                  (chapter,'chapter-read','options.request(path,','options.request(path+"/constructed",',"'GET',undefined,","'GET',{constructed:true},")]
        for source,shape,path_before,path_after,body_before,body_after in fixtures:
            with self.subTest(shape=shape):
                root,filename,sites,_=self.fixture(source)
                self.assertTrue(any(s.get('forwardingShape')==shape for s in sites),sites)
                self.assertEqual(self.check(root)[0],0)
                for changed in [source.replace(path_before,path_after),source.replace(body_before,body_after)]:
                    self.assertNotEqual(changed,source)
                    (root/filename).write_text(changed)
                    self.assertEqual(self.check(root)[0],1,changed)
                    changed_sites=scan(root)
                    transport=next(site for site in changed_sites if site['kind']=='request')
                    self.assertFalse(transport.get('forwardedRequest'),transport)
                    # Refreshing the hash cannot turn an argument constructor into
                    # a valid forwarding shape, even under an otherwise fresh pin.
                    bad={filename:{'category':'platform-transport','reason':'Review condition unchanged',
                         'sites':[transport['id']],'guardHashes':{transport['id']:transport['guardHash']}}}
                    with self.assertRaises(ValueError): classify([transport],bad)
                if shape=='chapter-read':
                    # Even the comparison capability is bound to the whole reviewed
                    # factory, rather than only its tiny route-builder expression.
                    (root/filename).write_text(source.replace('target.sessionId', "'different-session'"))
                    self.assertEqual(self.check(root)[0],1)

    def test_objc_bridge_and_c_method_policy_inputs_are_in_native_identity(self):
        sources=[('RCT_REMAP_METHOD(openStream,openStream:(NSString *)url) { NSMutableURLRequest *request=[NSMutableURLRequest requestWithURL:url]; request.HTTPMethod=@"GET"; }', '@"GET"', '@"DELETE"'),
                 ('static void Fetch(void) { NSMutableURLRequest *request=[NSMutableURLRequest requestWithURL:url]; request.HTTPBody=data; }', 'HTTPBody=data', 'HTTPBody=other')]
        for source,before,after in sources:
            root,filename,sites,_=self.fixture(source,'apps/native/Bridge.m','platform-url')
            self.assertTrue(sites[0].get('constructionHash'))
            self.assertEqual(self.check(root)[0],0)
            (root/filename).write_text(source.replace(before,after));self.assertEqual(self.check(root)[0],1)

    def test_cc4_forwarding_shapes_reject_path_body_and_options_construction(self):
        core='''export declare class HttpLocalApi {
 request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal, options?: unknown): Promise<T>;
 requestConsole<T>(path: string, method: string, body: unknown, signal: AbortSignal): Promise<T>;
 requestRaw(path: string, method: string, options: unknown): Promise<unknown>;
 routeFetch(input: string, options: unknown): Promise<unknown>;
 requestLyrics<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T>;
 requestMetadataUpload<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T>;
 requestBounded<T>(path: string, maxBytes: number, signal: AbortSignal): Promise<T>;
 send(request: object): Promise<unknown>;
}\n'''
        declarations="import type {HttpLocalApi} from '../../packages/client-core/src/index';\ndeclare const api: HttpLocalApi;\n"
        cases=[
            ('raw-signal-options', 'function guard(path: string, method: string, options: object, signal: AbortSignal) {return api.requestRaw(path,method,{...options,signal});}',
             'api.requestRaw(path,','api.requestRaw(path+"/built",','{...options,signal}','{...options,body:{built:true},signal}'),
            ('raw-signal-options', 'function guard(...args: [string,string,object]) {const [path,method,init]=args;return api.requestRaw(path,method,{...init,signal:undefined});}',
             'api.requestRaw(path,','api.requestRaw(path+"/built",','{...init,signal:undefined}','{...init,body:{built:true},signal:undefined}'),
            ('console', 'function guard(path: string, method: string, body: unknown, signal: AbortSignal) {return api.requestConsole(path,method,body,signal);}',
             'api.requestConsole(path,','api.requestConsole(path+"/built",','method,body,','method,{built:true},'),
            ('request-options', 'function guard(path: string, method: string, body: unknown, signal: AbortSignal, options: object) {return api.request(path,method,body,signal,options);}',
             'api.request(path,','api.request(path+"/built",','method,body,','method,{built:true},'),
            ('read-only', "function guard(path: string, method: string, body: unknown, signal: AbortSignal) {if(method!=='GET'||body!==undefined)throw Error();return api.request(path,'GET',undefined,signal);}",
             'api.request(path,','api.request(path+"/built",',"'GET',undefined,","'GET',{built:true},"),
            ('fetch-signal-options', 'function guard(input: string, options: object, signal: AbortSignal) {return api.routeFetch(input,{...options,signal});}',
             'api.routeFetch(input,','api.routeFetch(input+"/built",','{...options,signal}','{...options,body:{built:true},signal}'),
            ('lyrics', 'function guard(path: string, method: string, body: unknown, signal: AbortSignal) {return api.requestLyrics(path,method,body,signal);}',
             'api.requestLyrics(path,','api.requestLyrics(path+"/built",','method,body,','method,{built:true},'),
            ('metadata-upload', 'function guard(path: string, body: unknown, signal: AbortSignal) {return api.requestMetadataUpload(path,body,signal);}',
             'api.requestMetadataUpload(path,','api.requestMetadataUpload(path+"/built",','path,body,','path,{built:true},'),
            ('bounded', 'function guard(path: string, maxBytes: number, signal: AbortSignal) {return api.requestBounded(path,maxBytes,signal);}',
             'api.requestBounded(path,','api.requestBounded(path+"/built",','path,maxBytes,','path,maxBytes+1,'),
            ('request-object', 'function guard(request: object, signal: AbortSignal) {return api.send({...request,signal});}',
             '{...request,signal}','{...request,path:"/built",signal}','{...request,signal}','{...request,body:{built:true},signal}'),
        ]
        for shape,guard,p0,p1,b0,b1 in cases:
            with self.subTest(shape=shape):
                source=declarations+guard
                root,filename,sites,entry=self.fixture(source,dependencies={'packages/client-core/src/index.ts':core})
                self.assertEqual(sites[0].get('forwardingShape'),shape)
                self.assertEqual(self.check(root)[0],0)
                for changed in [source.replace(p0,p1),source.replace(b0,b1)]:
                    (root/filename).write_text(changed)
                    self.assertEqual(self.check(root)[0],1)
                    site=scan(root)[0];self.assertFalse(site.get('forwardedRequest'),site)
                    with self.assertRaises(ValueError):
                        classify([site],{filename:{**entry,'sites':[site['id']],'guardHashes':{site['id']:site['guardHash']}}})
                if shape=='request-options':
                    (root/filename).write_text(source.replace('signal,options)','signal,{...options})'))
                    self.assertFalse(scan(root)[0].get('forwardedRequest'))

    def test_a_domain_read_using_a_core_url_helper_is_not_a_transport_guard(self):
        source='''declare const api: {request<T>(path: string, method?: string, body?: unknown): Promise<T>};
declare function kindMetadataAgentsPath(kind: string): string;
function load(kind: string) {return api.request(kindMetadataAgentsPath(kind));}
'''
        root,_,sites,_=self.fixture(source)
        self.assertFalse(sites[0].get('forwardedRequest'))
        self.assertEqual(self.check(root)[0],1)

    def test_upload_and_bounded_pins_bind_captured_api_selection_in_the_full_policy(self):
        core='''export declare class HttpLocalApi {
 requestMetadataUpload<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T>;
 requestBounded<T>(path: string, maxBytes: number, signal: AbortSignal): Promise<T>;
}'''
        for method,parameters,arguments in [('requestMetadataUpload','body: FormData','body'),('requestBounded','maxBytes: number','maxBytes')]:
            source=f'''import type {{HttpLocalApi}} from '../../packages/client-core/src/index';
function SettingsEntry(context: {{api: HttpLocalApi; account: {{api: HttpLocalApi}}}}) {{
 const originalApi = context.api;
 return async <T>(path: string, {parameters}, input: AbortSignal) => {{
  return originalApi.{method}<T>(path, {arguments}, input);
 }};
}}'''
            with self.subTest(method=method):
                root,filename,sites,entry=self.fixture(source,dependencies={'packages/client-core/src/index.ts':core})
                self.assertNotEqual(sites[0]['guardHash'],sites[0]['forwardingHash'])
                self.assertEqual(self.check(root)[0],0)
                changed=source.replace('const originalApi = context.api;','const originalApi = context.account.api;')
                (root/filename).write_text(changed)
                mutated=scan(root)[0]
                self.assertEqual(mutated['forwardingHash'],sites[0]['forwardingHash'])
                self.assertNotEqual(mutated['guardHash'],sites[0]['guardHash'])
                self.assertEqual(self.check(root)[0],1)

    def test_exact_callback_span_needs_an_explicit_matching_ruling_scope(self):
        core='export declare class HttpLocalApi {requestMetadataUpload<T>(path: string, body: FormData, signal?: AbortSignal): Promise<T>;} '
        source='''import type {HttpLocalApi} from '../../packages/client-core/src/index';
function owner(api: HttpLocalApi) {
 return async <T>(path: string, body: FormData, signal?: AbortSignal) => {return api.requestMetadataUpload<T>(path,body,signal);};
}'''
        root,filename,sites,pin=self.fixture(source,dependencies={'packages/client-core/src/index.ts':core})
        site=sites[0];key=site['id']
        pin['guardHashes'][key]=site['forwardingHash']
        (root/ALLOWLIST).write_text(json.dumps({filename:pin}));self.assertEqual(self.check(root)[0],1)
        pin['guardSpans']={key:'forwarding-function'}
        (root/ALLOWLIST).write_text(json.dumps({filename:pin}));self.assertEqual(self.check(root)[0],1)
        approval={'category':'platform-transport','reason':pin['reason'],'condition':'Separately approved complete forwarding callback',
                  'guardHash':site['forwardingHash'],'guardSpan':'forwarding-function','occurrences':1,
                  'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6647211850'}
        (root/APPROVALS).write_text(json.dumps({key:approval}));self.assertEqual(self.check(root)[0],0)

    def test_native_verbatim_proof_transports_bind_full_spans_and_require_parameters(self):
        kotlin='''fun proofRequest(origin: String?, requestUrl: String?, method: String?, body: String?) {
 val target = requestUrl?.toHttpUrlOrNull()
 val request = Request.Builder().url(target).method(method!!, body.toByteArray(Charsets.UTF_8).toRequestBody("application/json".toMediaType())).build()
 client.newCall(request)
}'''
        objc='''+ (void)prove:(NSString *)origin fingerprint:(NSString *)fingerprint url:(NSString *)requestURL method:(NSString *)method body:(NSString *)body completion:(void (^)(NSDictionary *))completion {
 NSURL *url = [NSURL URLWithString:requestURL];
 NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url];
 request.HTTPMethod = method; request.HTTPBody = [body dataUsingEncoding:NSUTF8StringEncoding];
 [[session dataTaskWithRequest:request] resume];
}'''
        for filename,source,path_before,path_after,body_before,body_after in [
                ('apps/native/Proof.kt',kotlin,'.url(target)','.url(target+"/built")','body.toByteArray','replacement.toByteArray'),
                ('apps/native/Proof.m',objc,'URLWithString:requestURL','URLWithString:replacement','[body dataUsingEncoding','[replacement dataUsingEncoding')]:
            with self.subTest(filename=filename):
                root,filename,sites,entry=self.fixture(source,filename)
                self.assertTrue(all(s.get('forwardingShape')=='native-proof' for s in sites))
                self.assertEqual(self.check(root)[0],0)
                for changed in [source.replace(path_before,path_after),source.replace(body_before,body_after)]:
                    (root/filename).write_text(changed);self.assertEqual(self.check(root)[0],1)
                    mutated=scan(root);self.assertTrue(all(not s.get('forwardedRequest') for s in mutated))
                    rebound={filename:{**entry,'sites':[s['id']for s in mutated],
                                      'guardHashes':{s['id']:s['constructionHash']for s in mutated}}}
                    with self.assertRaises(ValueError):classify(mutated,rebound)

    def test_detection_growth_requires_exact_reviewed_existing_base_sites(self):
        source='import java.net.URL\nval link = URL(input)\nval text = link.readText()\n'
        root,filename,sites,_=self.fixture(source,'apps/native/Read.kt','platform-url')
        # The old detector did not see this existing java.net.URL read.
        (root/ALLOWLIST).write_text('{}');(root/BASELINE).write_text('{}')
        self.git(root,'add','.');self.git(root,'commit','-qm','Record the existing URL reader before detection improves')
        self.assertEqual(self.check(root)[0],1)
        key=sites[0]['id'];source_commit=self.git(root,'rev-parse','HEAD').strip()
        manifest={'sourceCommit':source_commit,'sites':{key:{'occurrences':1,'reason':'Reviewed F5 coverage of the existing URL reader',
            'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6645692710'}}}
        (root/DETECTIONS).write_text(json.dumps(manifest))
        self.assertEqual(main(['--update','--base','HEAD'],root=root),0)
        self.assertEqual(json.loads((root/BASELINE).read_text()),counts(sites))
        self.assertEqual(self.check(root)[0],0)
        # Neither raising the manifest cap nor editing the baseline permits a new
        # duplicate request introduced only by the product patch.
        (root/filename).write_text(source+'link.readText()\n')
        self.assertEqual(self.check(root)[0],1)
        manifest['sites'][key]['occurrences']=2;(root/DETECTIONS).write_text(json.dumps(manifest))
        (root/BASELINE).write_text(json.dumps({key:2}))
        self.assertEqual(self.check(root)[0],1)
        # A historical detection ruling cannot resurrect a request removed from
        # the current PR base, even though sourceCommit remains its ancestor.
        (root/filename).write_text(source.replace('val text = link.readText()\n',''))
        (root/BASELINE).write_text('{}')
        self.git(root,'add','.');self.git(root,'commit','-qm','Remove the URL request')
        (root/filename).write_text(source);(root/BASELINE).write_text(json.dumps({key:1}))
        self.assertEqual(self.check(root)[0],1)

    def test_direct_rehash_is_review_bound_byte_identical_and_one_for_one(self):
        root,filename,old_sites,_=self.fixture(self.GUARD)
        (root/ALLOWLIST).write_text('{}');(root/BASELINE).write_text(json.dumps(counts(old_sites)))
        self.git(root,'add','.');self.git(root,'commit','-qm','Keep the existing forwarding site direct')
        previous=self.git(root,'rev-parse','HEAD').strip();old=old_sites[0]
        source=self.GUARD.replace('  const value','  // An unrelated catalogue read moved into core.\n  const value')
        (root/filename).write_text(source)
        self.git(root,'add',filename);self.git(root,'commit','-qm','Change unrelated enclosing function source')
        reviewed=self.git(root,'rev-parse','HEAD').strip();new=scan(root)[0]
        # Linear rebuilds retain the independently reviewed source certificate,
        # even when that old review commit is no longer an ancestor of HEAD.
        self.git(root,'checkout','-q',previous)
        (root/filename).write_text(source)
        self.git(root,'add',filename);self.git(root,'commit','-qm','Rebuild the same reviewed function on main')
        self.assertNotEqual(self.git(root,'rev-parse','HEAD').strip(),reviewed)
        evidence='docs/client-api-rehashes/901-guard.json';(root/evidence).parent.mkdir(parents=True)
        (root/evidence).write_text(json.dumps({'oldSpan':self.GUARD[self.GUARD.index('async function'):].strip(),
                                              'newSpan':source[source.index('async function'):].strip()}))
        entry={'category':'identity-rehash','reason':'Reviewed unrelated function edit','condition':'Byte-identical forwarding, no construction or selection',
               'guardHash':new['guardHash'],'occurrences':1,
               'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646375725',
               'previousSiteId':old['id'],'previousGuardHash':old['guardHash'],'previousCommit':previous,
               'reviewedCommit':reviewed,'pullRequest':'https://github.com/PorticoMediaServer/portico/pull/901',
               'reviewUrl':'https://github.com/PorticoMediaServer/portico/pull/901#issuecomment-123','spanEvidence':evidence}
        (root/APPROVALS).write_text(json.dumps({new['id']:entry}))
        self.assertEqual(main(['--update','--base',previous],root=root),0)
        self.assertEqual(json.loads((root/BASELINE).read_text()),{new['id']:1})
        # A re-hash must consume the old baseline occurrence, never keep both.
        (root/BASELINE).write_text(json.dumps({old['id']:1,new['id']:1}))
        self.assertEqual(main(['--base',previous],root=root),1)

        (root/BASELINE).write_text(json.dumps({new['id']:1}))
        bad={**entry,'reviewedCommit':previous};(root/APPROVALS).write_text(json.dumps({new['id']:bad}))
        self.assertEqual(main(['--base',previous],root=root),1,'the reviewed source must contain the new full guard')
        bad=dict(entry);bad.pop('reviewUrl');(root/APPROVALS).write_text(json.dumps({new['id']:bad}))
        self.assertEqual(main(['--base',previous],root=root),1)
        # Direct identity authorization cannot be used as a transport allowance.
        pin={filename:{'category':'platform-transport','reason':entry['reason'],'sites':[new['id']],
                       'guardHashes':{new['id']:new['guardHash']}}}
        self.assertTrue(base_guard({},pin,{new['id']:1},{},{new['id']:entry}))
        (root/APPROVALS).write_text(json.dumps({new['id']:entry}))
        self.git(root,'add','.');self.git(root,'commit','-qm','Record the reviewed direct identity replacement')
        self.assertEqual(main(['--base','HEAD'],root=root),0,'an already active unchanged record stays valid')
        # Rebinding hashes/evidence to a path-construction edit still fails the
        # byte-for-byte forwarding test, even with otherwise valid metadata.
        changed=source.replace('api.request<unknown>(path,','api.request<unknown>(path+"/built",')
        (root/filename).write_text(changed);self.git(root,'add',filename);self.git(root,'commit','-qm','Change the forwarded path')
        mutated=scan(root)[0];bad={**entry,'guardHash':mutated['guardHash'],'reviewedCommit':self.git(root,'rev-parse','HEAD').strip()}
        (root/evidence).write_text(json.dumps({'oldSpan':self.GUARD[self.GUARD.index('async function'):].strip(),
                                              'newSpan':changed[changed.index('async function'):].strip()}))
        (root/APPROVALS).write_text(json.dumps({mutated['id']:bad}));(root/BASELINE).write_text(json.dumps({mutated['id']:1}))
        self.assertEqual(main(['--base',previous],root=root),1)


    def test_transport_rehash_cannot_duplicate_an_original_single_occurrence(self):
        root,filename,old_sites,pin=self.fixture(self.GUARD)
        old=old_sites[0]
        original={'category':'platform-transport','reason':pin['reason'],'condition':'Original core-built request with lifetime only',
                  'guardHash':old['guardHash'],'occurrences':1,
                  'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/875#issuecomment-6645260082'}
        (root/APPROVALS).write_text(json.dumps({old['id']:original}))
        self.git(root,'add','.');self.git(root,'commit','-qm','Bind the original single transport approval')
        previous=self.git(root,'rev-parse','HEAD').strip()
        for duplicate in [False,True]:
            source=self.GUARD.replace('  const value','  // Unrelated catalogue extraction.\n  const value')
            if duplicate:source=source.replace('await api.request<unknown>(path, method, body, signal)',
                'await Promise.all([api.request<unknown>(path, method, body, signal), api.request<unknown>(path, method, body, signal)])')
            (root/filename).write_text(source);self.git(root,'add',filename);self.git(root,'commit','-qm','Review the changed enclosing function')
            sites=scan(root);site=sites[0];reviewed=self.git(root,'rev-parse','HEAD').strip()
            evidence='docs/client-api-rehashes/transport.json';(root/evidence).parent.mkdir(parents=True,exist_ok=True)
            (root/evidence).write_text(json.dumps({'oldSpan':self.GUARD[self.GUARD.index('async function'):].strip(),
                                                 'newSpan':source[source.index('async function'):].strip()}))
            row={**original,'guardHash':site['guardHash'],'occurrences':len(sites),'previousSiteId':old['id'],
                 'previousGuardHash':old['guardHash'],'previousCommit':previous,'reviewedCommit':reviewed,
                 'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646375725',
                 'pullRequest':'https://github.com/PorticoMediaServer/portico/pull/912',
                 'reviewUrl':'https://github.com/PorticoMediaServer/portico/pull/912#issuecomment-123','spanEvidence':evidence}
            (root/APPROVALS).write_text(json.dumps({old['id']:original,site['id']:row}))
            (root/ALLOWLIST).write_text(json.dumps({filename:{**pin,'sites':[s['id']for s in sites],
                'guardHashes':{site['id']:site['guardHash']}}}))
            with self.subTest(duplicate=duplicate):
                self.assertEqual(main(['--base',previous],root=root),int(duplicate))

    def test_objc_rehash_cannot_substitute_the_message_receiver(self):
        source='''+ (void)prove:(NSString *)origin fingerprint:(NSString *)fingerprint url:(NSString *)requestURL method:(NSString *)method body:(NSString *)body completion:(void (^)(NSDictionary *))completion {
 NSURL *url = [NSURL URLWithString:requestURL];
 NSMutableURLRequest *request = [NSMutableURLRequest requestWithURL:url];
 request.HTTPMethod = method; request.HTTPBody = [body dataUsingEncoding:NSUTF8StringEncoding];
 [[session dataTaskWithRequest:request] resume];
}'''
        root,filename,old_sites,pin=self.fixture(source,'apps/native/Proof.m')
        original={s['id']:{'category':'platform-transport','reason':pin['reason'],'condition':'Core-built proof executed verbatim',
                  'guardHash':s['guardHash'],'occurrences':1,
                  'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6645692710'}for s in old_sites}
        (root/APPROVALS).write_text(json.dumps(original));self.git(root,'add','.');self.git(root,'commit','-qm','Approve original proof receiver')
        previous=self.git(root,'rev-parse','HEAD').strip()
        changed=source.replace('[session dataTaskWithRequest:request]','[otherSession dataTaskWithRequest:request]')
        (root/filename).write_text(changed);self.git(root,'add',filename);self.git(root,'commit','-qm','Substitute transport receiver')
        reviewed=self.git(root,'rev-parse','HEAD').strip();new_sites=scan(root);ledger=dict(original)
        for number,site in enumerate(new_sites):
            old=next(s for s in old_sites if s['callee']==site['callee'])
            evidence=f'docs/client-api-rehashes/objc-{number}.json';(root/evidence).parent.mkdir(parents=True,exist_ok=True)
            (root/evidence).write_text(json.dumps({'oldSpan':source,'newSpan':changed}))
            ledger[site['id']]={**original[old['id']],'guardHash':site['guardHash'],'previousSiteId':old['id'],
                'previousGuardHash':old['guardHash'],'previousCommit':previous,'reviewedCommit':reviewed,
                'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646375725',
                'pullRequest':'https://github.com/PorticoMediaServer/portico/pull/912',
                'reviewUrl':'https://github.com/PorticoMediaServer/portico/pull/912#issuecomment-123','spanEvidence':evidence}
        (root/APPROVALS).write_text(json.dumps(ledger))
        (root/ALLOWLIST).write_text(json.dumps({filename:{**pin,'sites':[s['id']for s in new_sites],
            'guardHashes':{s['id']:s['guardHash']for s in new_sites}}}))
        self.assertEqual(main(['--base',previous],root=root),1)


    def test_exception_expansion_requires_exact_ledger_ruling_and_multiplicity(self):
        root,filename,sites,entry=self.fixture(self.GUARD)
        key=sites[0]['id']
        (root/ALLOWLIST).write_text('{}');(root/BASELINE).write_text(json.dumps(counts(sites)))
        self.git(root,'add','ci');self.git(root,'commit','-qm','Keep the fixture request direct')
        (root/ALLOWLIST).write_text(json.dumps({filename:entry}));(root/BASELINE).write_text('{}')
        self.assertEqual(self.check(root)[0],1)
        approval={'reason':entry['reason'],'condition':'Reviewed unchanged core-built forwarding only','category':'platform-transport',
                  'guardHash':sites[0]['guardHash'],'occurrences':1,
                  'rulingUrl':'https://github.com/PorticoMediaServer/portico/pull/875#issuecomment-6645260082'}
        (root/APPROVALS).write_text(json.dumps({key:approval}))
        self.assertEqual(self.check(root)[0],0)
        # A valid ruling for one occurrence cannot approve two, even if every
        # current hash/category/reason is copied into the new ledger row.
        (root/filename).write_text(self.GUARD.replace('await api.request<unknown>(path, method, body, signal)', 'await Promise.all([api.request<unknown>(path, method, body, signal), api.request<unknown>(path, method, body, signal)])'))
        added=scan(root);new_key=added[0]['id'];self.assertEqual(new_key,added[1]['id'])
        entry['sites']=[new_key,new_key];entry['guardHashes']={new_key:added[0]['guardHash']}
        (root/ALLOWLIST).write_text(json.dumps({filename:entry}))
        approval['guardHash']=added[0]['guardHash'];(root/APPROVALS).write_text(json.dumps({new_key:approval}))
        code,output=self.check(root);self.assertEqual(code,1);self.assertIn('exact lead-ruling approval',output)
        approval['occurrences']=2;approval['rulingUrl']='https://github.com/PorticoMediaServer/portico/pull/875'
        (root/APPROVALS).write_text(json.dumps({new_key:approval}));self.assertEqual(self.check(root)[0],1)


if __name__=='__main__':unittest.main()
