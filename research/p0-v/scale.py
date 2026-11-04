#!/usr/bin/env python3
"""Reference feasibility only. No target-store adapter exists in phase 0 yet."""
import argparse,hashlib,json,resource,time
from model import Reference
p=argparse.ArgumentParser();p.add_argument('--size',type=int,required=True);a=p.parse_args();n=a.size
assert n>0
start=time.perf_counter()
# Leaf-only stress distribution; NOT S's realistic movie/TV/music/book fixture.
f=dict(entities=[],assets=[],attributes=[],ages=[dict(value_key='pg',minimum_age=8),dict(value_key='r',minimum_age=17)],genres=[])
for i in range(n):
    f['entities'].append(dict(id=i+1,public=f'id-{i:08}',kind=1,library='fixture-movies',title=f'Title Word{i%1000:04} {i:08}',sort_title='',parent_id=None,year=1950+i%77,added_ms=i%811,retired=0,library_retired=0))
    f['assets'].append(dict(entity_id=i+1,duration=float(i%7000),size=i*100,height=1080,asset_available=1,link_available=1))
    f['attributes'].append(dict(item_id=i+1,field='contentRating',value_key='r' if i%5==0 else 'pg',source_value='r' if i%5==0 else 'pg'))
    f['genres'].append(dict(entity_id=i+1,key=f'g{i%12}',label=f'Genre {i%12:02}'))
m=Reference(f);digests={}
for viewer in ['open','restricted']:
    expected=n if viewer=='open' else n-(n+4)//5
    for field in ['title','year','added','duration']:
        asc=m.browse('fixture-movies',1,viewer,field,'asc');desc=m.browse('fixture-movies',1,viewer,field,'desc')
        assert len(asc)==expected and len(desc)==expected
        assert [e['id'] for e in asc]==[e['id'] for e in reversed(desc)]
        h=hashlib.sha256()
        for e in asc:h.update((e['public']+'\n').encode())
        digests[viewer+'/'+field]=h.hexdigest()
    counts=m.facets('fixture-movies',viewer,'genre');assert sum(counts.values())==expected
    got=m.search_own_title('Title Word000',viewer)
    expected_search={f'id-{i:08}' for i in range(n) if i%1000<10 and (viewer=='open' or i%5!=0)}
    assert got==expected_search
print(json.dumps(dict(size=n,seconds=round(time.perf_counter()-start,3),peakRSSKiB=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,semanticDigests=digests,scope='model feasibility and internal consistency only; NOT target-store parity'),indent=2))
