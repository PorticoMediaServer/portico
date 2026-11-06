#!/usr/bin/env python3
"""All-verb client API ownership ratchet. See docs/client-api-ownership.md.

python3 ci/client_api_census.py [--update] [--base <review base>] [--report <json>]
"""
import argparse
from bisect import bisect_right
from collections import Counter
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import unicodedata
from ratchet_base import merge_base, historical_json, source_snapshot, check_queue
from ratchet_runtime import RatchetRuntime, active_runtime

ROOT = Path(__file__).resolve().parent.parent
SCANNER = ROOT / 'ci/client_api_sites.mjs'
BASELINE = 'ci/client-api-baseline.json'
ALLOWLIST = 'ci/client-api-exceptions.json'
APPROVALS = 'ci/client-api-approvals.json'
DETECTIONS = 'ci/client-api-detection-additions.json'
REHASH_RULING = 'https://github.com/PorticoMediaServer/portico/pull/880#issuecomment-6646375725'
SOURCE_ROOTS = ('web/', 'apps/native/', 'apps/desktop/', 'apps/vega/',
                'packages/player-core/', 'packages/reader-core/', 'website/', 'server/receiver/cast/')
EXTENSIONS = {'.ts', '.tsx', '.mts', '.cts', '.js', '.jsx', '.mjs', '.cjs', '.kt', '.java', '.swift', '.m', '.mm', '.rs'}


