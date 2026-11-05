#!/usr/bin/env python3
"""All-verb client API ownership ratchet. See docs/client-api-ownership.md.

python3 ci/client_api_census.py [--update] [--base <review base>] [--report <json>]
"""
import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from ratchet_base import merge_base, source_snapshot

ROOT = Path(__file__).resolve().parent.parent
SCANNER = ROOT / 'ci/client_api_sites.mjs'
BASELINE = 'ci/client-api-baseline.json'
ALLOWLIST = 'ci/client-api-exceptions.json'
APPROVALS = 'ci/client-api-approvals.json'
SOURCE_ROOTS = ('web/', 'apps/native/', 'apps/desktop/', 'apps/vega/',
                'packages/player-core/', 'packages/reader-core/', 'website/', 'server/receiver/cast/')
EXTENSIONS = {'.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs', '.kt', '.java', '.swift', '.m', '.mm', '.rs'}


def production(path):
    return (path.startswith(SOURCE_ROOTS) and Path(path).suffix in EXTENSIONS
            and not re.search(r'(?:^|/)(?:tests?|__tests__|scripts|node_modules|build|dist|Pods|dev)/|\.(?:test|spec|browser|capture)\.|\.d\.ts$', path)
            and not (path.startswith('server/receiver/cast/') and re.search(r'(?:^|/)(?:contracts|schemas|decoders|routes)\.(?:js|ts)$', path))
            and not re.search(r'/(?:ds-[^/]+|.*\.specimen)\.tsx$', path))


def tokens(source):
    """Native lexical tokens with offsets. Strings/comments cannot masquerade as calls.

    Handles nested block comments (Kotlin/Swift/Rust), raw Rust strings, Kotlin/Swift
    multiline strings and Objective-C strings. No HTTP matching inside a string.
    """
    out, i = [], 0
    while i < len(source):
        if source[i].isspace(): i += 1; continue
        if source.startswith('//', i):
            end = source.find('\n', i); i = len(source) if end < 0 else end; continue
        if source.startswith('/*', i):
            depth = 1; i += 2
            while i < len(source) and depth:
                if source.startswith('/*', i): depth += 1; i += 2
                elif source.startswith('*/', i): depth -= 1; i += 2
                else: i += 1
            continue
        start = i
        raw = re.match(r'r(#+)?"', source[i:])
        if raw:
            delimiter = '"' + (raw[1] or ''); end = source.find(delimiter, i + raw.end())
            i = len(source) if end < 0 else end + len(delimiter)
            out.append(('string', source[start:i], start)); continue
        if source.startswith('@"', i): i += 1
        if source.startswith('"""', i):
            end = source.find('"""', i + 3); i = len(source) if end < 0 else end + 3
            out.append(('string', source[start:i], start)); continue
        if source[i] in ('"', "'"):
            # Rust lifetimes are identifiers, not unterminated character strings.
            if source[i] == "'" and re.match(r"'[A-Za-z_]\w*(?!')\b", source[i:]) and not re.match(r"'[^'\n]+'",source[i:]):
                i += 1; out.append(('punct', "'", start)); continue
            quote = source[i]; i += 1
            while i < len(source):
                if source[i] == '\\': i += 2; continue
                if source[i] == quote: i += 1; break
                i += 1
            out.append(('string', source[start:i], start)); continue
        word = re.match(r'[A-Za-z_$][\w$]*', source[i:])
        if word:
            i += word.end(); out.append(('id', word[0], start)); continue
        out.append(('punct', source[i], i)); i += 1
    return out


