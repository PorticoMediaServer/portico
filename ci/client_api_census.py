#!/usr/bin/env python3
"""Direct client API calls outside client-core may move or fall, but their total may not rise."""
import argparse
import json
from pathlib import Path
import re
import subprocess
import sys
import unicodedata
from ratchet_base import merge_base, source_snapshot, check_queue
from ratchet_runtime import RatchetRuntime, active_runtime

ROOT = Path(__file__).resolve().parent.parent
SCANNER = ROOT / 'ci/client_api_sites.mjs'
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
        found.append({'file':path,'line':source.count('\n',0,offset)+1,
                      'kind':'url' if is_url else 'request', 'callee':value,
                      'expression':expression, 'callText':source[ts[message_start][2]:ts[end][2]+len(ts[end][1])]})
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
    return sites


def _main(argv, root=ROOT, paths=None):
    runtime=active_runtime(root)
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base');parser.add_argument('--predecessor');parser.add_argument('--queue-head')
    args=parser.parse_args(argv)
    if args.queue_head and not args.base:parser.error('--queue-head requires --base')
    for field in ['base','predecessor','queue_head']:
        if getattr(args,field):setattr(args,field,runtime.git.git(['rev-parse','--verify',getattr(args,field)+'^{commit}']).decode().strip())
    errors=check_queue(root,args.predecessor or args.base,args.queue_head,
        lambda parent,snapshot,paths:main(['--base',parent],root=snapshot,paths=paths)) if args.queue_head else []
    total=len(scan(root,paths))
    for ref in dict.fromkeys(merge_base(root,ref) for ref in [args.base,args.predecessor] if ref):
        with source_snapshot(root,ref) as (snapshot,old_paths):
            previous=len(scan(snapshot,old_paths))
        print(f'Direct client API calls: {previous} -> {total} (base {ref[:12]})')
        if total>previous:errors.append('Direct client API call count increased; move calls into packages/client-core.')
    if not args.base and not args.predecessor:print(f'Direct client API calls: {total} (inventory only; pass --base to compare)')
    for error in errors:print(error)
    return int(bool(errors))


def main(argv=None, root=ROOT, paths=None):
    try:
        if runtime:=active_runtime(root):
            status=_main(argv,root,paths);runtime.ensure_head();return status
        with RatchetRuntime(root) as runtime:
            status=_main(argv,root,paths);runtime.ensure_head();return status
    except (ValueError,OSError,RuntimeError,subprocess.CalledProcessError) as error:
        print(f'Client API count failed: {error}',file=sys.stderr);return 1


if __name__=='__main__':sys.exit(main(sys.argv[1:]))
