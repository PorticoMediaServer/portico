#!/usr/bin/env python3
"""Small independent source-generator oracle, not a production recipe oracle.
Reconstructs initial ASCII fixture and all prefix populations without SQLite/FTS.
"""
import argparse, json, math
from pathlib import Path

WORDS = 'Harbor Night Silver River Moon Adventure Blue Garden Silent Echo Northern Empire Crimson Valley Winter Dream Star Ocean Hidden Road Golden Light Shadow Wild Castle Sun Journey Mountain Distant Love Clock Morning'.split()
FIXED = ['Harbor Lights','Northern Road','Blue River','1984','東京夜話','1234567','Nebuchadnezzar Chronicle','Spider-Man','Amélie']
def title(i):
    if i < 9: return FIXED[i]
    if i % 47 == 0: return f'東京夜話 {i}'
    if i % 37 == 0: return f'1984 Chronicle {i}'
    return f'{WORDS[i%32]} {WORDS[i//32%32]} {WORDS[i//1024%32]} {i}'
def parent(i):
    r,b=i%100,i-i%100
    if 16<=r<=18:return b+15
    if 19<=r<60:return b+16+(r-19)%3
    if r in (61,62):return b+60
    if 63<=r<90:return b+61+(r-63)%2
    return None

def run(n,variant):
    shared=max(128,n//50)
    names={i:f'{["Ada","John","Jo","Al","Alice","Nolan","Akira","Lee"][i%8]} {["Lovelace","Smith","Williams","Li","Cooper","Johnson"][i%6]} {i}'.lower() for i in range(1,shared+1)}
    names.update({shared+i:f'{["Night","Criterion","Al","Studio"][i%4]} Collection {i}'.lower() for i in range(1,max(1,shared//10)+1)})
    titles=[title(i).lower().replace('é','e') for i in range(n)]
    def words(s):
        # ASCII cases in this oracle only; hyphen supplies separate and joined words.
        return set(s.split()+s.replace('-',' ').split()+[s.replace('-','')])
    edges={i:{1+(i*17+j*43)%shared for j in range(2)} | {shared+1+(i//100)%max(1,shared//10)} for i in range(n)}
    edges[0].add(8);edges[1].add(2);edges[2].add(shared+4)
    def term(t):
        results={}
        for i in range(n):
            own=any(w.startswith(t) for w in words(titles[i]))
            tier=2 if titles[i].split()[0].startswith(t) else 1 if own else -1
            x=i
            for depth in range(3):
                if depth and any(w.startswith(t) for w in words(titles[x])): tier=max(tier,0)
                if variant=='A' or len(t)>=3:
                    if any(any(w.startswith(t) for w in words(names[e])) for e in edges[x]):tier=max(tier,0)
                x=parent(x)
                if x is None:break
            if tier>=0:results[i]=tier
        return results
    output={}
    for q in ['al','jo','harbor ad','ada','night','li']:
        terms=q.split();sets=[term(t) for t in terms];lead=min(range(len(terms)),key=lambda j:len(sets[j]));hits=set(sets[0]).intersection(*[set(s) for s in sets[1:]])
        page=sorted(hits,key=lambda i:(-(3 if titles[i]==q else sets[lead][i]),i))[:41]
        output[q]={'population':len(sets[lead]),'matched_count':len(hits),'page':page,'witness_0':0 in hits,'witness_1':1 in hits,'witness_2':2 in hits}
    return output

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--n',type=int,default=1000);p.add_argument('--log',type=Path);p.add_argument('--variant',choices=['A','B'],required=True);args=p.parse_args()
    expected=run(args.n,args.variant)
    if args.log:
        observed={e['data']['query']:e['data'] for line in args.log.read_text().splitlines() if (e:=json.loads(line))['event']=='quality'}
        for q,result in expected.items():
            for field in ['population','page']:
                assert observed[q][field]==result[field],(q,field,observed[q][field],result[field])
    print(json.dumps({'variant':args.variant,'n':args.n,'verified_against_log':bool(args.log),'results':expected},indent=2))