def native_function_spans(ts, source, path):
    """Complete enclosing native method bodies, including builder input bindings."""
    closes, stack = {}, []
    for i, (kind, value, _) in enumerate(ts):
        if kind == 'string': continue
        if value == '{': stack.append(i)
        elif value == '}' and stack: closes[stack.pop()] = i
    rounds, round_stack = {}, []
    for i,(kind,value,_) in enumerate(ts):
        if kind=='string': continue
        if value=='(': round_stack.append(i)
        elif value==')' and round_stack: rounds[round_stack.pop()]=i
    spans = []
    for i, (kind, value, offset) in enumerate(ts):
        objc = value in ('-', '+') and source[source.rfind('\n',0,offset)+1:offset].strip()=='' and i+1<len(ts) and ts[i+1][1]=='('
        macro=value in ('RCT_EXPORT_METHOD','RCT_REMAP_METHOD','RCT_EXPORT_BLOCKING_SYNCHRONOUS_METHOD')
        following=rounds.get(i+1)
        declaration=(path.endswith(('.m','.mm','.java')) and kind=='id' and following is not None
                     and following+1<len(ts) and ts[following+1][1]=='{'
                     and (i==0 or ts[i-1][1] not in ('.','new'))
                     and value not in ('if','for','while','switch','catch','synchronized'))
        if value not in ('fun','func','fn') and not objc and not macro and not declaration: continue
        depth = 0
        for j in range(i+1,len(ts)):
            if ts[j][0]=='string': continue
            token = ts[j][1]
            if token == '(': depth += 1
            elif token == ')': depth -= 1
            elif depth == 0 and token in (';', '='): break
            elif depth == 0 and token == '{':
                if j in closes: spans.append((i,closes[j]))
                break
    return spans


def native_chain(ts, start, end):
    """Keep the complete receiver and fluent policy-bearing request chain."""
    while start >= 2 and ts[start-1][1] == '.' and ts[start-2][0]=='id': start -= 2
    while end+2 < len(ts):
        dot = end+1
        if ts[dot][1]=='?' and ts[dot+1][1]=='.': dot += 1
        if ts[dot][1]!='.' or ts[dot+1][0]!='id': break
        end = dot+1
        if end+1<len(ts) and ts[end+1][1]=='(':
            depth=0
            for j in range(end+1,len(ts)):
                if ts[j][0]=='string': continue
                if ts[j][1]=='(': depth+=1
                elif ts[j][1]==')':
                    depth-=1
                    if depth==0: end=j;break
            else: break
    return start,end


