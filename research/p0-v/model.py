#!/usr/bin/env python3
"""Independent slow semantic model. No application imports, SQL or projections.

Input is source facts, not candidate results. Unsupported semantics fail closed.
The prototype validates three leaf pivots; see V-ORACLE for outstanding adapters.
"""
import argparse,collections,copy,json,re,unicodedata,urllib.parse

def fold(s):
    return ''.join(c for c in unicodedata.normalize('NFKD',s.lower()) if not unicodedata.combining(c))

def title_key(e):
    text=fold(e.get('sort_title') or e['title'])
    # Corpus is English. Other language article policies require named adapters.
    if not e.get('sort_title'):
        text=re.sub(r'^(the|an|a)\s+','',text)
    return text

class Reference:
    def __init__(self,facts):
        self.facts=facts
        self.entities={x['id']:x for x in facts['entities']}
        assert len(self.entities)==len(facts['entities']), 'duplicate source identity'
        self.attrs=collections.defaultdict(lambda:collections.defaultdict(set))
        self.labels=collections.defaultdict(lambda:collections.defaultdict(dict))
        for a in facts['attributes']:
            self.attrs[a['item_id']][a['field']].add(a['value_key'])
            self.labels[a['item_id']][a['field']][a['value_key']]=a['source_value']
        self.assets=collections.defaultdict(list)
        for a in facts['assets']: self.assets[a['entity_id']].append(a)
        self.genres=collections.defaultdict(dict)
        for g in facts['genres']: self.genres[g['entity_id']][g['key']]=g['label']
        self.ages={x['value_key']:x['minimum_age'] for x in facts['ages']}

    def visible(self,e,viewer):
        if e['retired'] or e['library_retired']: return False
        if viewer in ('owner','open'): return True
        if viewer!='restricted': raise ValueError('unknown viewer')
        if e['library'] not in ('fixture-movies','fixture-tv'): return False
        attrs=self.attrs[e['id']]
        ages=[self.ages[k] for k in attrs['contentRating'] if k in self.ages]
        return any(a>=0 for a in ages) and not any(a>13 for a in ages) and not (attrs['label'] & {'violence','nudity'})

    def browse(self,library,kind,viewer,field='title',direction='asc'):
        if kind not in (1,4,7): raise ValueError('aggregate pivots require separate reviewed reducer')
        rows=[e for e in self.entities.values() if e['library']==library and e['kind']==kind and self.visible(e,viewer)]
        def key(e):
            if field=='title': v=title_key(e)
            elif field=='year': v=e['year']
            elif field=='added': v=e['added_ms'] or 0
            elif field=='duration': v=max([a['duration'] for a in self.assets[e['id']]] or [0])
            else: raise ValueError('unimplemented sort '+field)
            return v,e['id']
        return sorted(rows,key=key,reverse=direction=='desc')

    def facets(self,library,viewer,field):
        counts=collections.Counter()
        for e in self.browse(library,1,viewer):
            values=self.genres[e['id']] if field=='genre' else self.labels[e['id']][field]
            # Count each entity once per value, irrespective of source fanout.
            for v in set(values.values()): counts[v]+=1
        return dict(counts)

    def search_own_title(self,query,viewer,kind=1):
        terms=re.findall(r'[^\W_]+',fold(query))
        return {e['public'] for e in self.entities.values() if e['kind']==kind and self.visible(e,viewer)
                and all(any(w.startswith(t) for w in re.findall(r'[^\W_]+',fold(e['title']))) for t in terms)}

def compare(facts):
    m=Reference(facts);before=json.dumps(facts,sort_keys=True);chains=collections.defaultdict(list);checks=0;searches=collections.defaultdict(list)
    for c in facts['cases']:
        if c['name']=='browse':
            b=json.loads(c['body']);lib=c['path'].split('/')[3];kind={'movies':1,'episodes':4,'songs':7}[b['pivot']]
            if c['viewer']=='restricted' and lib=='fixture-music':
                assert c['status']==404, c
                checks+=1;continue
            assert c['status']==200, c
            s=b['sort'][0];key=(lib,kind,c['viewer'],s['field'],s['direction'])
            want=m.browse(*key);doc=c['response'];assert doc['pageInfo']['total']==len(want),(key,'total',doc['pageInfo']['total'],len(want))
            chains[key].extend(e['id'] for e in doc['entries']);checks+=1
        elif c['name']=='facet':
            assert c['status']==200,c
            field=urllib.parse.parse_qs(urllib.parse.urlsplit(c['path']).query)['field'][0]
            doc=c['response'];values=doc.get('values',doc.get('entries',[]))
            got={v.get('value',v.get('key')):v['count'] for v in values}
            want=m.facets('fixture-movies',c['viewer'],field)
            assert got==want,('facets',field,c['viewer'],got,want,doc)
            expected=sorted(want,key=lambda v:(-want[v],v.lower()))
            assert [v['value'] for v in values]==expected,('facet order',field)
            assert doc['total']==len(want) and not doc['totalIsLowerBound']
            assert all(not v['countIsLowerBound'] for v in values)
            checks+=1
        elif c['name']=='search':
            assert c['status']==200,c
            q=urllib.parse.parse_qs(urllib.parse.urlsplit(c['path']).query)['q'][0]
            # Grouped requests continue to exhaustion; no first-page truncation.
            g=c['response']['groups'][0]
            assert g['id']=='movies' and g['status']=='success',g
            want=m.search_own_title(q,c['viewer'])
            assert g['totalCountExact'] and g['totalCount']==len(want),('search total',q,g)
            assert bool(g['nextCursor'])==g['hasMore']
            got=[e['id'] for e in g['items']]
            searches[(q,c['viewer'])].extend(got)
            checks+=1
    for (q,viewer),got in searches.items():
        want=m.search_own_title(q,viewer)
        assert set(got)==want and len(got)==len(want),('search',q,viewer,len(got),len(want),set(got)^want)
        checks+=1
    for key,got in chains.items():
        want=[e['public'] for e in m.browse(*key)]
        assert got==want,('chain',key,'first mismatch',next(((i,a,b) for i,(a,b) in enumerate(zip(got,want)) if a!=b),None),'lengths',len(got),len(want))
        assert len(got)==len(set(got)),('duplicate',key)
        checks+=1
    assert json.dumps(facts,sort_keys=True)==before,'model mutated source facts'
    return {'source_entities':len(m.entities),'http_pages':len(facts['cases']),'complete_chains':len(chains),'assertion_groups':checks}

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('facts');a=p.parse_args();print(json.dumps(compare(json.load(open(a.facts))),indent=2))
