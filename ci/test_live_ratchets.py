import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout, redirect_stderr
from unittest.mock import patch

import client_api_census as census
import engineering_rules as engineering
from ratchet_base import event_base, event_bases, merge_base, source_snapshot, queue_increments


class LiveRatchets(unittest.TestCase):
    def setUp(self):
        self.directory=tempfile.TemporaryDirectory();self.addCleanup(self.directory.cleanup)
        self.root=Path(self.directory.name)
        self.git('init','-q','-b','main');self.git('config','user.name','Justin Ehler');self.git('config','user.email','justin@justinehler.com')
        self.path='web/src/request.ts'
        (self.root/self.path).parent.mkdir(parents=True)
        for config in engineering.QUERY_READ_CONFIGS:
            destination=self.root/config;destination.parent.mkdir(parents=True,exist_ok=True)
            destination.write_text(json.dumps({'compilerOptions':{'target':'ES2022','lib':['ES2022','DOM'],'strict':True,'types':[]},
                **({'include':['src/**/*.ts']} if config=='web/tsconfig.json' else {'files':[]})}))
        self.prefix='declare function fetch(input: string, init?: object): Promise<unknown>;\n'
        self.source=self.prefix+"fetch('/v1/kept');\nfetch('/v1/removed');\nsetInterval(tick, 1000);\nsetInterval(tick, 1000);\n"
        (self.root/self.path).write_text(self.source)
        self.git('add','.')
        (self.root/census.ALLOWLIST).write_text('{}')
        (self.root/census.BASELINE).write_text(json.dumps(census.counts(census.scan(self.root))))
        (self.root/'ci/engineering-rules-baseline.json').write_text(json.dumps(engineering.source_counts(self.root)))
        self.base=self.commit('Record upper bounds')

    def git(self,*args):
        return subprocess.check_output(['git',*args],cwd=self.root,text=True,stderr=subprocess.PIPE).strip()

    def commit(self,message):
        self.git('add','.');self.git('commit','-qm',message);return self.git('rev-parse','HEAD')

    def checks(self,base,predecessor=None,queue_head=None):
        args=['--base',base]+(['--predecessor',predecessor]if predecessor else [])+(['--queue-head',queue_head]if queue_head else [])
        with redirect_stdout(io.StringIO()),redirect_stderr(io.StringIO()):
            return (census.main(args,root=self.root),engineering.main(args,root=self.root))

    def test_removal_passes_without_editing_either_baseline(self):
        (self.root/self.path).write_text(self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n")
        self.assertEqual(self.checks(self.base),(0,0))
        self.assertEqual(self.git('diff','--name-only'),self.path)

    def test_stale_high_baselines_do_not_allow_reintroduced_sites_or_count_growth(self):
        (self.root/self.path).write_text(self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n")
        lower=self.commit('Remove calls without editing upper bounds')
        (self.root/self.path).write_text(self.source)
        self.assertEqual(self.checks(lower),(1,1))
        # Updating a stale-high baseline cannot conceal the growth either.
        with redirect_stdout(io.StringIO()),redirect_stderr(io.StringIO()):
            self.assertEqual(census.main(['--update','--base',lower],root=self.root),1)
            self.assertEqual(engineering.main(['--update','--base',lower],root=self.root),1)

    def test_merge_group_checks_all_queued_changes_against_target_tip(self):
        self.git('update-ref','refs/remotes/origin/main',self.base)
        # First group grows below a deliberately stale-high ceiling. The second
        # changes no counted call. Comparing only with group.base_sha hides it.
        (self.root/self.path).write_text(self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n")
        target=self.commit('Main removes calls, retaining upper bounds')
        self.git('update-ref','refs/remotes/origin/main',target)
        self.git('checkout','-qb','queued-first')
        (self.root/self.path).write_text(self.source)
        first_source=self.commit('First queued PR adds calls')
        self.git('checkout','-qb','group-first',target)
        self.git('merge','--no-ff','-qm','Synthetic first merge group',first_source)
        first=self.git('rev-parse','HEAD')
        self.git('checkout','-qb','queued-second',target)
        (self.root/'web/src/unrelated.ts').write_text('export {};\n')
        second_source=self.commit('Second queued PR is unrelated')
        self.git('checkout','-qb','group-second',first)
        self.git('merge','--no-ff','-qm','Synthetic second merge group',second_source)
        head=self.git('rev-parse','HEAD')
        self.assertEqual(len(self.git('show','-s','--format=%P',head).split()),2)
        event={'merge_group':{'base_ref':'refs/heads/main','base_sha':first,'head_sha':head}}
        with patch('plan.command',side_effect=lambda args,cwd=None:subprocess.check_output(args,cwd=self.root,text=True)):
            selected,predecessor=event_bases(event,{'GITHUB_EVENT_NAME':'merge_group'})
        self.assertEqual((selected,predecessor),(target,first))
        self.assertEqual(self.checks(first),(0,0),'using only the previous queued group would miss growth')
        self.assertEqual(self.checks(selected,predecessor),(1,1))
        # Once that first group has merged, compare the remaining group with it.
        self.git('update-ref','refs/remotes/origin/main',first)
        with patch('plan.command',side_effect=lambda args,cwd=None:subprocess.check_output(args,cwd=self.root,text=True)):
            self.assertEqual(event_base(event,{'GITHUB_EVENT_NAME':'merge_group'}),first)

    def test_later_group_cannot_spend_an_earlier_groups_removal_credit(self):
        target=self.base;self.git('update-ref','refs/remotes/origin/main',target)
        self.git('checkout','-qb','queued-first')
        (self.root/self.path).write_text(self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n")
        first_source=self.commit('First queued PR removes calls without baseline edits')
        self.git('checkout','-qb','group-first',target)
        self.git('merge','--no-ff','-qm','Synthetic first group',first_source)
        first=self.git('rev-parse','HEAD')
        self.git('checkout','-qb','queued-second',first)
        (self.root/self.path).write_text(self.source)
        second_source=self.commit('Later queued PR restores the removed calls')
        self.git('checkout','-qb','group-second',first)
        self.git('merge','--no-ff','-qm','Synthetic second group',second_source)
        head=self.git('rev-parse','HEAD');self.assertEqual(len(self.git('show','-s','--format=%P',head).split()),2)
        event={'merge_group':{'base_ref':'refs/heads/main','base_sha':first,'head_sha':head}}
        with patch('plan.command',side_effect=lambda args,cwd=None:subprocess.check_output(args,cwd=self.root,text=True)):
            aggregate,predecessor=event_bases(event,{'GITHUB_EVENT_NAME':'merge_group'})
        self.assertEqual((aggregate,predecessor),(target,first))
        self.assertEqual(self.checks(aggregate),(0,0),'aggregate-only checking incorrectly spends removal credit')
        self.assertEqual(self.checks(aggregate,predecessor),(1,1))

    def test_two_harmless_queued_groups_pass_before_and_after_main_advances(self):
        self.git('update-ref','refs/remotes/origin/main',self.base)
        (self.root/self.path).write_text(self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n")
        first=self.commit('First group removes calls')
        (self.root/'web/src/unrelated.ts').write_text('export {};\n');head=self.commit('Second group is harmless')
        event={'merge_group':{'base_ref':'refs/heads/main','base_sha':first,'head_sha':head}}
        for target in [self.base,first]:
            self.git('update-ref','refs/remotes/origin/main',target)
            with patch('plan.command',side_effect=lambda args,cwd=None:subprocess.check_output(args,cwd=self.root,text=True)):
                aggregate,predecessor=event_bases(event,{'GITHUB_EVENT_NAME':'merge_group'})
            self.assertEqual(self.checks(aggregate,predecessor or None),(0,0))

    def group(self,parent,name,source=None,merge=True):
        self.git('checkout','-qb',name+'-source',parent)
        if source is not None:(self.root/self.path).write_text(source)
        else:(self.root/('web/src/'+name+'.ts')).write_text('export {};\n')
        commit=self.commit(name+' source')
        if not merge:return commit
        self.git('checkout','-qb',name+'-group',parent)
        self.git('merge','--no-ff','-qm',name+' synthetic queue merge',commit)
        head=self.git('rev-parse','HEAD')
        self.assertEqual(len(self.git('show','-s','--format=%P',head).split()),2)
        return head

    def three_group_case(self,cleanup=False,merge=True):
        removed=self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n"
        first=self.group(self.base,'A',removed,merge=merge)
        second=self.group(first,'B',self.source,merge=merge)
        head=self.group(second,'C',removed if cleanup else None,merge=merge)
        before=self.git('status','--porcelain')
        self.assertEqual(self.checks(self.base,second),(0,0),'aggregate and immediate ranges alone miss failed B')
        self.assertEqual(queue_increments(self.root,self.base,head),[(self.base,first),(first,second),(second,head)])
        self.assertEqual(self.checks(self.base,second,head),(1,1))
        self.assertEqual(self.git('status','--porcelain'),before,'historical checks do not alter the checkout/index')

    def test_clean_third_group_cannot_carry_a_failed_restoration_under_headgreen(self):
        self.three_group_case()

    def test_every_entry_is_checked_even_when_a_later_entry_removes_the_growth(self):
        self.three_group_case(cleanup=True)

    def test_linear_rebase_queue_entries_are_each_checked(self):
        self.three_group_case(merge=False)

    def test_three_harmless_groups_pass_full_history_before_and_after_main_advances(self):
        first=self.group(self.base,'A',self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n")
        second=self.group(first,'B');head=self.group(second,'C')
        for target in [self.base,first]:self.assertEqual(self.checks(target,second,head),(0,0))

    def test_export_subst_cannot_hide_restoration_in_linear_or_merged_queue_history(self):
        original=self.prefix+"fetch('/v1/kept');\n// \nfetch('/v1/removed');\n"
        removed=self.prefix+"fetch('/v1/kept');\n// $Format:%n$fetch('/v1/removed');\n"
        (self.root/self.path).write_text(original)
        (self.root/census.BASELINE).write_text(json.dumps(census.counts(census.scan(self.root))))
        target=self.commit('Record original comment-bearing request identity and upper bound')
        for merge in [False,True]:
            with self.subTest(merge=merge):
                name='merged' if merge else 'linear'
                self.git('checkout','-qb',name+'-target',target)
                (self.root/'.gitattributes').write_text(self.path+' export-subst\n')
                first=self.group(target,name+'A',removed,merge=merge)
                second=self.group(first,name+'B',original,merge=merge)
                head=self.group(second,name+'C',merge=merge)
                before=(self.git('rev-parse','HEAD'),self.git('write-tree'),self.git('status','--porcelain'))
                self.assertEqual(self.checks(target),(0,0),'aggregate alone sees no net change')
                self.assertEqual(self.checks(first),(1,1),'the restored call must grow against the exact removal blob')
                for base in [target,first]:self.assertEqual(self.checks(base,second,head),(1,1))
                self.assertEqual((self.git('rev-parse','HEAD'),self.git('write-tree'),self.git('status','--porcelain')),before)
                (self.root/'.gitattributes').unlink();head=self.commit('Later removal of archive attribute')
                self.assertEqual(self.checks(target,second,head),(1,1))

    def test_snapshot_bytes_ignore_export_attributes_filters_and_worktree_edits(self):
        source=b'// $Format:%n$\r\nfetch("/v1/exact");\r\n'
        (self.root/self.path).write_bytes(source)
        native='apps/native/Probe.swift';(self.root/native).parent.mkdir(parents=True,exist_ok=True)
        (self.root/native).write_bytes(b'// $Format:%n$let x = try await URLSession.shared.data(for: request)\n')
        commits=[]
        for attributes in ['export-subst','export-ignore','export-ignore export-subst']:
            (self.root/'.gitattributes').write_text(''.join(path+' '+attributes+' filter=replace\n' for path in [self.path,native]))
            commits.append(self.commit('Record '+attributes+' on exact source blobs'))
        self.git('config','filter.replace.smudge','false');self.git('config','filter.replace.required','true')
        (self.root/self.path).write_text('Uncommitted worktree substitution\n')
        (self.root/native).write_text('Uncommitted native substitution\n')
        for commit in commits:
            with self.subTest(commit=commit),source_snapshot(self.root,commit) as (snapshot,paths):
                self.assertIn(self.path,paths);self.assertIn(native,paths)
                self.assertEqual((snapshot/self.path).read_bytes(),source)
                for path in paths:
                    self.assertEqual((snapshot/path).read_bytes(),subprocess.check_output(['git','cat-file','blob',commit+':'+path],cwd=self.root))
        self.assertEqual((self.root/self.path).read_text(),'Uncommitted worktree substitution\n')

    def test_missing_or_incomplete_source_blob_fails_both_gates_closed(self):
        real_run=subprocess.run
        oid=self.git('rev-parse',self.base+':apps/desktop/tsconfig.json').encode()
        for output in [None,b'missing missing\n',oid+b' blob 20\nshort\n',oid+b' blob 1\nx',oid+b' blob 0\n\nextra']:
            def fault(args,**kwargs):
                if args==['git','cat-file','--batch']:
                    if output is None:raise subprocess.CalledProcessError(128,args,stderr=b'injected missing object')
                    return subprocess.CompletedProcess(args,0,stdout=output,stderr=b'')
                return real_run(args,**kwargs)
            with self.subTest(output=output),patch('ratchet_base.subprocess.run',side_effect=fault):
                with redirect_stdout(io.StringIO()),redirect_stderr(io.StringIO()):
                    self.assertEqual(census.main(['--base',self.base],root=self.root),1)
                    with self.assertRaises((ValueError,subprocess.CalledProcessError)):
                        engineering.main(['--base',self.base],root=self.root)

    def test_historical_source_symlinks_fail_closed_instead_of_omitting_the_file(self):
        (self.root/self.path).unlink();(self.root/self.path).symlink_to('/outside/source.ts')
        commit=self.commit('Tracked source is not a regular blob')
        with self.assertRaisesRegex(ValueError,'not a regular blob'):
            with source_snapshot(self.root,commit):self.fail('must reject the missing regular source')

    def test_incomplete_queue_boundaries_fail_closed(self):
        first=self.group(self.base,'A');head=self.group(first,'B')
        real_output=subprocess.check_output
        def missing_entry(args,**kwargs):
            if args[:2]==['git','log']:return head+' '+first+'\n'
            return real_output(args,**kwargs)
        with patch('ratchet_base.subprocess.check_output',side_effect=missing_entry):
            with self.assertRaisesRegex(ValueError,'every first-parent'):queue_increments(self.root,self.base,head)

    def test_historical_baseline_blob_read_failure_fails_closed(self):
        (self.root/self.path).write_text(self.prefix+"fetch('/v1/kept');\nsetInterval(tick, 1000);\n")
        lower=self.commit('Remove calls, retaining high bounds')
        (self.root/self.path).write_text(self.source)
        real_run=subprocess.run
        def fault(args,**kwargs):
            if args==['git','show',lower+':'+census.BASELINE]:
                return subprocess.CompletedProcess(args,128,stdout='',stderr='injected historical object read failure')
            return real_run(args,**kwargs)
        output=io.StringIO()
        with patch('client_api_census.subprocess.run',side_effect=fault),redirect_stdout(output),redirect_stderr(output):
            self.assertEqual(census.main(['--base',lower],root=self.root),1)
        self.assertIn('Cannot read historical '+census.BASELINE,output.getvalue())

    def test_manual_self_comparisons_and_unconfigured_pushes_fail_closed(self):
        with patch('plan.command',side_effect=lambda args,cwd=None:subprocess.check_output(args,cwd=self.root,text=True)):
            with self.assertRaisesRegex(ValueError,'earlier base'):
                event_base({}, {'GITHUB_EVENT_NAME':'workflow_dispatch','CI_BASE':self.base})
        with self.assertRaisesRegex(ValueError,'explicit CI_BASE'):event_base({}, {'GITHUB_EVENT_NAME':'push'})

    def test_explicit_target_is_reduced_to_the_actual_branch_merge_base(self):
        self.git('branch','target')
        (self.root/self.path).write_text(self.prefix+"fetch('/v1/kept');\n")
        branch=self.commit('Slice removes calls')
        self.git('checkout','-q','target')
        (self.root/'other.txt').write_text('target advanced\n');tip=self.commit('Target advances independently')
        self.git('checkout','-q',branch)
        self.assertEqual(merge_base(self.root,tip),self.base)
        self.assertEqual(self.checks(tip),(0,0))

    def test_snapshot_preserves_npm_workspace_symbol_resolution(self):
        (self.root/'package.json').write_text(json.dumps({'workspaces':['packages/client-core']}))
        core=self.root/'packages/client-core';(core/'src').mkdir(parents=True)
        (core/'package.json').write_text(json.dumps({'name':'@portico/client-core','types':'src/index.ts'}))
        (core/'src/index.ts').write_text('export declare class HttpLocalApi {farewell(path: string, method: string): void;}')
        (self.root/self.path).write_text("import type {HttpLocalApi} from '@portico/client-core';\ndeclare const api: HttpLocalApi;\napi.farewell('/v1/read', 'DELETE');\n")
        commit=self.commit('Workspace requester imported by package name')
        with source_snapshot(self.root,commit) as (snapshot,paths):
            sites=census.scan(snapshot,paths)
            self.assertEqual([(s['kind'],s['callee'])for s in sites],[('request','api.farewell')])

    def test_snapshot_uses_the_same_third_party_types_without_current_workspace_sources(self):
        library=self.root/'node_modules/local-types';library.mkdir(parents=True)
        (library/'package.json').write_text(json.dumps({'name':'local-types','types':'index.d.ts'}))
        (library/'index.d.ts').write_text('export declare function identity<T>(value: T): T;')
        (self.root/self.path).write_text("import {identity} from 'local-types';\nconst local=identity({request(value: number) {return value;}});\nlocal.request(3);\n")
        # node_modules is an installed dependency, never tracked source.
        self.git('add',self.path);self.git('commit','-qm','Use the installed declaration')
        actual=engineering.source_counts(self.root)
        self.assertEqual(actual,{})
        with source_snapshot(self.root,'HEAD') as (snapshot,paths):
            self.assertEqual(engineering.source_counts(snapshot,paths),actual)


if __name__=='__main__':unittest.main()
