import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from client_api_census import ROOT, production, native_sites, classify, compare, base_guard


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
        e={'web/src/guard.ts':{'category':'platform-transport','reason':'Reviewed exact core-built request guard','sites':['x']}}
        site={'file':'web/src/guard.ts','id':'x','line':1}
        with self.assertRaises(ValueError): classify([site],e)
        self.assertEqual(len(classify([dict(site,forwardedRequest=True)],e)[1]),1)

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

''')
            r=subprocess.run(['node',str(ROOT/'ci/client_api_sites.mjs')],input=json.dumps({'root':str(root),'files':['src/x.ts']}),text=True,capture_output=True,check=True)
            sites=json.loads(r.stdout)
            self.assertEqual([s['kind'] for s in sites],['request','request','request','url'],sites)
            self.assertEqual([s['line'] for s in sites],[9,10,12,13])


if __name__=='__main__':unittest.main()