def native_sites(path, source):
    if path.endswith('.rs'):
        # Skip each test module's balanced body, preserving later production source.
        ranges = []
        lexical = tokens(source)
        for match in re.finditer(r'#\[cfg\(test\)\]\s*(?:pub\s+)?mod\s+\w+\s*\{', source):
            depth = 1
            for _, value, offset in lexical:
                if offset < match.end(): continue
                if value == '{': depth += 1
                elif value == '}':
                    depth -= 1
                    if not depth:
                        ranges.append((match.start(), offset+1)); break
        for start, end in reversed(ranges):
            source = source[:start]+re.sub(r'[^\n]', ' ', source[start:end])+source[end:]
    if path.endswith(('.m','.mm')):
        source = re.sub(r'^#if DEBUG\b[\s\S]*?^#endif\b', lambda m: re.sub(r'[^\n]',' ',m[0]), source, flags=re.M)
    ts = tokens(source)
    found = []
    function_spans = native_function_spans(ts, source, path)
    brackets, enclosing = [], {}
    for n, token in enumerate(ts):
        enclosing[n] = list(brackets)
        if token[1] in ('[', '('): brackets.append(token[1])
        elif token[1] in (']', ')') and brackets: brackets.pop()
    native_calls = {'openConnection', 'openStream', 'URLRequest', 'requestWithURL',
                    'dataTaskWithURL', 'dataTaskWithRequest', 'downloadTaskWithRequest',
                    'downloadTaskWithURL', 'dataTask', 'downloadTask', 'uploadTask',
                    'newCall', 'bytes', 'networkRequest', 'profileRequest', 'readingRequest',
                    'Request', 'request', 'send', 'execute', 'exchange', 'wire'}
    for i, (kind, value, offset) in enumerate(ts):
        if kind != 'id' or i + 1 >= len(ts): continue
        # A real call/message selector, never a matching substring in a literal/comment.
        after = ts[i + 1][1]
        if after not in ('(', ':'): continue
        if after == ':' and '[' not in enclosing[i]: continue
        # Function declarations are not calls.
        if i and ts[i - 1][1] in ('fun', 'func', 'fn'): continue
        if value == 'bytes' and (after != '(' or ts[i+2][1] != 'for'): continue
        if value == 'execute' and (i < 2 or ts[i-1][1] != '.' or not re.search(r'(?:client|http|request|call)',ts[i-2][1],re.I)): continue
        before = [t[1] for t in ts[max(0,i-3):i]]
        is_builder = value == 'Builder' and 'Request' in before
        is_url = value in ('stringWithFormat','stringByAppendingString','format')
        if value not in native_calls and not is_builder and not is_url: continue
        if value in ('send', 'execute', 'Request', 'request', 'wire', 'exchange'):
            # Only network receivers, HTTP method arguments or API-path-bearing calls;
            # exclude map lookups, playback commands, DB requests and FFI request structs.
            head = ''.join(t[1] for t in ts[max(0,i-2):i])
            nearby = ts[i+1:i+45]
            if not (re.search(r'(?:Transport|session|client|http|api|wire|xhr)',head,re.I)
                    or any(t[0]=='string' and re.search(r'/v1/|"(?:GET|POST|PUT|PATCH|DELETE|HEAD)"',t[1]) for t in nearby)):
                continue
        end = i+1
        if after == '(':
            depth = 0
            for end in range(i+1,len(ts)):
                if ts[end][1] == '(': depth += 1
                elif ts[end][1] == ')':
                    depth -= 1
                    if not depth: break
        else:
            depth = 0
            for end in range(i+1,len(ts)):
                if ts[end][1] == '[': depth += 1
                elif ts[end][1] == ']':
                    if depth == 0: break
                    depth -= 1
                elif ts[end][1] == ';' and depth == 0: break
        start, end = native_chain(ts, i, end) if after == '(' else (i, end)
        call = ts[start:end+1]
        if is_url and not any(t[0]=='string' and '/v1/' in t[1] for t in call): continue
        if value == 'Request' and is_builder: continue
        expression = ' '.join(t[1] for t in call)
        spans = [(a,b) for a,b in function_spans if a<=i<=b]
        construction_hash = None
        if spans:
            a,b = min(spans,key=lambda span:span[1]-span[0])
            construction_hash = hashlib.sha256(source[ts[a][2]:ts[b][2]+1].encode()).hexdigest()
        found.append({'file':path,'line':source.count('\n',0,offset)+1,
                      'kind':'url' if is_url else 'request','owner':'<native>',
                      'callee':value,'operations':[], 'expression':expression, 'constructionHash': construction_hash})
    return found


def scan(root=ROOT, paths=None):
    if paths is None: paths = subprocess.check_output(['git','ls-files','-z'],cwd=root).decode().split('\0')
    paths = sorted(p for p in paths if p and production(p))
    js = [p for p in paths if Path(p).suffix in {'.ts','.tsx','.js','.jsx','.mjs','.cjs'}]
    if js:
        result = subprocess.run(['node',str(SCANNER)],cwd=root,
                                input=json.dumps({'root':str(root),'files':js}),text=True,capture_output=True,check=True)
        sites = json.loads(result.stdout)
    else: sites = []
    for path in paths:
        if path not in js: sites.extend(native_sites(path,(root/path).read_text()))
    for site in sites:
        # Stable across line moves. Multisets preserve identical calls on the same line.
        identity = [site['kind'],site['owner'],site['callee'],site['expression']]
        if 'constructionHash' in site: identity.append(site['constructionHash'])
        if site.get('forwardedRequest') and site.get('guardHash'): identity.append(site['guardHash'])
        identity = json.dumps(identity, separators=(',',':'))
        site['id'] = site['file']+'#'+hashlib.sha256(identity.encode()).hexdigest()[:16]
    return sorted(sites,key=lambda s:(s['file'],s['line'],s['id']))


