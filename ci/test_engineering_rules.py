import json
import unittest
from pathlib import Path
from commit_bylines import problems
from engineering_rules import BASELINE, compare, counts, query_reads, tracked


QUERY_FIXTURE = """\
import {HttpLocalApi} from './client';
declare const api: HttpLocalApi;
declare const loose: any;
declare const signal: AbortSignal;
declare const id: string, a: string, b: string, m: string;
declare const verb: 'POST' | 'PATCH';
declare const either: 'GET' | 'POST';
declare const f: (x: string) => string, g: (x: string) => string, h: (x: string) => string;
declare function encodedRoutePath(name: string, params: Record<string, string>): string;
declare const lifetime: {request<T>(read: () => Promise<T>): Promise<T>};
declare const factOwner: {request(group: 'directory' | 'members'): void};
declare const port: {request(tick: number): void};
declare const dock: {request(panel: string): boolean};
declare const reader: {request<T>(path: string, signal?: AbortSignal): Promise<T>};
declare const forwarding: {request<T>(...args: Parameters<HttpLocalApi['request']>): Promise<T>};
export async function cases(this: {send(path: string, method?: string): Promise<unknown>}) {
  await api.request('/v1/home'); /*read*/
  await api.request('/v1/home', 'GET', undefined, signal); /*read*/
  await api.request('/v1/home', undefined, undefined, signal); /*read*/ // an explicit undefined method
  await api.request('/v1/home', 'get'); /*read*/ // lower case
  await api.request(encodedRoutePath('a', {id: encodeURIComponent(f(g(h(id))))})); /*read*/ // four calls deep
  await api.request(`/v1/items?ids=${a},${b}`); /*read*/ // a template with a comma
  await api.requestRaw('/v1/events', 'GET'); /*read*/
  await api.requestConsole('/v1/console/x'); /*read*/
  await this.send('/v1/home', 'GET'); /*read*/ // a requester under another name
  await fetch('/v1/files/1'); /*read*/
  await fetch('/v1/files/1', {method: 'GET'}); /*read*/
  await fetch('/v1/files/1', {method: m}); /*read*/ // not statically known
  await api.request('/v1/home', m); /*read*/ // not statically known
  await api.request('/v1/home', either); /*read*/
  await loose.request('/v1/home'); /*read*/ // unresolved, named like a requester
  await reader.request('/v1/home', signal); /*read*/ // a requester with no method
  await forwarding.request('/v1/guide', 'GET', undefined, signal); /*read*/ // a rest-parameter wrapper, read by position
  await forwarding.request('/v1/guide', 'POST', {}); /*not*/
  await api.request('/v1/home', 'POST', {}); /*not*/
  await api.request('/v1/home', 'patch', {}); /*not*/
  await api.request('/v1/home', verb, {}); /*not*/
  await fetch('/v1/files/1', {method: 'DELETE'}); /*not*/
  await lifetime.request(() => api.request('/v1/setup-code/review', 'POST', {})); /*not*/ // a closure wrapper around a write
  factOwner.request('directory'); /*not*/
  port.request(3); /*not*/
  dock.request('info'); /*not*/
  await navigator.locks.request('portico', async () => {}); /*not*/
  await navigator.wakeLock.request('screen'); /*not*/
  const home = new HomeService(api); /*read*/
  const ids = new Set<string>(); const byId = new Map<string, string>(); /*not*/
  const get = (path: string) => api.request(path); /*read*/ // a wrapper counts once, here
  await get('/v1/a'); await get('/v1/b'); /*not*/ // and never at its callers
  return [home, ids, byId];
}
class HomeService { constructor(readonly api: HttpLocalApi) {} }
"""
QUERY_CLIENT = """\
export class HttpLocalApi {
  request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> { return fetch(path, {method, body: JSON.stringify(body), signal}).then(r => r.json()); }
  requestRaw(path: string, method: string, options: {signal?: AbortSignal} = {}): Promise<Response> { return fetch(path, {method, signal: options.signal}); }
  requestConsole<T>(path: string, method?: string): Promise<T> { return this.request<T>(path, method); }
}
"""


