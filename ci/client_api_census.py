#!/usr/bin/env python3
"""All-verb client API ownership ratchet. See docs/client-api-ownership.md.

python3 ci/client_api_census.py [--update] [--base <review base>] [--report <json>]
"""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
from ratchet_base import merge_base, source_snapshot, historical_json, check_queue

ROOT = Path(__file__).resolve().parent.parent
SCANNER = ROOT / 'ci/client_api_sites.mjs'
BASELINE = 'ci/client-api-baseline.json'
ALLOWLIST = 'ci/client-api-exceptions.json'
APPROVALS = 'ci/client-api-approvals.json'
DETECTIONS = 'ci/client-api-detection-additions.json'
REHASH_RULING = 'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646375725'
SOURCE_ROOTS = ('web/', 'apps/native/', 'apps/desktop/', 'apps/vega/',
                'packages/player-core/', 'packages/reader-core/', 'website/', 'server/receiver/cast/')
EXTENSIONS = {'.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs', '.kt', '.java', '.swift', '.m', '.mm', '.rs'}


def production(path):
    return (path.startswith(SOURCE_ROOTS) and Path(path).suffix in EXTENSIONS
            and not re.search(r'(?:^|/)(?:tests?|__tests__|scripts|node_modules|build|dist|Pods|dev)/|\.(?:test|spec|browser|capture)\.|\.d\.ts$', path)
            and not (path.startswith('server/receiver/cast/') and re.search(r'(?:^|/)(?:contracts|schemas|decoders|routes)\.(?:js|ts)$', path))
            and not re.search(r'/(?:ds-[^/]+|.*\.specimen)\.tsx$', path))


def tokens(source, rust=False, swift=False):
    """Native lexical tokens with offsets. Strings/comments cannot masquerade as calls.

    Swift literal payload stays opaque, including extended/multiline delimiters.
    Executable interpolation is recursively tokenized, with its original offsets.
    The full literal token preserves enclosing request identities; interpolation
    children remain visible when detecting their own executable calls.
    """
    out=[]
    def code(i, interpolation=False):
        rounds=0
        while i < len(source):
            if source[i].isspace(): i += 1; continue
            if source.startswith('//', i):
                end=re.search(r'[\r\n]',source[i:]);i=len(source) if end is None else i+end.start();continue
            if source.startswith('/*', i):
                depth = 1; i += 2
                while i < len(source) and depth:
                    if source.startswith('/*', i): depth += 1; i += 2
                    elif source.startswith('*/', i): depth -= 1; i += 2
                    else: i += 1
                continue
            start = i
            literal=re.match(r'(#+)?("""|")',source[i:]) if swift else None
            if literal:
                hashes=literal[1] or '';delimiter=literal[2]+hashes;escape='\\'+hashes
                slot=len(out);out.append(None);i+=literal.end()
                while i<len(source):
                    if source.startswith(escape,i) and not (hashes and source.startswith('#',i+len(escape))):
                        opening=i+len(escape)
                        if source.startswith('(',opening):
                            out.append(('punct','(',opening));i=code(opening+1,interpolation=True)
                        else:i=min(len(source),opening+1)
                    elif source.startswith(delimiter,i):i+=len(delimiter);break
                    else:i+=1
                out[slot]=('string',source[start:i],start);continue
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
                # Rust lifetimes are identifiers, not character strings.
                lifetime=re.match(r"'[A-Za-z_]\w*",source[i:]) if rust else None
                if lifetime and not source.startswith("'",i+lifetime.end()):
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
            out.append(('punct', source[i], i))
            if interpolation:
                if source[i]==')' and rounds==0:return i+1
                if source[i]=='(':rounds+=1
                elif source[i]==')':rounds-=1
            i += 1
        return i
    code(0)
    return out


def token_expression(ts):
    values=[];literal_end=-1
    for kind,value,offset in ts:
        if offset<literal_end:continue
        values.append(value)
        if kind=='string':literal_end=offset+len(value)
    return ' '.join(values)


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


