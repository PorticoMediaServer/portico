#!/usr/bin/env python3
import argparse,copy,json,pathlib,time
from model import Reference,compare,instant_ns

p=argparse.ArgumentParser();p.add_argument('facts');p.add_argument('--plans');p.add_argument('--record-plan',action='store_true');a=p.parse_args()
facts=json.load(open(a.facts));start=time.perf_counter();result=compare(facts)
controls={}
# Corrupt observed output, never expected facts. Every control must fail.
b=next(i for i,c in enumerate(facts['cases']) if c['name']=='browse' and len(c['response'].get('entries',[]))>1)
f=next(i for i,c in enumerate(facts['cases']) if c['name']=='facet')
s=next(i for i,c in enumerate(facts['cases']) if c['name']=='search' and c['response']['groups'][0]['items'])
for name,mutation in [
 ('missing',lambda x:x['cases'][b]['response']['entries'].pop()),
 ('extra',lambda x:x['cases'][b]['response']['entries'].append(copy.deepcopy(x['cases'][b]['response']['entries'][0]))),
 ('wrong-id',lambda x:x['cases'][b]['response']['entries'][0].update(id='unknown')),
 ('wrong-order',lambda x:x['cases'][b]['response']['entries'].reverse()),
 ('wrong-total',lambda x:x['cases'][b]['response']['pageInfo'].update(total=999)),
 ('total-type',lambda x:x['cases'][b]['response']['pageInfo'].update(total='40')),
 ('facet-count',lambda x:x['cases'][f]['response']['values'][0].update(count=999)),
 ('facet-order',lambda x:x['cases'][f]['response']['values'].reverse()),
 ('search-membership',lambda x:x['cases'][s]['response']['groups'][0]['items'][0].update(id='forbidden')),
 ('search-truncation',lambda x:x['cases'][s]['response']['groups'][0]['items'].pop()),
 ('search-total',lambda x:x['cases'][s]['response']['groups'][0].update(totalCount=999)),
 ('source-duplicate',lambda x:x['entities'].append(copy.deepcopy(x['entities'][0]))),
]:
    x=copy.deepcopy(facts);mutation(x)
    try:compare(x)
    except AssertionError:controls[name]='rejected'
    else:raise AssertionError('negative control accepted: '+name)
# Explicit policy edge cases independent of what the seeded fixture happens to contain.
e=copy.deepcopy(facts['entities'][0]);e.update(id=999999,public='policy',library='fixture-movies',retired=0,library_retired=0)
def permitted(attrs,ages,viewer='restricted',**updates):
    item=dict(e,**updates)
    m=Reference(dict(entities=[item],assets=[],genres=[],attributes=[dict(item_id=item['id'],field=f,value_key=k,source_value=k) for f,k in attrs],ages=[dict(value_key=k,minimum_age=v) for k,v in ages.items()]))
    return m.visible(item,viewer)
assert not permitted([],{});assert permitted([('contentRating','pg')],{'pg':8})
assert not permitted([('contentRating','pg'),('contentRating','r')],{'pg':8,'r':17})
assert not permitted([('contentRating','unknown')],{})
assert not permitted([('contentRating','pg'),('label','violence')],{'pg':8})
assert not permitted([('contentRating','pg')],{'pg':8},library='hidden')
assert not permitted([],{},viewer='owner',retired=1)
chron=json.loads((pathlib.Path(__file__).parent/'chronology.json').read_text())
actual=[x['name'] for x in sorted(chron['records'],key=lambda x:(instant_ns(x['added_at']),x['id']),reverse=True)]
legacy=[x['name'] for x in sorted(chron['records'],key=lambda x:(x['added_at'],x['id']),reverse=True)]
assert actual==chron['instantDescending'] and legacy==chron['legacyTextDescending'] and actual!=legacy
assert instant_ns('2026-11-04T12:00:00Z')==instant_ns('2026-11-04T12:00:00.000000000Z')
assert instant_ns(chron['records'][0]['added_at'])!=instant_ns(chron['records'][2]['added_at'])
pair=chron['fractionalPair']
assert [r['id'] for r in sorted(pair['records'],key=lambda r:(instant_ns(r['added_at']),r['id']),reverse=True)]==pair['instantDescending']==[2,1]
assert [r['id'] for r in sorted(pair['records'],key=lambda r:(r['added_at'],r['id']),reverse=True)]==pair['legacyDescending']==[1,2]
result['chronology']=dict(instantDescending=actual,legacyTextDescending=legacy,semantic_difference=True)
result.update(negative_controls=controls,policy_edge_cases=7,elapsed_seconds=round(time.perf_counter()-start,3))
if a.plans:
    plans={k:facts[k] for k in ['planRegistry','engine','compileOptions','schemaSHA256','plannerPragmas','statistics']}
    # Include schema and engine bytes. Deliberate SQL edit and missing opcode cannot pass.
    path=pathlib.Path(a.plans)
    if path.exists():assert json.loads(path.read_text())==plans,'EXPLAIN golden changed: review exact SQL/schema/engine/stats diff'
    else:
        assert a.record_plan,'missing plan golden; explicit --record-plan required'
        path.write_text(json.dumps(plans,indent=2)+'\n')
    for kind in ['sql','explain']:
        x=copy.deepcopy(plans);v=next(iter(x['planRegistry'].values()))
        if kind=='sql':v['sql']+=' LIMIT 1'
        else:v['explain'].pop()
        assert x!=plans,'plan control failed'
    result['static_statements']=len(plans['planRegistry'])
print(json.dumps(result,indent=2))