def query_read_fixture():
    import tempfile
    root = tempfile.mkdtemp(prefix='query-read-')
    (Path(root) / 'src').mkdir()
    (Path(root) / 'tsconfig.json').write_text(json.dumps({'compilerOptions': {'target': 'ES2022', 'lib': ['ES2022', 'DOM'], 'module': 'ESNext',
        'moduleResolution': 'bundler', 'strict': True, 'noEmit': True, 'skipLibCheck': True, 'types': []}, 'include': ['src/**/*.ts']}))
    (Path(root) / 'src' / 'fixture.ts').write_text(QUERY_FIXTURE)
    (Path(root) / 'src' / 'client.ts').write_text(QUERY_CLIENT)
    try:
        return query_reads(['src/fixture.ts'], root=root, configs=['tsconfig.json'])
    finally:
        import shutil
        shutil.rmtree(root, ignore_errors=True)


class EngineeringRuleTests(unittest.TestCase):
    def found(self, source, path='web/src/x.ts'):
        return counts([path], read=lambda _: source)

    def test_patterns_catch_the_bug_class_and_spare_the_fix(self):
        self.assertEqual(self.found('if (a.accessToken !== b.accessToken) return;'), {'token-compare': {'web/src/x.ts': 1}})
        self.assertEqual(self.found('const t = setInterval(tick, 1000);'), {'set-interval': {'web/src/x.ts': 1}})
        self.assertEqual(self.found("const r = res.headers.get('Retry-After');"), {'retry-after': {'web/src/x.ts': 1}})
        self.assertEqual(self.found('catch (e) { conn.dispose(); throw e; }'), {'throwing-cleanup': {'web/src/x.ts': 1}})
        self.assertEqual(self.found('catch (error) {await completed.release(); throw error;}'), {'throwing-cleanup': {'web/src/x.ts': 1}})
        for fixed in ['catch (e) { await releaseThenRethrow(e, "x", () => conn.dispose()); }',
                      'catch (e) { await r.release().catch(report); throw e; }',
                      "compareSessionAuthority(held, current) === 'same'",
                      'repeat(task, {everyMs: 1000, backoff})']:
            self.assertEqual(self.found(fixed), {}, fixed)

    def test_client_data_patterns(self):
        self.assertEqual(self.found('if (data.scope.viewerFence !== this.state.data.scope.viewerFence) throw x;'), {'fence-compare': {'web/src/x.ts': 1}})
        self.assertEqual(self.found('if (fence === held.fence) return;'), {'fence-compare': {'web/src/x.ts': 1}})
        self.assertEqual(self.found('const pages = usePageMemory(); const kept = pages.recall(key);'), {'page-memory': {'web/src/x.ts': 2}})
        self.assertEqual(self.found('export function useBrowseLease(context) {'), {'lease-layer': {'web/src/x.ts': 1}})
        for fixed in ['useQuery({queryKey: queryKeys.itemDetail(id, "none"), queryFn, meta: {dependencies}})',
                      'if (next.personal.revision < held.personal.revision) return;',
                      'const lease = acquireLease();',
                      'queries.moved(dependsOn.personalTitle(library, id));']:
            self.assertEqual(self.found(fixed), {}, fixed)

    def test_fence_compare_narrowing(self):
        # Step 7 item 5: shape and presence checks, and reads through an object named fence, are not fence compares.
        for spared in ["if (!record(raw) || typeof raw.viewerFence !== 'string') bad();",
                       'if (editingFence !== undefined && ready) save();',
                       'if (snapshot.fence === null) return;',
                       'if (destination.sessionId === playback.fence.sessionId) open();',
                       'if (session?.id !== fence?.sessionId) return;',
                       'return a.serverId === b.serverId && a.viewerFence === b.viewerFence; // fence-compare: kept (same answer)']:
            self.assertEqual(self.found(spared), {}, spared)
        for counted in ['if (fence === held.fence) return;',
                        'if (preview.viewerFence !== ticket.context.scope.privacyFence) throw x;',
                        'if (a.viewerFence !== b.fence.viewerFence) return;',
                        '// fence-compare: kept (a reason on another line exempts nothing)\nif (fence === held.fence) return;']:
            self.assertEqual(self.found(counted), {'fence-compare': {'web/src/x.ts': 1}}, counted)

    def test_fence_compare_narrowing_keeps_a_call_or_a_template_on_a_fence(self):
        # A method or optional call on a fence, or a template built from one, is still a fence compare.
        for counted in ['if (current !== doc.viewerFence.trim()) return;',
                        'if (current !== doc.viewerFence?.toString()) return;',
                        'if (current !== doc.viewerFence?.toString?.()) return;',
                        'if (viewerFence !== `${held.viewerFence}`) return;']:
            self.assertEqual(self.found(counted), {'fence-compare': {'web/src/x.ts': 1}}, counted)
        # A field read through a playback fence and a template with no substitution stay spared.
        for spared in ['if (id === previous.fence.playback.scope.serverId) return;',
                       'if (fence === `none`) return;']:
            self.assertEqual(self.found(spared), {}, spared)

    def test_the_kept_marker_is_a_line_comment_with_a_reason_that_spares_one_compare(self):
        one = {'fence-compare': {'web/src/x.ts': 1}}
        # No reason, an empty reason, or no parentheses: nothing is spared.
        for unreasoned in ['if (fence === held.fence) return; // fence-compare: kept',
                           'if (fence === held.fence) return; // fence-compare: kept ()',
                           'if (fence === held.fence) return; // fence-compare: kept ( )',
                           'if (fence === held.fence) return; // fence-compare: kept because same answer']:
            self.assertEqual(self.found(unreasoned), one, unreasoned)
        # The marker text inside a string, a template or a block comment is not a marker.
        for quoted in ["const tag = 'fence-compare: kept (x)'; if (fence === held.fence) return;",
                       'const tag = "// fence-compare: kept (x)"; if (fence === held.fence) return;',
                       'const tag = `// fence-compare: kept (x)`; if (fence === held.fence) return;',
                       'const tag = `a\n// fence-compare: kept (x)`; if (fence === held.fence) return;',
                       'if (fence === held.fence) return; /* fence-compare: kept (x) */',
                       "if (fence === held.fence) return; const s = 'it\\'s // fence-compare: kept (x)';"]:
            self.assertEqual(self.found(quoted), one, quoted)
        # A real comment after a string that holds quotes and a template substitution still counts as the marker.
        self.assertEqual(self.found("const s = `${a['b']}`; if (fence === held.fence) return; // fence-compare: kept (same answer)"), {})
        # One marker spares one compare: the second on its line still counts.
        self.assertEqual(self.found('a.viewerFence === b.viewerFence && c.viewerFence !== d.viewerFence // fence-compare: kept (first one only)'), one)
        # It spares its own line only, wherever the compares sit in the file.
        self.assertEqual(self.found('if (fence === held.fence) a(); // fence-compare: kept (same answer)\nif (fence === other.fence) b();'), one)

    def test_query_read_counts_reads_the_checker_resolves_to_a_requester(self):
        lines = query_read_fixture()
        # Each case in fixture.ts ends with `// read` (counted) or `// not` (spared); a line may hold several.
        source = QUERY_FIXTURE.splitlines()
        expected = [i + 1 for i, line in enumerate(source) for _ in range(line.count('/*read*/'))]
        self.assertEqual(lines.get('src/fixture.ts', []), expected,
                         '\n'.join(f'{i + 1}: {line}' for i, line in enumerate(source) if '/*read*/' in line or '/*not*/' in line))

    def test_query_read_cache_follows_every_file_the_checker_loads(self):
        # Review 6630380810: a requester declared in an exempt file decides the count elsewhere. Rename its `method`
        # parameter and a POST through it becomes a read-only call; the reused answer must not hide that.
        import shutil
        import tempfile
        root = Path(tempfile.mkdtemp(prefix='query-read-cache-'))
        try:
            (root / 'tsconfig.json').write_text(json.dumps({'compilerOptions': {'target': 'ES2022', 'lib': ['ES2022', 'DOM'], 'module': 'ESNext',
                'moduleResolution': 'bundler', 'strict': True, 'noEmit': True, 'skipLibCheck': True, 'types': []}, 'include': ['src/**/*.ts']}))
            (root / 'src' / 'query').mkdir(parents=True)
            requester = root / 'src' / 'query' / 'viewer-queries.ts'
            requester.write_text('export type ViewerRequester = {request<T>(path: string, method?: string, body?: unknown): Promise<T>};\n')
            (root / 'src' / 'probe.ts').write_text("import type {ViewerRequester} from './query/viewer-queries';\ndeclare const requester: ViewerRequester;\nvoid requester.request('/v1/x', 'POST', {});\n")
            self.assertEqual(query_reads(['src/probe.ts'], root=root, configs=['tsconfig.json']), {})
            self.assertEqual(query_reads(['src/probe.ts'], root=root, configs=['tsconfig.json']), {}, 'served again unchanged')
            requester.write_text('export type ViewerRequester = {request<T>(path: string, verb?: string, body?: unknown): Promise<T>};\n')
            self.assertEqual(query_reads(['src/probe.ts'], root=root, configs=['tsconfig.json']), {'src/probe.ts': [3]})
            (root / 'src' / 'extra.ts').write_text('export const untracked = 1;\n')
            self.assertEqual(query_reads(['src/probe.ts'], root=root, configs=['tsconfig.json']), {'src/probe.ts': [3]}, 'an untracked file joins the key')
        finally:
            shutil.rmtree(root, ignore_errors=True)

    def test_query_read_keeps_the_marker_rules(self):
        path = 'web/src/x.ts'
        def n(text, lines):
            return counts([path], read=lambda _: text, reads={path: lines}).get('query-read', {}).get(path, 0)
        self.assertEqual(n("api.request(path); // query-read: kept (sign-in proves who the viewer is)", [1]), 0)
        self.assertEqual(n("api.request(path); // query-read: kept", [1]), 1, 'a reason is required')
        self.assertEqual(n("api.request('// query-read: kept (x)');", [1]), 1, 'not inside a string')
        self.assertEqual(n("api.request(a); api.request(b); // query-read: kept (first only)", [1, 1]), 1, 'one read per marker')
        self.assertEqual(n("api.request(a); // fence-compare: kept (another rule)", [1]), 1)
        self.assertEqual(counts(['packages/client-core/src/query/viewer-queries.ts'], read=lambda _: 'x', reads={'packages/client-core/src/query/viewer-queries.ts': [1]}), {},
                         'the query cache is its home')

    def test_authorized_home_is_kept_on_purpose_and_only_by_its_marker(self):
        path = 'apps/native/src-next/screens/home/model.ts'
        source = (BASELINE.parent.parent / path).read_text(encoding='utf-8')
        line = next(line for line in source.splitlines() if 'document.viewerFence === authority.viewerFence' in line)
        self.assertIn('fence-compare: kept', line)
        self.assertEqual(self.found(line, path), {})
        self.assertEqual(self.found(line.split('//')[0], path), {'fence-compare': {path: 1}})

    def test_the_homes_of_the_helpers_are_exempt(self):
        self.assertEqual(self.found('a.accessToken === b.accessToken', 'packages/client-core/src/session-authority.ts'), {})
        self.assertEqual(self.found("'retry-after'", 'packages/client-core/src/timer.ts'), {})

    def test_counts_may_fall_but_never_rise(self):
        baseline = {'set-interval': {'a.ts': 2}}
        self.assertEqual(compare({'set-interval': {'a.ts': 2}}, baseline), ([], []))
        self.assertEqual(compare({'set-interval': {'a.ts': 3}}, baseline), ([('set-interval', 'a.ts', 3, 2)], []))
        self.assertEqual(compare({'set-interval': {'b.ts': 1, 'a.ts': 2}}, baseline), ([('set-interval', 'b.ts', 1, 0)], []))
        self.assertEqual(compare({}, baseline), ([], [('set-interval', 'a.ts', 0, 2)]))

    def test_the_committed_baseline_bounds_the_tree(self):
        import json
        self.assertEqual(compare(counts(tracked()), json.loads(BASELINE.read_text()))[0], [])