def native_forwarding_shape(path, body):
    """The two reviewed certificate executors consume URL/method/body parameters.

    These are admission shapes, not an immutability proof. The full native method
    hash is mandatory in the pin and ledger; any policy edit requires re-review.
    """
    if path.endswith('.kt') and all(re.search(p,body) for p in [
            r'fun\s+proofRequest\([^)]*requestUrl:\s*String\?[^)]*method:\s*String\?[^)]*body:\s*String\?',
            r'requestUrl\?\.toHttpUrlOrNull\(\)', r'\.url\(target\)',
            r'\.method\(method!!,\s*body\.toByteArray\(Charsets\.UTF_8\)\.toRequestBody\("application/json"\.toMediaType\(\)\)\)']):
        return 'native-proof'
    if path.endswith(('.m','.mm')) and all(re.search(p,body) for p in [
            r'prove:\(NSString\s*\*\)origin\s+fingerprint:',
            r'url:\(NSString\s*\*\)requestURL\s+method:\(NSString\s*\*\)method\s+body:\(NSString\s*\*\)body',
            r'NSURL\s*\*url\s*=\s*\[NSURL\s+URLWithString:requestURL\]',
            r'requestWithURL:url\]', r'request\.HTTPMethod\s*=\s*method\s*;',
            r'request\.HTTPBody\s*=\s*\[body\s+dataUsingEncoding:NSUTF8StringEncoding\]']):
        return 'native-proof'
    return None