def counts(sites):
    return dict(sorted(Counter(s['id'] for s in sites).items()))


def compare(actual, baseline):
    new = [f'{key}: {n} direct API site(s), baseline {baseline.get(key,0)}' for key,n in actual.items() if n>baseline.get(key,0)]
    stale = [key for key,n in baseline.items() if actual.get(key,0)<n]
    return new, stale


def exception_pins(entry):
    if set(entry['sites']) & set(entry.get('additionalSites', {})):
        raise ValueError('Exception ID overlap between sites and additionalSites is forbidden')
    return {**{key: {'category': entry['category'], 'reason': entry['reason'], **({'guardHash': entry.get('guardHashes', {}).get(key)} if entry['category']=='platform-transport' else {})} for key in entry['sites']},
            **entry.get('additionalSites', {})}


def classify(sites, exceptions):
    approved = {'platform-url','native-extension','native-listening','separate-product','platform-transport'}
    exempt, direct = [], []
    remaining = {path: Counter(entry['sites']) + Counter(entry.get('additionalSites', {}).keys()) for path,entry in exceptions.items()}
    for path,entry in exceptions.items():
        if any(pin['category'] not in approved or not pin['reason'].strip() for pin in exception_pins(entry).values()) or any(c in path for c in '*?['):
            raise ValueError(f'Invalid exception (exact file, approved category and reason required): {path}')
        if not entry.get('sites'): raise ValueError(f'Exception must pin its exact construction/request sites: {path}')
    for site in sites:
        entry = exceptions.get(site['file'])
        pin = exception_pins(entry).get(site['id']) if entry else None
        allowed = pin is not None and remaining[site['file']][site['id']] > 0
        if allowed: remaining[site['file']][site['id']] -= 1
        if allowed and pin['category']=='platform-transport' and (not pin.get('guardHash') or pin['guardHash'] != site.get('guardHash')):
            raise ValueError(f"{site['file']}:{site['line']}: reviewed guard source changed or is unbound; re-approval and a new guardHash are required")
        if allowed and pin['category']=='platform-transport' and not site.get('forwardedRequest'):
            raise ValueError(f"{site['file']}:{site['line']}: platform transport must match an approved unchanged request/raw/chapter forwarding shape")
        (exempt if allowed else direct).append(site)
    return direct, exempt