class CommitBylineTests(unittest.TestCase):
    justin = 'Justin Ehler <justin@justinehler.com>'

    def test_ai_identities_and_trailers_are_refused(self):
        bad = [('a' * 40, 'Claude <noreply@anthropic.com>', self.justin, 'Fix it'),
               ('b' * 40, self.justin, self.justin, 'Fix it\n\nCo-Authored-By: Claude <noreply@anthropic.com>'),
               ('c' * 40, self.justin, self.justin, 'Fix it\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)'),
               ('d' * 40, self.justin, 'Codex <codex@openai.com>', 'Fix it'),
               ('e' * 40, self.justin, self.justin, 'Fix it\n\nCo-authored-by: GPT-5 <ai@example.com>')]
        self.assertEqual(len(problems(bad)), 5)

    def test_justins_own_commits_pass_even_when_they_mention_a_tool(self):
        good = [('f' * 40, self.justin, self.justin, 'Retire the old Codex switcher notes'),
                ('0' * 40, self.justin, self.justin, 'Follow the Claude review\n\nThe reviewer asked for a lock-order note.'),
                ('1' * 40, self.justin, self.justin, 'Generate development contracts again\n\nGenerated by apigen, never hand-edited.'),
                ('2' * 40, self.justin, self.justin, 'Add a copilot setting to the player\n\nCo-authored-by: Someone Else <someone@example.com>')]
        self.assertEqual(problems(good), [])


if __name__ == '__main__':
    unittest.main()