def native_sites(path, source):
    if path.endswith('.rs'):
        # Skip each test module's balanced body, preserving later production source.
        ranges = []
        lexical = tokens(source,rust=True)
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
    ts = tokens(source,rust=path.endswith('.rs'),swift=path.endswith('.swift'))
    # Transport evidence comes from native imports, declared types and constructor
    # bindings. A file's readText/readBytes/data/get methods alone prove no HTTP.
    code = ' '.join(value if kind != 'string' else '""' for kind,value,_ in ts)
    java_url = bool(re.search(r'\b(?:import\s+java\s*\.\s*net\s*\.\s*URL|java\s*\.\s*net\s*\.\s*URL\b)', code))
    url_names = set()
    if java_url:
        url_names.update(re.findall(r'\b(\w+)\s*:\s*(?:java\s*\.\s*net\s*\.\s*)?URL\b',code))
        url_names.update(re.findall(r'\bURL\s+(\w+)\s*(?:=|,|\))',code))
        url_names.update(re.findall(r'\b(?:val|var|let)\s+(\w+)\s*(?::[^=;{}]+)?=\s*(?:new\s+)?(?:java\s*\.\s*net\s*\.\s*)?URL\s*\(',code))
    session_type=r'(?:(?:Foundation|FoundationNetworking)\s*\.\s*)?URLSession'
    session_names = set(re.findall(r'\b(\w+)\s*:\s*'+session_type+r'\b',code))
    session_names.update(re.findall(r'\b(?:let|var)\s+(\w+)\s*(?::[^=;{}]+)?=\s*(?:\(\s*)*'+session_type+r'\b',code))
    http_type = r'(?:reqwest\s*:\s*:\s*(?:blocking\s*:\s*:\s*)?Client|OkHttpClient|HttpClient)'
    rust_borrow = r'(?:&\s*(?:\x27\s*\w+\s+)?(?:mut\s+)?)?'
    http_names = set(re.findall(r'\b(\w+)\s*:\s*'+rust_borrow+http_type+r'\b',code))
    http_names.update(re.findall(r'\b(?:let|val|var)\s+(\w+)\s*(?::[^=;{}]+)?=\s*(?:new\s+)?'+http_type+r'\b',code))
    if re.search(r'\buse\s+reqwest\s*:\s*:\s*(?:blocking\s*:\s*:\s*)?Client\b',code):
        http_names.update(re.findall(r'\b(?:let|val|var)\s+(\w+)\s*(?::[^=;{}]+)?=\s*Client\s*:\s*:',code))
        http_names.update(re.findall(r'\b(\w+)\s*:\s*'+rust_borrow+r'Client\b',code))
    def receiver_at(i):
        return ts[i-2][1] if i>=2 and ts[i-1][1]=='.' else None
    def url_receiver(i):
        if receiver_at(i) in url_names: return True
        if not java_url or i<2 or ts[i-1][1]!='.' or ts[i-2][1]!=')': return False
        depth=1
        for j in range(i-3,-1,-1):
            if ts[j][1]==')': depth+=1
            elif ts[j][1]=='(':
                depth-=1
                if not depth:return j>0 and ts[j-1][1]=='URL'
        return False
    round_open={};stack=[]
    for n,(_,value,_) in enumerate(ts):
        if value=='(':stack.append(n)
        elif value==')' and stack:round_open[n]=stack.pop()
    def session_origin(end):
        """Recognize a complete receiver rooted in a known URLSession binding/type.

        Walk property/call chains and grouping parentheses, never method spelling
        or URLSession-looking text inside constructor arguments or strings.
        """
        while end>=0 and ts[end][1] in {'?','!'}:end-=1
        if end<0:return None
        if ts[end][1]==')':
            opening=round_open.get(end)
            if opening is None:return None
            before=opening-1
            grouping=(before<0 or ts[before][0]!='id' and ts[before][1] not in {')',']','?','!'}
                      or ts[before][1] in {'await','try','return','throw'})
            if grouping:return opening if session_origin(end-1) is not None else None
            return session_origin(before)
        if ts[end][0]!='id':return None
        if end>=2 and ts[end-1][1]=='.':
            origin=session_origin(end-2)
            if origin is not None:return origin
        if ts[end][1]=='URLSession' or ts[end][1] in session_names:
            start=end
            while start>=2 and ts[start-1][1]=='.' and ts[start-2][0]=='id':start-=2
            return start
        return None
    found = []
    function_spans = native_function_spans(ts, source, path)
    brackets, enclosing, opening_offsets = [], {}, {}
    for n, token in enumerate(ts):
        enclosing[n] = [kind for kind,_ in brackets]
        opening_offsets[n] = [index for kind,index in brackets if kind=='[']
        if token[1] in ('[', '('): brackets.append((token[1],n))
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
        url_read = value in {'readText','readBytes','readLines','inputStream'} and url_receiver(i)
        session_start=session_origin(i-2) if value in {'data','bytes','download','upload'} and i>=2 and ts[i-1][1]=='.' else None
        session_read = session_start is not None and after=='(' and ts[i+2][1] in {'for','from','with'}
        fluent = value in {'get','post','put','patch','delete','head'} and (receiver_at(i) in http_names
                    or ''.join(t[1] for t in ts[max(0,i-3):i]) in {'reqwest::','ureq::','isahc::'})
        if value == 'bytes' and not session_read: continue
        if value == 'execute' and (i < 2 or ts[i-1][1] != '.' or not re.search(r'(?:client|http|request|call)',ts[i-2][1],re.I)): continue
        before = [t[1] for t in ts[max(0,i-3):i]]
        is_builder = value == 'Builder' and 'Request' in before
        is_url = value in ('stringWithFormat','stringByAppendingString','format')
        if value not in native_calls and not is_builder and not is_url and not url_read and not session_read and not fluent: continue
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
        if session_read:start=session_start  # Bind inline/grouped receiver inputs too.
        message_start=opening_offsets[i][-1] if after==':' and opening_offsets[i] else start
        call = ts[start:end+1]
        if is_url and not any(t[0]=='string' and '/v1/' in t[1] for t in call): continue
        if value == 'Request' and is_builder: continue
        expression = token_expression(call)
        spans = [(a,b) for a,b in function_spans if a<=i<=b]
        construction_hash = None
        policy = ''
        if spans:
            a,b = min(spans,key=lambda span:span[1]-span[0])
            policy = source[ts[a][2]:ts[b][2]+1]
            construction_hash = hashlib.sha256(policy.encode()).hexdigest()
        shape = native_forwarding_shape(path,policy)
        found.append({'file':path,'line':source.count('\n',0,offset)+1,
                      'kind':'url' if is_url else 'request','owner':'<native>',
                      'callee':value,'operations':[], 'expression':expression, 'callText':source[ts[message_start][2]:ts[end][2]+len(ts[end][1])], 'constructionHash': construction_hash,
                      **({'forwardedRequest':True,'forwardingShape':shape,'guardHash':construction_hash} if shape else {})})
    return found