def production(path):
    return (path.startswith(SOURCE_ROOTS) and Path(path).suffix in EXTENSIONS
            and not re.search(r'(?:^|/)(?:tests?|__tests__|scripts|node_modules|build|dist|Pods|dev)/|\.(?:test|spec|browser|capture)\.|\.d\.[cm]?ts$', path)
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
    def regex_end(start, expression):
        opening=re.match(r'(#+)?/',source[start:])
        if not opening:return None
        hashes=opening[1] or '';i=start+opening.end();content=i
        if not hashes and (not expression or i==len(source) or source[i].isspace()):return None
        if not hashes:
            operator=start
            while operator and source[operator-1] in '+-*/%=<>!&|^~?.':operator-=1
            if '/' in source[operator:start] or (operator<start and operator and
                    (source[operator-1].isalnum() or source[operator-1] in '_)]}')):return None
        multiline=bool(hashes and source[i:i+1] in ('\r','\n'))
        classes=rounds=0;escaped=False
        while i<len(source):
            char=source[i]
            if char in '\r\n' and not multiline:return None
            if char=='\\':
                if source[i+1:i+2] in ('\r','\n') and not multiline:return None
                i+=2;escaped=True;continue
            if not classes and source.startswith('/'+hashes,i):
                if not hashes and (i==content or (source[i-1] in ' \t' and not escaped)):return None
                line=max(source.rfind('\n',0,i),source.rfind('\r',0,i))+1
                if not multiline or not source[line:i].strip():return i+1+len(hashes)
            if char=='[':classes+=1
            elif char==']' and classes:classes-=1
            elif not classes:
                if char=='(':rounds+=1
                elif char==')':
                    if not hashes and not rounds:return None  # An unapplied/prefix '/' operator.
                    rounds-=1
                elif char=='#' and multiline:
                    end=re.search(r'[\r\n]',source[i:]);i=len(source) if end is None else i+end.start();continue
            i+=1;escaped=False
        return None
    def code(i, interpolation=False):
        rounds=0;expression=True;last_end=i
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
            # SE-0354: extended regexes are unambiguous. Bare '/' needs an
            # expression position, balanced ')' and a same-line terminator;
            # division/operators must leave their executable operands visible.
            end=regex_end(i,expression or '\n' in source[last_end:i]) if swift else None
            if end is not None:
                i=end;out.append(('string',source[start:i],start));expression=False;last_end=i;continue
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
                out[slot]=('string',source[start:i],start);expression=False;last_end=i;continue
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
                i += word.end(); out.append(('id', word[0], start))
                expression=word[0] in ('return','throw','case','in','try','await');last_end=i;continue
            out.append(('punct', source[i], i))
            if interpolation:
                if source[i]==')' and rounds==0:return i+1
                if source[i]=='(':rounds+=1
                elif source[i]==')':rounds-=1
            expression=(not source[i].isdigit() and source[i] not in ')]}.'
                        and (source[i] not in '!?' or expression))
            i += 1;last_end=i
        return i
    code(0)
    return out


def kotlin_span_tokens(source):
    """Kotlin tokens and actual NLs with literal/trivia boundaries kept separate.

    Comments are hidden trivia, including their newlines. Literal payloads stay
    opaque, but interpolation code is lexed to find the real closing delimiter.
    Returned offsets still refer to the complete original source.
    """
    out, newlines, n = [], [], len(source)
    def letter(char):return unicodedata.category(char) in {'Lu','Ll','Lt','Lm','Lo'}
    def block_comment(i):
        depth=1;i+=2
        while i<n and depth:
            if source.startswith('/*',i):depth+=1;i+=2
            elif source.startswith('*/',i):depth-=1;i+=2
            else:i+=1
        return i,depth==0
    def quoted(i):
        quote=source[i];raw=source.startswith('"""',i);i+=3 if raw else 1
        while i<n:
            if raw and source.startswith('"""',i):
                while i<n and source[i]=='"':i+=1
                return i,True
            if not raw and source[i]=='\\':i+=2;continue
            if not raw and source[i]==quote:return i+1,True
            if not raw and source[i] in '\r\n':return n,False
            if quote=='"' and source.startswith('${',i):
                i,closed=code(i+2,interpolation=True,emit=False)
                if not closed:return n,False
            else:i+=1
        return n,False
    def code(i, interpolation=False, emit=True):
        depth=0
        while i<n:
            start=i;char=source[i]
            if char.isspace():
                if char=='\n' and emit:newlines.append(i)
                i+=1;continue
            if source.startswith('//',i):
                end=source.find('\n',i+2);i=n if end<0 else end;continue
            if source.startswith('/*',i):
                i,closed=block_comment(i)
                if not closed:
                    if emit:out.append(('invalid',source[start:i],start))
                    return n,False
                continue
            if char in {'"',"'"}:
                i,closed=quoted(i)
                if emit:out.append(('string' if closed else 'invalid',source[start:i],start))
                if not closed:return n,False
                continue
            if char=='`':
                i+=1
                while i<n and source[i] not in '`\r\n':i+=1
                closed=i<n and source[i]=='`' and i>start+1
                if closed:i+=1
                if emit:out.append(('quoted-id' if closed else 'invalid',source[start:i],start))
                if not closed:return n,False
                continue
            if char=='_' or letter(char):
                i+=1
                while i<n and (source[i]=='_' or letter(source[i]) or unicodedata.category(source[i])=='Nd'):i+=1
                if emit:out.append(('id',source[start:i],start))
                continue
            if char=='{' and interpolation:depth+=1
            elif char=='}' and interpolation:
                if not depth:return i+1,True
                depth-=1
            if emit:out.append(('punct',char,i))
            i+=1
        return n,not interpolation
    code(0)
    return out,newlines


def native_function_spans(ts, source, path, line_breaks=None):
    """Complete method source ranges [start,end), including builder inputs."""
    if path.endswith('.kt') and line_breaks is None:ts,line_breaks=kotlin_span_tokens(source)
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
    def balanced_prefix(at):
        pairs={'(':')','[':']','{':'}','<':'>'};stack=[]
        angles=ts[at][1]=='<'
        for index in range(at,len(ts)):
            kind,value,_=ts[index]
            if kind=='string':continue
            if value in pairs and (value!='<' or angles and (not stack or stack[-1]=='>')):
                stack.append(pairs[value])
            elif value in ')]}' or value=='>' and angles and stack and stack[-1]=='>' and ts[index-1][1]!='-':
                if not stack or stack.pop()!=value:return None
                if not stack:return index+1
        return None
    def identifier_end(at):
        hard={'as','break','class','continue','do','else','false','for','fun','if','in','interface',
              'is','null','object','package','return','super','this','throw','true','try','typealias','typeof','val','var','when','while'}
        if at<len(ts) and ts[at][0]=='id' and ts[at][1] not in hard:return at+1
        if at<len(ts) and ts[at][0]=='quoted-id':return at+1
        if at<len(ts) and ts[at][1]=='`':
            for end in range(at+1,len(ts)):
                if '\n' in source[ts[at][2]:ts[end][2]]:return None
                if ts[end][1]=='`':return end+1 if end>at+1 else None
        return None
    def annotation_name_end(at):
        end=identifier_end(at)
        if end is None:return None
        while end<len(ts) and ts[end][1]=='.':
            end=identifier_end(end+1)
            if end is None:return None
        if end<len(ts) and ts[end][1]=='(':return balanced_prefix(end)
        return end
    def named_function(at):
        # A receiver is not the function name: the final simpleIdentifier must
        # be followed by the parameter list. Never scan past an anonymous list.
        if at<len(ts) and ts[at][1]=='<':
            at=balanced_prefix(at)
            if at is None:return False
        if at<len(ts) and ts[at][1]=='interface':return identifier_end(at+1) is not None
        while at<len(ts):
            if ts[at][1]=='@':
                at=annotation_name_end(at+1)
                if at is None:return False
                continue
            if ts[at][1]=='(':
                end=balanced_prefix(at)
                if end is None:return False
            else:
                end=identifier_end(at)
                if end is None:return False
                if end<len(ts) and ts[end][1]=='(':
                    close=balanced_prefix(end)
                    return close is not None and (close==len(ts) or ts[close][1] not in {'.','?','-','<'})
                if end<len(ts) and ts[end][1]=='<':
                    end=balanced_prefix(end)
                    if end is None:return False
            while end<len(ts) and ts[end][1]=='?':end+=1
            if end>=len(ts) or ts[end][1]!='.':return False
            at=end+1
        return False
    def kotlin_declaration_start(at):
        # Kotlin modifiers may interleave with complete annotations. Only their
        # prefix is skipped; it never becomes part of the preceding try span.
        modifiers={'enum','sealed','annotation','data','inner','value','override','lateinit',
                   'public','private','internal','protected','tailrec','operator','infix','inline',
                   'external','suspend','const','abstract','final','open','vararg','noinline','crossinline','expect','actual'}
        targets={'file','field','property','get','set','receiver','param','setparam','delegate'}
        while at<len(ts):
            if ts[at][1] in modifiers:at+=1;continue
            if ts[at][1]!='@':break
            at+=1
            if at+1<len(ts) and ts[at+1][1]==':':
                if ts[at][1] not in targets:return False
                at+=2
            if at<len(ts) and ts[at][1]=='[':
                at+=1;count=0
                while at<len(ts) and ts[at][1]!=']':
                    at=annotation_name_end(at)
                    if at is None:return False
                    count+=1
                if not count or at>=len(ts):return False
                at+=1
            else:
                at=annotation_name_end(at)
                if at is None:return False
        if at>=len(ts):return False
        declaration=ts[at][1];following=at+1
        if declaration=='fun':return named_function(following)
        if declaration in {'class','object','interface','typealias'}:
            return identifier_end(following) is not None
        if declaration in {'val','var'}:return identifier_end(following) is not None or following<len(ts) and ts[following][1]=='('
        if declaration=='companion':return following<len(ts) and ts[following][1] in {'object','data'}
        return declaration in {'init','constructor'} and following<len(ts) and ts[following][1]==('{' if declaration=='init' else '(')
    def kotlin_try_end(start):
        if start+1>=len(ts) or ts[start][1]!='try' or ts[start+1][1]!='{':return None
        end=closes.get(start+1)
        if end is None:return None
        clause=False
        while end+1<len(ts) and ts[end+1][1]=='catch':
            arguments=end+2
            if arguments>=len(ts) or ts[arguments][1]!='(':return None
            close=rounds.get(arguments)
            if close is None or close+1>=len(ts) or ts[close+1][1]!='{':return None
            end=closes.get(close+1)
            if end is None:return None
            clause=True
        if end+1<len(ts) and ts[end+1][1]=='finally':
            body=end+2
            if body>=len(ts) or ts[body][1]!='{':return None
            end=closes.get(body)
            if end is None:return None
            clause=True
        if not clause:return None
        if end+1<len(ts) and ts[end+1][1] not in {'}',';'}:
            # Support the complete standalone try expression, never a prefix of
            # a chained/Elvis/cast/infix expression. Unknown tails stay unbound.
            following=end+1
            newline=bisect_right(line_breaks,ts[end][2])
            if newline==len(line_breaks) or line_breaks[newline]>=ts[following][2]:return None
            if not kotlin_declaration_start(following):return None
        return end
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
            elif depth == 0 and token == '=':
                if path.endswith('.kt'):
                    end=kotlin_try_end(j+1)
                    if end is not None:spans.append((i,end))
                break
            elif depth == 0 and token == ';': break
            elif depth == 0 and token == '{':
                if j in closes: spans.append((i,closes[j]))
                break
    return [(ts[a][2],ts[b][2]+1) for a,b in spans]


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


def token_expression(ts):
    values=[];literal_end=-1
    for kind,value,offset in ts:
        if offset<literal_end:continue
        values.append(value)
        if kind=='string':literal_end=offset+len(value)
    return ' '.join(values)


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
    line_breaks=None
    if path.endswith('.kt'):ts,line_breaks=kotlin_span_tokens(source)
    else:ts=tokens(source,rust=path.endswith('.rs'),swift=path.endswith('.swift'))
    # Transport evidence comes from native imports, declared types and constructor
    # bindings. A file's readText/readBytes/data/get methods alone prove no HTTP.
    code = ' '.join(value if kind not in {'string','quoted-id','invalid'} else '""' for kind,value,_ in ts)
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
    function_spans = native_function_spans(ts, source, path,line_breaks)
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
        spans = [(a,b) for a,b in function_spans if a<=offset<b]
        construction_hash = None
        policy = ''
        if spans:
            a,b = min(spans,key=lambda span:span[1]-span[0])
            policy = source[a:b]
            construction_hash = hashlib.sha256(policy.encode()).hexdigest()
        shape = native_forwarding_shape(path,policy)
        found.append({'file':path,'line':source.count('\n',0,offset)+1,
                      'kind':'url' if is_url else 'request','owner':'<native>',
                      'callee':value,'operations':[], 'expression':expression, 'callText':source[ts[message_start][2]:ts[end][2]+len(ts[end][1])], 'constructionHash': construction_hash,
                      **({'forwardedRequest':True,'forwardingShape':shape,'guardHash':construction_hash} if shape else {})})
    return found


def scan(root=ROOT, paths=None):
    runtime=active_runtime(root)
    if runtime:return runtime.census(runtime.current(root,paths))
    if paths is None:
        paths = subprocess.check_output(['git','ls-files','-z'],cwd=root).decode().split('\0')
    paths = sorted(p for p in paths if p and production(p))
    js = [p for p in paths if Path(p).suffix in {'.ts','.tsx','.mts','.cts','.js','.jsx','.mjs','.cjs'}]
    if js:
        result = subprocess.run(['node',str(SCANNER)],cwd=root,
                                input=json.dumps({'root':str(root),'files':js}),text=True,capture_output=True,check=True)
        sites = json.loads(result.stdout)
    else: sites = []
    for path in paths:
        if path not in js: sites.extend(native_sites(path,(root/path).read_text()))
    return identify_sites(sites)


def identify_sites(sites):
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


def scan_snapshot(root, ref, files=None):
    with source_snapshot(root,ref) as (snapshot,paths):
        return active_runtime(snapshot).census(active_runtime(snapshot).current(snapshot),files)


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
    old_pins={source_id(key) for entry in old_exceptions.values() for key in exception_pins(entry)}
    candidates=set(rising)&entries.keys()-old_pins
    if not candidates:return {}
    files={key.split('#')[0] for key in candidates}
    original=counts(scan_snapshot(root,source,files))
    at_base=original if subprocess.check_output(['git','rev-parse',base],cwd=root,text=True).strip()==source else counts(scan_snapshot(root,base,files))
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
    pins = {**{key: {'category': entry['category'], 'reason': entry['reason'], **({'guardHash': entry.get('guardHashes', {}).get(key),
                **({'guardSpan':entry['guardSpans'][key]} if key in entry.get('guardSpans',{}) else {})} if entry['category'] in {'platform-transport','non-api-platform'} or entry.get('guardSpans',{}).get(key)=='native-function' else {}),
                **({'sourceHashes':entry['sourceHashes'][key]} if key in entry.get('sourceHashes',{}) else {})} for key in entry['sites']},
            **entry.get('additionalSites', {})}
    budgets=Counter(entry['sites'])+Counter(entry.get('additionalSites',{}).keys())
    for key,pin in pins.items():
        if '@' not in key:continue
        site,span=key.rsplit('@',1)
        if (not re.fullmatch(r'.+#[a-f0-9]{16}',site) or not re.fullmatch(r'[a-f0-9]{64}',span)
                or pin['category']!='platform-transport' or pin.get('guardHash')!=span or budgets[key]!=1):
            raise ValueError(f'{key}: an exact ID/hash pair requires its full guardHash and occurrence count 1')
        if site in pins:raise ValueError(f'{key}: paired and unqualified allowances cannot overlap')
    return pins


def source_id(key):
    return key.rsplit('@',1)[0] if '@' in key else key


def exception_counts(exceptions):
    return sum((Counter(e['sites'])+Counter(e.get('additionalSites',{}).keys()) for e in exceptions.values()),Counter())


def matches_guard(site, expected, span=None):
    if span=='native-function' and Path(site['file']).suffix not in {'.kt','.java','.m','.mm','.swift','.rs'}:return False
    field={None:'guardHash','forwarding-function':'forwardingHash','native-function':'constructionHash'}.get(span)
    return field is not None and expected is not None and expected==site.get(field)


def validate_native_pin(key, pin, count):
    if pin.get('guardSpan')!='native-function':return
    path=key.split('#')[0]
    if (pin['category'] not in {'platform-transport','platform-url'}
            or not path.startswith('apps/native/') or Path(path).suffix not in {'.kt','.java','.m','.mm','.swift'}
            or not re.fullmatch(r'[a-f0-9]{64}',pin.get('guardHash') or '') or count!=1
            or not isinstance(pin.get('sourceHashes'),dict) or path not in pin['sourceHashes']):
        raise ValueError(f'{key}: native-function requires one exact native span and its complete policy-source hash')


def validate_source_hashes(key, hashes):
    if not isinstance(hashes,dict) or not hashes:
        raise ValueError(f'{key}: exact condition source hashes are required')
    for path,digest in hashes.items():
        if (not isinstance(path,str) or not Path(path).parts or Path(path).is_absolute() or '..' in Path(path).parts
                or '.git' in Path(path).parts or 'node_modules' in Path(path).parts
                or not isinstance(digest,str) or not re.fullmatch(r'[a-f0-9]{64}',digest)):
            raise ValueError(f'{key}: invalid exact condition source hash')


def condition_source_hash(root, path, key):
    """Read exact regular files without following any below-root symlink.

    Directory descriptors bind each parent before opening the next component;
    checking resolve()/hashes alone permits relocated relative dependencies.
    """
    descriptors=[]
    try:
        flags=os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
        descriptors.append(os.open(root,flags))
        parts=Path(path).parts
        for part in parts[:-1]:
            descriptors.append(os.open(part,flags,dir_fd=descriptors[-1]))
        descriptors.append(os.open(parts[-1],os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK,dir_fd=descriptors[-1]))
        if not stat.S_ISREG(os.fstat(descriptors[-1]).st_mode):
            raise ValueError(f'{key}: condition source must be a regular file without symlinks: {path}')
        digest=hashlib.sha256()
        while chunk:=os.read(descriptors[-1],131072):digest.update(chunk)
        return digest.hexdigest()
    except OSError as error:
        raise ValueError(f'{key}: cannot read condition source as a regular file without symlinks: {path}: {error}') from error
    finally:
        for descriptor in reversed(descriptors):os.close(descriptor)


def classify(sites, exceptions, root=None):
    approved = {'platform-url','native-extension','native-listening','separate-product','platform-transport','non-api-platform'}
    exempt, direct = [], []
    remaining = {path: Counter(entry['sites']) + Counter(entry.get('additionalSites', {}).keys()) for path,entry in exceptions.items()}
    for path,entry in exceptions.items():
        if any(pin['category'] not in approved or not pin['reason'].strip() for pin in exception_pins(entry).values()) or any(c in path for c in '*?['):
            raise ValueError(f'Invalid exception (exact file, approved category and reason required): {path}')
        if not entry.get('sites'): raise ValueError(f'Exception must pin its exact construction/request sites: {path}')
        for key,pin in exception_pins(entry).items():
            validate_native_pin(key,pin,remaining[path][key])
            if pin['category']=='non-api-platform' or 'sourceHashes' in pin:
                validate_source_hashes(key,pin.get('sourceHashes'))
                if root is None:raise ValueError(f'{key}: checking condition source hashes requires the exact source root')
                for source_path,digest in pin['sourceHashes'].items():
                    if condition_source_hash(root,source_path,key)!=digest:
                        raise ValueError(f'{key}: reviewed condition source changed: {source_path}; re-approval is required')
    for site in sites:
        entry = exceptions.get(site['file'])
        pins=exception_pins(entry) if entry else {}
        pair=site['id']+'@'+str(site.get('guardHash'))
        binding=pair if pair in pins else site['id']
        pin=pins.get(binding)
        allowed = pin is not None and remaining[site['file']][binding] > 0
        if allowed: remaining[site['file']][binding] -= 1
        if allowed and (pin['category']=='platform-transport' or pin.get('guardSpan')=='native-function') and not matches_guard(site,pin.get('guardHash'),pin.get('guardSpan')):
            raise ValueError(f"{site['file']}:{site['line']}: reviewed guard source changed or is unbound; re-approval and a new guardHash are required")
        if allowed and pin['category']=='platform-transport' and pin.get('guardSpan')!='native-function' and not site.get('forwardedRequest'):
            raise ValueError(f"{site['file']}:{site['line']}: platform transport must match the closed forwarder grammar or an exact frozen legacy span")
        if allowed and pin['category']=='non-api-platform' and not matches_guard(site,pin.get('guardHash')):
            raise ValueError(f"{site['file']}:{site['line']}: reviewed non-API source span changed; re-approval is required")
        if allowed:exempt.append(dict(site,exceptionId=binding))
        else:direct.append(site)
    return direct, exempt


def approval_for(approvals, key, pin, count):
    entry = approvals.get(key, {})
    return (entry.get('reason') == pin['reason'] and entry.get('condition', '').strip()
            and entry.get('category') == pin['category'] and entry.get('guardHash') == pin.get('guardHash')
            and entry.get('guardSpan')==pin.get('guardSpan')
            and entry.get('sourceHashes')==pin.get('sourceHashes')
            and type(entry.get('occurrences')) is int and entry['occurrences'] >= count
            and isinstance(entry.get('rulingUrl'), str)
            and re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/(?:pull|issues)/[1-9]\d*#issuecomment-[1-9]\d*', entry['rulingUrl']))


def validate_approvals(approvals):
    for key, entry in approvals.items():
        if (not re.fullmatch(r'.+#[a-f0-9]{16}(?:@[a-f0-9]{64})?', key) or not entry.get('reason', '').strip()
                or not entry.get('condition', '').strip() or type(entry.get('occurrences')) is not int or entry['occurrences'] < 1
                or entry.get('category') not in {'platform-transport','platform-url','native-extension','native-listening','separate-product','identity-rehash','non-api-platform'}
                or not isinstance(entry.get('rulingUrl'),str)
                or not re.fullmatch(r'https://github\.com/PorticoMediaServer/portico/(?:pull|issues)/[1-9]\d*#issuecomment-[1-9]\d*',entry['rulingUrl'])
                or entry['category'] in {'platform-transport','identity-rehash','non-api-platform'} and not re.fullmatch(r'[a-f0-9]{64}',entry.get('guardHash',''))):
            raise ValueError(f'{key}: approval requires an exact site, condition, positive multiplicity, category/source binding and lead-ruling URL')
        if entry['category']=='non-api-platform' or 'sourceHashes' in entry:
            validate_source_hashes(key,entry.get('sourceHashes'))
        validate_native_pin(key,entry,entry['occurrences'])
        if '@' in key and (entry['category']!='platform-transport' or key.rsplit('@',1)[1]!=entry.get('guardHash')
                           or entry['occurrences']!=1 or 'previousSiteId' in entry):
            raise ValueError(f'{key}: paired approval requires its exact full hash, count 1 and an explicit ruling')
        if 'guardSpan' in entry and entry['guardSpan']!='native-function' and (entry['category']!='platform-transport' or entry['guardSpan']!='forwarding-function'):
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
        old_key=(commit,previous.split('#')[0])
        if old_key not in snapshots:snapshots[old_key]=scan_snapshot(root,commit,{old_key[1]})
        old=[s for s in snapshots[old_key] if s['id']==previous]
        reviewed_commit=entry['reviewedCommit']
        reviewed_key=(reviewed_commit,key.split('#')[0])
        if reviewed_key not in snapshots:snapshots[reviewed_key]=scan_snapshot(root,reviewed_commit,{reviewed_key[1]})
        reviewed=[s for s in snapshots[reviewed_key] if s['id']==key and matches_guard(s,entry['guardHash'],entry.get('guardSpan'))]
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


def base_guard(baseline, exceptions, old_baseline, old_exceptions, approvals=None, detections=None, rehashes=None, site_counts=None, base_site_counts=None):
    permitted={**old_baseline}
    for key,count in (detections or {}).items():permitted[key]=max(permitted.get(key,0),count)
    for key,count in rehash_counts(baseline,old_baseline,rehashes or {}).items():permitted[key]=max(permitted.get(key,0),count)
    errors, _ = compare(baseline, permitted)
    # New/changed allowances require a reviewed ruling for this exact site,
    # source hash, reason/category and complete multiplicity. Ledger edits are
    # themselves reviewable; an old approval cannot authorize a larger count.
    approvals = approvals or {}
    before=exception_counts(old_exceptions);after=exception_counts(exceptions)
    # Converting DIRECT duplicates to distinct paired exceptions must not let an
    # extra occurrence spend the same old DIRECT budget a second time. Check the
    # complete source multiset (before classification), not only the residuals.
    paired={source_id(key) for key in after if '@' in key and after[key]>before[key]}
    old_total=Counter(old_baseline)
    for key,n in before.items():old_total[source_id(key)]+=n
    for key in paired:
        if site_counts is None:
            errors.append(f'{key}: paired activation requires complete source occurrence counts')
        elif site_counts.get(key,0)>old_total[key]:
            errors.append(f'{key}: paired activation grows total source occurrences; a new detection ruling is required')
    # A stale-high upper bound must not supply removal credit when a formerly
    # DIRECT occurrence becomes exempt. Check its live combined multiplicity,
    # including the unapproved occurrences left after classification.
    if base_site_counts is not None:
        activated={source_id(key) for key in after if after[key]>before[key]}
        for key in activated:
            if base_site_counts.get(key,0) and (site_counts or {}).get(key,0)>base_site_counts[key]:
                errors.append(f'{key}: exception activation grows live source occurrences against the merge base')
    for path,entry in exceptions.items():
        pins=exception_pins(entry);old=old_exceptions.get(path)
        previous=exception_pins(old) if old else {}
        total=Counter(entry['sites'])+Counter(entry.get('additionalSites', {}).keys())
        was=Counter(old['sites'])+Counter(old.get('additionalSites', {}).keys()) if old else Counter()
        for key,pin in pins.items():
            if (previous.get(key)!=pin or total[key]>was[key]) and not approval_for(approvals,key,pin,total[key]):
                errors.append(f'{key}: exception expanded or changed without an exact lead-ruling approval in {APPROVALS}')
    return errors


def _main(argv=None, root=ROOT, paths=None):
    write_root=root
    runtime=active_runtime(root)
    view=runtime.current(root,paths);root=view.root;paths=view.paths
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--update',action='store_true');p.add_argument('--initialize',action='store_true',help='one-time creation; refuses an existing baseline')
    p.add_argument('--base');p.add_argument('--predecessor');p.add_argument('--queue-head');p.add_argument('--report',type=Path);a=p.parse_args(argv)
    if a.queue_head and not a.base:p.error('--queue-head requires --base')
    for field in ['base','predecessor','queue_head']:
        if getattr(a,field):setattr(a,field,runtime.git.git(['rev-parse','--verify',getattr(a,field)+'^{commit}']).decode().strip())
    queue_errors=check_queue(root,a.base,a.queue_head,lambda parent,snapshot,paths:main(['--base',parent],root=snapshot,paths=paths)) if a.queue_head else []
    exceptions=json.loads((root/ALLOWLIST).read_text())
    approvals=json.loads((root/APPROVALS).read_text()) if (root/APPROVALS).exists() else {}
    sites=scan(root,paths)
    try:
        validate_approvals(approvals)
        direct,exempt=classify(sites,exceptions,root=root)
    except ValueError as error:
        print(f"Client-core ownership ratchet failed: {error}", file=sys.stderr); return 1
    actual=counts(direct)
    baseline_path=root/BASELINE
    if a.initialize:
        if baseline_path.exists():p.error('baseline already exists; --update only removes sites')
        baseline_path.write_text(json.dumps(actual,indent=2)+'\n')
        (write_root/BASELINE).write_text(json.dumps(actual,indent=2)+'\n')
    baseline=json.loads(baseline_path.read_text())
    new,stale=compare(actual,baseline)
    detections={}
    refs=list(dict.fromkeys(merge_base(root,ref)for ref in [a.base,a.predecessor]if ref))
    if refs:new=[]
    for base in refs:
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
        base_view=runtime.historical(base)
        base_sites=runtime.census(base_view)
        new+=base_guard(actual if a.update else baseline,exceptions,old_baseline,old_exceptions,approvals,detections,moves,counts(sites),counts(base_sites))
        previous,_=classify(base_sites,old_exceptions,root=base_view.root)
        live=counts(previous)
        for key,count in rehash_counts(actual,live,moves).items():live[key]=max(live.get(key,0),count)
        new+=compare(actual,live)[0]
    new+=queue_errors
    pinned = exception_counts(exceptions)
    stale_exceptions = list((pinned - Counter(s.get('exceptionId',s['id']) for s in exempt)).elements())
    if stale_exceptions:new += [f'{site}: stale exception; remove its allowance' for site in stale_exceptions]
    if a.report:a.report.write_text(json.dumps({'direct':direct,'exceptions':exempt},indent=2)+'\n')
    if new:
        print('Client-core ownership ratchet failed: new/increased direct API sites outside packages/client-core.',file=sys.stderr)
        print('\n'.join(new),file=sys.stderr)
        print('Move URL/request/body policy into named client-core domain operations; do not raise the baseline or use a generic screen request wrapper.',file=sys.stderr)
        return 1
    if a.update and actual!=baseline:(write_root/BASELINE).write_text(json.dumps(actual,indent=2)+'\n')
    print(f'Client-core ownership holds: {len(direct)} direct sites; {len(exempt)} pinned approved exception sites.')
    return 0


def main(argv=None, root=ROOT, paths=None):
    try:
        if runtime:=active_runtime(root):
            status=_main(argv,root,paths);runtime.ensure_head();return status
        with RatchetRuntime(root) as runtime:
            status=_main(argv,root,paths);runtime.ensure_head();return status
    except (ValueError,OSError,RuntimeError,subprocess.CalledProcessError) as error:
        print(f'Client-core ownership ratchet failed closed: {error}',file=sys.stderr);return 1


if __name__=='__main__':sys.exit(main())
