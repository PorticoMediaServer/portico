import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from client_api_census import ROOT, production, native_sites


def swift_string_examples(call):
    return [json.dumps('Use "'+call+'" here'),
            '#"Use "'+call+'" here"#',
            '##"Use "#'+call+'"# here"##',
            '"""\nUse "'+call+'" here\n"""',
            '#"""\nUse """ "'+call+'" here\n"""#',
            '"outer \\("quoted '+call+'")"',
            '#"outer \\#(#"Use "'+call+'" here"#)"#',
            '##"not interpolation \\#('+call+')"##',
            '"outer \\(1 /* ignored /* '+call+' */ comment */ + 2)"']

def swift_regex_examples(call):
    return ['/'+call+'/', '#/'+call+'/#', '##/'+call+'/# still pattern/##',
            '/prefix\\/'+call+'/', '#/escaped\\/#'+call+'/#',
            '/[a/b)"]'+call+'/', '/[[:alpha:]/]'+call+'/', '#/[/#)"]'+call+'/#',
            '#/\n  '+call+' # comment with /# and "\n/#',
            '##/\n  '+call+'\n/##']


class ClientApiCensus(unittest.TestCase):
    def test_all_platform_languages_and_writes_are_in_scope(self):
        for p in ['web/src/request.tsx','apps/native/platform/request.ts','apps/native/Native/Transport.swift',
                  'apps/native/Native/Transport.m','apps/native/android/app/src/main/java/x/Transport.kt',
                  'apps/desktop/src-tauri/src/request.rs','apps/vega/src/bridge/runtime.ts']:
            self.assertTrue(production(p),p)
        for p in ['packages/client-core/src/x.ts','web/test/x.test.ts','apps/native/scripts/tool.ts',
                  'apps/native/test/probe.kt','server-next/receiver/cast/contracts.js']:
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


    def test_swift_async_http_receivers_include_qualified_inline_and_grouped_chains(self):
        receivers=['URLSession.shared','Foundation.URLSession.shared','FoundationNetworking.URLSession.shared',
                   'URLSession(configuration: .ephemeral)','Foundation.URLSession(configuration: .ephemeral)',
                   '(URLSession.shared)','((Foundation.URLSession(configuration: .ephemeral)))',
                   'URLSession(configuration: .ephemeral).delegateQueue.session()',
                   'session','(session)','((session))','self.session','optionalSession?']
        for method in ['bytes','data','upload','download']:
            for receiver in receivers:
                call=f'{receiver}.{method}(for: request)'
                source=f'''import Foundation
func load(session: Foundation.URLSession, optionalSession: URLSession?, request: RequestValue) async throws {{
 let value = try await {call}
}}'''
                with self.subTest(method=method,receiver=receiver):
                    sites=native_sites('apps/native/Stream.swift',source)
                    self.assertEqual([s['callee']for s in sites],[method])
                    self.assertEqual(sites[0]['callText'],call)
        for receiver in ['model','(model)','factory(URLSession.shared)','factory("URLSession.shared")']:
            for method in ['bytes','data','upload','download']:
                self.assertEqual(native_sites('apps/native/Stream.swift',f'let value = try await {receiver}.{method}(for: request)'),[])


    def test_swift_literals_comments_and_nested_interpolation_do_not_create_phantom_calls(self):
        for method in ['bytes','data','upload','download']:
            call=f'URLSession(configuration: .ephemeral).{method}(for: request)'
            for literal in swift_string_examples(call):
                source='let documentation = '+literal+'\n'
                with self.subTest(method=method,literal=literal):
                    self.assertEqual(native_sites('apps/native/Probe.swift',source),[])
                    sites=native_sites('apps/native/Probe.swift',source+'let real = try await '+call+'\n')
                    self.assertEqual([s['callText']for s in sites],[call])
        call='URLSession.shared.bytes(for: request)'
        for literal in ['"outer \\(try await '+call+')"',
                        '#"outer \\#(try await '+call+')"#',
                        '##"outer \\##(try await '+call+')"##',
                        '#"outer \\#("nested \\(try await '+call+')")"#']:
            self.assertEqual([s['callText']for s in native_sites('apps/native/Probe.swift','let value = '+literal)],[call])
        # Interpolation tokens do not churn the enclosing request's old identity.
        source='func load(origin: String) { let r = URLRequest(url: URL(string: "\\(origin)/v1/system")!) }'
        sites=native_sites('apps/native/Probe.swift',source)
        self.assertEqual(sites[0]['expression'].count('origin'),1)


    def test_swift_regex_payloads_are_opaque_inside_interpolation_with_real_call_controls(self):
        for method in ['bytes','data','upload','download']:
            call=f'URLSession.shared.{method}(for: request)'
            for regex in swift_regex_examples(call):
                for source in ['let pattern = '+regex,
                               'let text = """\n\\( '+regex+' )\n"""',
                               '#"outer \\#("nested \\( '+regex+' )")"#']:
                    with self.subTest(method=method,source=source):
                        self.assertEqual(native_sites('apps/native/Probe.swift',source),[])
                        sites=native_sites('apps/native/Probe.swift',source+'\nlet real = try await '+call)
                        self.assertEqual([s['callText']for s in sites],[call])
                source='let text = """\n\\( ('+regex+', try await '+call+') )\n"""'
                self.assertEqual([s['callText']for s in native_sites('apps/native/Probe.swift',source)],[call])
        # Exact independently typechecked regression from the review.
        source='func phantom() -> String { return "\\( #/URLSession.shared.data(for: request)/# )" }'
        self.assertEqual(native_sites('apps/native/Probe.swift',source),[])


    def test_swift_division_and_unapplied_slash_do_not_hide_executable_requests(self):
        call='URLSession.shared.data(for: request)'
        for expression in ['left / '+call+' / right','left/'+call+'/right','left+/'+call+'/right',
                           'array.reduce(1, /) + '+call+' / 5','prefix(/x) + '+call+' / 5']:
            for source in ['let value = '+expression,'let text = "\\( '+expression+' )"']:
                with self.subTest(source=source):
                    self.assertEqual([s['callText']for s in native_sites('apps/native/Probe.swift',source)],[call])
        for source in ['let r = /URLSession.shared.data(for: request)/',
                       'let x = 1\n/URLSession.shared.data(for: request)/',
                       'let r = ^^/URLSession.shared.data(for: request)/']:
            self.assertEqual(native_sites('apps/native/Probe.swift',source),[])
