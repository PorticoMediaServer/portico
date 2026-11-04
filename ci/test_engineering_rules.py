import unittest
from commit_bylines import problems
from engineering_rules import BASELINE, compare, counts, tracked


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

    def test_query_read_counts_gets_and_services_outside_the_query_cache(self):
        one = {'query-read': {'web/src/x.ts': 1}}
        for counted in ["const raw = await api.request(routePath('get_home'), 'GET', undefined, signal);",
                        "const raw = await api.request<RouteResponse<'get_me'>>(routePath('get_me'),'GET',undefined,signal);",
                        "const raw = await this.api.request<unknown>(`/v1/items/${encodeURIComponent(id)}/detail`);",
                        "const raw = await api.request(encodedRoutePath('get_libraries_id', {id: encodeURIComponent(id)}) + '?limit=5');",
                        "const raw = await api.requestRaw('/v1/events?waitMs=0', 'GET');",
                        "const raw = await api.request(\n  routePath('get_libraries'),\n  'GET',\n  undefined, signal);",
                        'const home = new HomeService(api, {observation});',
                        'const saved = new PersonalSavedService(\n  api);']:
            self.assertEqual(self.found(counted), one, counted)
        for spared in ["await api.request(routePath('patch_preferences'), 'PATCH', body, signal);",
                       "await api.request(path, 'POST');",
                       'await api.request(path, method, body);',
                       "await navigator.locks.request('portico-lock', async () => work());",
                       'const service = createHomeService(api);',
                       'const s = new Set(ids); const m = new Map();',
                       "const raw = await api.request(routePath('get_me'), 'GET', undefined, signal); // query-read: kept (sign-in proves who the viewer is before any cache exists)"]:
            self.assertEqual(self.found(spared), {}, spared)
        # The marker is held to the same rules as fence-compare's: a reason, a real comment, one read per marker.
        self.assertEqual(self.found("api.request(routePath('get_me')); // query-read: kept"), one)
        self.assertEqual(self.found("api.request(a); api.request(b); // query-read: kept (first only)"), one)
        self.assertEqual(self.found("api.request(a); // fence-compare: kept (another rule's marker)"), one)

    def test_query_read_is_at_home_in_the_query_cache_only(self):
        read = "const raw = await api.request(routePath('get_home'), 'GET', undefined, signal);"
        self.assertEqual(self.found(read, 'packages/client-core/src/query/viewer-queries.ts'), {})
        self.assertEqual(self.found(read, 'packages/client-core/src/query/keys.ts'), {})
        self.assertEqual(self.found(read, 'packages/client-core/src/query-helpers.ts'), {'query-read': {'packages/client-core/src/query-helpers.ts': 1}})
        self.assertEqual(self.found(read, 'packages/client-core/src/home-service.ts'), {'query-read': {'packages/client-core/src/home-service.ts': 1}})

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

    def test_the_committed_baseline_matches_the_tree(self):
        import json
        self.assertEqual(compare(counts(tracked()), json.loads(BASELINE.read_text())), ([], []))


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
