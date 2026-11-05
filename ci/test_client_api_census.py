import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from client_api_census import ROOT, production, native_sites, classify, compare, base_guard, scan, counts, main, BASELINE, ALLOWLIST, APPROVALS, DETECTIONS


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