def scan(root=ROOT, paths=None):
    if paths is None:
        paths = subprocess.check_output(['git','ls-files','-z'],cwd=root).decode().split('\0')
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
        # Existing guard identities stay stable. Newly recognized adapters already
        # counted as direct keep their identity; every allowance still binds its
        # complete guardHash in classify/base_guard, independently of this ID.
        if site.get('forwardingShape') in {'request','requestRaw','request-without-signal','chapter-read','chapter-capability'} and site.get('guardHash'):
            identity.append(site['guardHash'])
        identity = json.dumps(identity, separators=(',',':'))
        site['id'] = site['file']+'#'+hashlib.sha256(identity.encode()).hexdigest()[:16]
    return sorted(sites,key=lambda s:(s['file'],s['line'],s['id']))


def scan_snapshot(root, ref):
    """Scan immutable source with the same installed declaration environment."""
    with source_snapshot(root,ref) as (snapshot,paths):
        return scan(snapshot,paths)


def verified_detection_counts(root, base, baseline, old_baseline, old_exceptions, manifest):
    """Growth needs both a reviewed exact-site ruling and the same site in base.

    The manifest authorizes a detection improvement, never a product addition:
    re-scan both its immutable source commit and the PR base using this detector.
    A deleted request cannot be reintroduced using an old manifest entry.
    """
    rising={key:n for key,n in baseline.items() if n>old_baseline.get(key,0)}
    if not rising or not manifest:return {}
    source=manifest.get('sourceCommit','');entries=manifest.get('sites',{})
    if not re.fullmatch(r'[a-f0-9]{40}',source):raise ValueError('Detection additions require an immutable sourceCommit')
    for key,entry in entries.items():
        if (not re.fullmatch(r'.+#[a-f0-9]{16}',key) or type(entry.get('occurrences')) is not int or entry['occurrences']<1
                or not entry.get('reason','').strip() or not isinstance(entry.get('rulingUrl'),str)
                or not re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/(?:pull|issues)/[1-9]\d*#issuecomment-[1-9]\d*',entry['rulingUrl'])):
            raise ValueError(f'{key}: detection addition requires exact count, reason and lead-ruling URL')
    if subprocess.run(['git','merge-base','--is-ancestor',source,base],cwd=root).returncode:
        raise ValueError('Detection sourceCommit must be an ancestor of the PR base')
    old_pins={key for entry in old_exceptions.values() for key in exception_pins(entry)}
    candidates=set(rising)&entries.keys()-old_pins
    if not candidates:return {}
    original=counts(scan_snapshot(root,source))
    at_base=original if subprocess.check_output(['git','rev-parse',base],cwd=root,text=True).strip()==source else counts(scan_snapshot(root,base))
    return {key:min(entries[key]['occurrences'],original.get(key,0),at_base.get(key,0)) for key in candidates}


def counts(sites):
    return dict(sorted(Counter(s['id'] for s in sites).items()))


def compare(actual, baseline):
    new = [f'{key}: {n} direct API site(s), baseline {baseline.get(key,0)}' for key,n in actual.items() if n>baseline.get(key,0)]
    stale = [key for key,n in baseline.items() if actual.get(key,0)<n]
    return new, stale


def exception_pins(entry):
    if set(entry['sites']) & set(entry.get('additionalSites', {})):
        raise ValueError('Exception ID overlap between sites and additionalSites is forbidden')
    return {**{key: {'category': entry['category'], 'reason': entry['reason'], **({'guardHash': entry.get('guardHashes', {}).get(key),
                **({'guardSpan':entry['guardSpans'][key]} if key in entry.get('guardSpans',{}) else {})} if entry['category']=='platform-transport' else {})} for key in entry['sites']},
            **entry.get('additionalSites', {})}


def matches_guard(site, expected, span=None):
    field={None:'guardHash','forwarding-function':'forwardingHash'}.get(span)
    return field is not None and expected is not None and expected==site.get(field)


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
        if allowed and pin['category']=='platform-transport' and not matches_guard(site,pin.get('guardHash'),pin.get('guardSpan')):
            raise ValueError(f"{site['file']}:{site['line']}: reviewed guard source changed or is unbound; re-approval and a new guardHash are required")
        if allowed and pin['category']=='platform-transport' and not site.get('forwardedRequest'):
            raise ValueError(f"{site['file']}:{site['line']}: platform transport must match an approved unchanged request/raw/chapter forwarding shape")
        (exempt if allowed else direct).append(site)
    return direct, exempt


def approval_for(approvals, key, pin, count):
    entry = approvals.get(key, {})
    return (entry.get('reason') == pin['reason'] and entry.get('condition', '').strip()
            and entry.get('category') == pin['category'] and entry.get('guardHash') == pin.get('guardHash')
            and entry.get('guardSpan')==pin.get('guardSpan')
            and type(entry.get('occurrences')) is int and entry['occurrences'] >= count
            and isinstance(entry.get('rulingUrl'), str)
            and re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/(?:pull|issues)/[1-9]\d*#issuecomment-[1-9]\d*', entry['rulingUrl']))


def validate_approvals(approvals):
    for key, entry in approvals.items():
        if (not re.fullmatch(r'.+#[a-f0-9]{16}', key) or not entry.get('reason', '').strip()
                or not entry.get('condition', '').strip() or type(entry.get('occurrences')) is not int or entry['occurrences'] < 1
                or entry.get('category') not in {'platform-transport','platform-url','native-extension','native-listening','separate-product','identity-rehash'}
                or not isinstance(entry.get('rulingUrl'),str)
                or not re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/(?:pull|issues)/[1-9]\d*#issuecomment-[1-9]\d*',entry['rulingUrl'])
                or entry['category'] in {'platform-transport','identity-rehash'} and not re.fullmatch(r'[a-f0-9]{64}',entry.get('guardHash',''))):
            raise ValueError(f'{key}: approval requires an exact site, condition, positive multiplicity, category/source binding and lead-ruling URL')
        if 'guardSpan' in entry and (entry['category']!='platform-transport' or entry['guardSpan']!='forwarding-function'):
            raise ValueError(f'{key}: invalid explicitly reviewed forwarding span')
        if entry['category']=='identity-rehash' or entry['rulingUrl']==REHASH_RULING or 'previousSiteId' in entry:
            if (entry['category'] not in {'identity-rehash','platform-transport'}
                    or not re.fullmatch(r'.+#[a-f0-9]{16}',entry.get('previousSiteId',''))
                    or key.split('#')[0]!=entry['previousSiteId'].split('#')[0]
                    or not re.fullmatch(r'[a-f0-9]{64}',entry.get('previousGuardHash',''))
                    or not re.fullmatch(r'[a-f0-9]{40}',entry.get('previousCommit',''))
                    or not re.fullmatch(r'[a-f0-9]{40}',entry.get('reviewedCommit',''))
                    or not re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/pull/[1-9]\d*',entry.get('pullRequest',''))
                    or not re.fullmatch(re.escape(entry.get('pullRequest',''))+r'#(?:issuecomment-|pullrequestreview-|discussion_r)[1-9]\d*',entry.get('reviewUrl',''))
                    or not re.fullmatch(r'docs/client-api-rehashes/[A-Za-z0-9._-]+\.json',entry.get('spanEvidence',''))):
                raise ValueError(f'{key}: re-hash requires old/new hashes, immutable commits, editing PR, independent review and complete span evidence')


def transport_rehash_budgets(approvals, old_approvals, exceptions):
    """Every retained/replacement allowance consumes its original shared budget."""
    nodes={};limits={};affected=set()
    for ledger in [old_approvals,approvals]:
        for key,entry in ledger.items():
            if entry['category']=='platform-transport':
                node=(key,entry['guardHash']);nodes[node]=entry
                limits[node]=min(limits.get(node,entry['occurrences']),entry['occurrences'])
    for key,entry in approvals.items():
        if entry['category']!='platform-transport' or 'previousSiteId' not in entry:continue
        previous=nodes.get((entry['previousSiteId'],entry['previousGuardHash']),{})
        if not previous or entry['occurrences']>limits[entry['previousSiteId'],entry['previousGuardHash']]:
            raise ValueError(f'{key}: transport re-hash expands the original occurrence budget; a new ruling is required')
    used=Counter()
    for entry in exceptions.values():
        counts=Counter(entry['sites'])+Counter(entry.get('additionalSites',{}).keys())
        for key,pin in exception_pins(entry).items():
            if pin['category']!='platform-transport':continue
            node=(key,pin['guardHash']);seen=set();lineage=[]
            while node in nodes:
                if node in seen:raise ValueError(f'{key}: cyclic transport re-hash lineage')
                seen.add(node);lineage.append(node);used[node]+=counts[key];parent=nodes[node]
                if 'previousSiteId' not in parent:break
                node=(parent['previousSiteId'],parent['previousGuardHash'])
            if len(lineage)>1:affected.update(lineage)
    for node,count in used.items():
        if node in affected and count>limits[node]:
            raise ValueError(f'{node[0]}: retained and replacement transport pins exceed their shared occurrence budget')


def verify_rehashes(root, base, sites, approvals, old_approvals, old_baseline=None, old_exceptions=None, exceptions=None):
    """Standing re-hash rulings never authorize a new request or an exception.

    Direct identities can relocate only against a removed base occurrence. A
    transport pin can re-hash only from an already-approved transport pin.
    Review links are checked by independent human review, as for all ledger rows.
    """
    moves={};snapshots={}
    transport_rehash_budgets(approvals,old_approvals,exceptions or {})
    for key,entry in approvals.items():
        if 'previousSiteId' not in entry:continue
        current=[s for s in sites if s['id']==key]
        if not current:continue  # A reviewed future source entry grants no active allowance.
        if entry==old_approvals.get(key):
            old_pin=exception_pins((old_exceptions or {}).get(current[0]['file'],{'sites':[]})).get(key,{})
            active=(old_baseline or {}).get(key,0)>=len(current) or old_pin.get('guardHash')==entry['guardHash']
            if active and all(matches_guard(s,entry['guardHash'],entry.get('guardSpan')) for s in current):continue
        previous=entry['previousSiteId'];commit=entry['previousCommit']
        for ancestor,descendant in [(commit,base)]:
            if subprocess.run(['git','merge-base','--is-ancestor',ancestor,descendant],cwd=root).returncode:
                raise ValueError(f'{key}: re-hash source/review commit is outside its immutable ancestry')
        if commit not in snapshots:snapshots[commit]=scan_snapshot(root,commit)
        old=[s for s in snapshots[commit] if s['id']==previous]
        reviewed_commit=entry['reviewedCommit']
        if reviewed_commit not in snapshots:snapshots[reviewed_commit]=scan_snapshot(root,reviewed_commit)
        reviewed=[s for s in snapshots[reviewed_commit] if s['id']==key and matches_guard(s,entry['guardHash'],entry.get('guardSpan'))]
        try:proof=json.loads((root/entry['spanEvidence']).read_text())
        except (OSError,ValueError) as error:raise ValueError(f'{key}: complete re-hash span evidence is missing or unreadable') from error
        if (not old or len(reviewed)<len(current) or any(not matches_guard(s,entry['previousGuardHash'],entry.get('guardSpan')) for s in old)
                or any(not matches_guard(s,entry['guardHash'],entry.get('guardSpan')) for s in current)
                or Counter(s.get('callText') for s in old)!=Counter(s.get('callText') for s in current)
                or not isinstance(proof,dict) or any(not isinstance(proof.get(field),str) for field in ['oldSpan','newSpan'])
                or any(hashlib.sha256(proof.get(field,'').encode()).hexdigest()!=entry[hash_field]
                       for field,hash_field in [('oldSpan','previousGuardHash'),('newSpan','guardHash')])):
            raise ValueError(f'{key}: re-hash changed forwarding bytes, source hashes or complete span evidence')
        if entry['category']=='platform-transport':
            original=old_approvals.get(previous,{})
            if (original.get('category')!='platform-transport' or original.get('guardHash')!=entry['previousGuardHash']
                    or original.get('guardSpan')!=entry.get('guardSpan')
                    or original.get('reason')!=entry['reason'] or original.get('condition')!=entry['condition']):
                raise ValueError(f'{key}: re-hash cannot create a new transport approval')
        else:moves[key]=(previous,min(entry['occurrences'],len(old),len(current)))
    return moves


def rehash_counts(baseline, old_baseline, moves):
    available={key:max(0,n-baseline.get(key,0)) for key,n in old_baseline.items()};permitted={}
    for key,(previous,n) in sorted(moves.items()):
        moved=min(n,available.get(previous,0));available[previous]=available.get(previous,0)-moved
        permitted[key]=old_baseline.get(key,0)+moved
    return permitted


def base_guard(baseline, exceptions, old_baseline, old_exceptions, approvals=None, detections=None, rehashes=None):
    permitted={**old_baseline}
    for key,count in (detections or {}).items():permitted[key]=max(permitted.get(key,0),count)
    for key,count in rehash_counts(baseline,old_baseline,rehashes or {}).items():permitted[key]=max(permitted.get(key,0),count)
    errors, _ = compare(baseline, permitted)
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


def main(argv=None, root=ROOT, paths=None):
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--update',action='store_true');p.add_argument('--initialize',action='store_true',help='one-time creation; refuses an existing baseline')
    p.add_argument('--base');p.add_argument('--predecessor');p.add_argument('--queue-head');p.add_argument('--report',type=Path);a=p.parse_args(argv)
    if a.queue_head and not a.base:p.error('--queue-head requires --base')
    exceptions=json.loads((root/ALLOWLIST).read_text())
    approvals=json.loads((root/APPROVALS).read_text()) if (root/APPROVALS).exists() else {}
    sites=scan(root,paths)
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
    detections={}
    try:
        refs=list(dict.fromkeys(merge_base(root,ref)for ref in [a.base,a.predecessor]if ref))
    except (ValueError,OSError,subprocess.CalledProcessError) as error:
        print(f'Client-core ownership ratchet failed: cannot resolve comparison base: {error}',file=sys.stderr);return 1
    if refs:new=[]
    for base in refs:
        try:
            # These files are required. An object/read failure is never evidence
            # that this is the one-time foundation initialization.
            old_baseline=historical_json(root,base,BASELINE)
            old_exceptions=historical_json(root,base,ALLOWLIST)
            old_approvals=historical_json(root,base,APPROVALS,optional=True) or {}
            manifest=json.loads((root/DETECTIONS).read_text()) if (root/DETECTIONS).exists() else {}
            moves=verify_rehashes(root,base,sites,approvals,old_approvals,old_baseline,old_exceptions,exceptions)
            proposed={key:max(n,actual.get(key,0)) for key,n in baseline.items()}
            proposed.update({key:n for key,n in actual.items() if key not in proposed})
            detections=verified_detection_counts(root,base,proposed,old_baseline,old_exceptions,manifest)
            permitted={**baseline}
            for key,count in detections.items():permitted[key]=max(permitted.get(key,0),count)
            for key,count in rehash_counts(actual,old_baseline,moves).items():permitted[key]=max(permitted.get(key,0),count)
            new+=compare(actual,permitted)[0]
            new+=base_guard(actual if a.update else baseline,exceptions,old_baseline,old_exceptions,approvals,detections,moves)
            previous,_=classify(scan_snapshot(root,base),old_exceptions)
            live=counts(previous)
            for key,count in rehash_counts(actual,live,moves).items():live[key]=max(live.get(key,0),count)
            new+=compare(actual,live)[0]
        except (ValueError,OSError,subprocess.CalledProcessError) as error:new.append(str(error))
    if a.queue_head:
        try:new+=check_queue(root,a.base,a.queue_head,lambda parent,snapshot,paths:main(['--base',parent],root=snapshot,paths=paths))
        except (ValueError,OSError,subprocess.CalledProcessError) as error:new.append(f'Cannot verify full queue history: {error}')
    pinned = sum((Counter(e['sites']) + Counter(e.get('additionalSites', {}).keys()) for e in exceptions.values()), Counter())
    stale_exceptions = list((pinned - Counter(counts(exempt))).elements())
    if stale_exceptions:new += [f'{site}: stale exception; remove its allowance' for site in stale_exceptions]
    if a.report:a.report.write_text(json.dumps({'direct':direct,'exceptions':exempt},indent=2)+'\n')
    if new:
        print('Client-core ownership ratchet failed: new/increased direct API sites outside packages/client-core.',file=sys.stderr)
        print('\n'.join(new),file=sys.stderr)
        print('Move URL/request/body policy into named client-core domain operations; do not raise the baseline or use a generic screen request wrapper.',file=sys.stderr)
        return 1
    if a.update and actual!=baseline:baseline_path.write_text(json.dumps(actual,indent=2)+'\n')
    print(f'Client-core ownership holds: {len(direct)} direct sites; {len(exempt)} pinned approved exception sites.')
    return 0


if __name__=='__main__':sys.exit(main())
