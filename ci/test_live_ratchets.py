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
from ratchet_base import event_base, merge_base, source_snapshot


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

    def checks(self,base):
        with redirect_stdout(io.StringIO()),redirect_stderr(io.StringIO()):
            return (census.main(['--base',base],root=self.root),engineering.main(['--base',base],root=self.root))

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
            selected=event_base(event,{'GITHUB_EVENT_NAME':'merge_group'})
        self.assertEqual(selected,target)
        self.assertEqual(self.checks(first),(0,0),'using only the previous queued group would miss growth')
        self.assertEqual(self.checks(selected),(1,1))
        # Once that first group has merged, compare the remaining group with it.
        self.git('update-ref','refs/remotes/origin/main',first)
        with patch('plan.command',side_effect=lambda args,cwd=None:subprocess.check_output(args,cwd=self.root,text=True)):
            self.assertEqual(event_base(event,{'GITHUB_EVENT_NAME':'merge_group'}),first)

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