def approval_for(approvals, key, pin, count):
    entry = approvals.get(key, {})
    return (entry.get('reason') == pin['reason'] and entry.get('condition', '').strip()
            and entry.get('category') == pin['category'] and entry.get('guardHash') == pin.get('guardHash')
            and type(entry.get('occurrences')) is int and entry['occurrences'] >= count
            and isinstance(entry.get('rulingUrl'), str)
            and re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/(?:pull|issues)/[1-9]\d*#issuecomment-[1-9]\d*', entry['rulingUrl']))


def validate_approvals(approvals):
    for key, entry in approvals.items():
        if (not re.fullmatch(r'.+#[a-f0-9]{16}', key) or not entry.get('reason', '').strip()
                or not entry.get('condition', '').strip() or type(entry.get('occurrences')) is not int or entry['occurrences'] < 1
                or entry.get('category') not in {'platform-transport','platform-url','native-extension','native-listening','separate-product'}
                or not isinstance(entry.get('rulingUrl'),str)
                or not re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/(?:pull|issues)/[1-9]\d*#issuecomment-[1-9]\d*',entry['rulingUrl'])
                or entry['category']=='platform-transport' and not re.fullmatch(r'[a-f0-9]{64}',entry.get('guardHash',''))):
            raise ValueError(f'{key}: approval requires an exact site, condition, positive multiplicity, category/source binding and lead-ruling URL')


def base_guard(baseline, exceptions, old_baseline, old_exceptions, approvals=None):
    errors, _ = compare(baseline, old_baseline)
    # New/changed allowances require a reviewed ruling for this exact site,
    # source hash, reason/category and complete multiplicity. Ledger edits are
    # themselves reviewable; an old approval cannot authorize a larger count.
    approvals = approvals or {}
    for path,entry in exceptions.items():
        pins=exception_pins(entry);old=old_exceptions.get(path)
        previous=exception_pins(old) if old else {}
        total=Counter(entry['sites'])+Counter(entry.get('additionalSites', {}).keys())
        was=Counter(old['sites'])+Counter(old.get('additionalSites', {}).keys()) if old else Counter()
        for key,pin in pins.items():
            if (previous.get(key)!=pin or total[key]>was[key]) and not approval_for(approvals,key,pin,total[key]):
                errors.append(f'{key}: exception expanded or changed without an exact lead-ruling approval in {APPROVALS}')
    return errors


def main(argv=None, root=ROOT):
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--update',action='store_true');p.add_argument('--initialize',action='store_true',help='one-time creation; refuses an existing baseline')
    p.add_argument('--base');p.add_argument('--report',type=Path);a=p.parse_args(argv)
    exceptions=json.loads((root/ALLOWLIST).read_text())
    approvals=json.loads((root/APPROVALS).read_text()) if (root/APPROVALS).exists() else {}
    sites=scan(root)
    try:
        validate_approvals(approvals)
        direct,exempt=classify(sites,exceptions)
    except ValueError as error:
        print(f"Client-core ownership ratchet failed: {error}", file=sys.stderr); return 1
    actual=counts(direct)
    baseline_path=root/BASELINE
    if a.initialize:
        if baseline_path.exists():p.error('baseline already exists; --update only removes sites')
        baseline_path.write_text(json.dumps(actual,indent=2)+'\n')
    baseline=json.loads(baseline_path.read_text())
    new,stale=compare(actual,baseline)
    if a.base:
        a.base=merge_base(root,a.base)
        def old(path):
            r=subprocess.run(['git','show',a.base+':'+path],cwd=root,text=True,capture_output=True)
            if r.returncode:
                # Only the foundation PR can introduce the baseline; don't hide invalid refs.
                subprocess.run(['git','rev-parse','--verify',a.base+'^{commit}'],cwd=root,check=True,stdout=subprocess.DEVNULL)
                return None
            return json.loads(r.stdout)
        old_baseline,old_exceptions=old(BASELINE),old(ALLOWLIST)
        if old_baseline is not None:
            try:
                with source_snapshot(root,a.base) as (snapshot,paths):
                    previous,_=classify(scan(snapshot,paths),old_exceptions or {})
                new+=compare(actual,counts(previous))[0]
                new+=base_guard(baseline,exceptions,old_baseline,old_exceptions or {},approvals)
            except ValueError as error: new.append(str(error))
    pinned = sum((Counter(e['sites']) + Counter(e.get('additionalSites', {}).keys()) for e in exceptions.values()), Counter())
    stale_exceptions = list((pinned - Counter(counts(exempt))).elements())
    if stale_exceptions:new += [f'{site}: stale exception; remove its allowance' for site in stale_exceptions]
    if a.report:a.report.write_text(json.dumps({'direct':direct,'exceptions':exempt},indent=2)+'\n')
    if new:
        print('Client-core ownership ratchet failed: new/increased direct API sites outside packages/client-core.',file=sys.stderr)
        print('\n'.join(new),file=sys.stderr)
        print('Move URL/request/body policy into named client-core domain operations; do not raise the baseline or use a generic screen request wrapper.',file=sys.stderr)
        return 1
    if a.update and stale:baseline_path.write_text(json.dumps(actual,indent=2)+'\n')
    print(f'Client-core ownership holds: {len(direct)} direct sites; {len(exempt)} pinned approved exception sites.')
    return 0


if __name__=='__main__':sys.exit(main())
