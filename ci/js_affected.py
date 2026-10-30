"""Short-tier graph using the maintained legacy resolver, without its campaign exclusions."""
import ast
import json
from pathlib import Path
import re


def select(root, paths, candidates):
    # Reuse only the pure resolver; importing the historical runner has side effects.
    tree = ast.parse((root/'scripts/ci-affected.py').read_text())
    resolver = next(n for n in tree.body if isinstance(n, ast.FunctionDef) and n.name == 'dependencies')
    scope = {'ROOT': root, 'Path': Path, 're': re, 'json': json}
    exec(compile(ast.Module(body=[resolver], type_ignores=[]), '<maintained dependency resolver>', 'exec'), scope)
    known = {p.resolve() for base in ['packages','web','apps','website'] for p in (root/base).rglob('*')
             if p.suffix in {'.ts','.tsx','.js','.mjs','.cjs','.json'} and p.is_file()
             and not set(p.parts)&{'node_modules','dist','build','third_party','.git'}}
    paths = [p for p in paths if not p.startswith(('server/','apikit/'))]
    changed = {(root/p).resolve() for p in paths}
    # Deleted/new unknown mappings, config and non-JS changes conservatively retain inventory.
    if not changed or not changed <= known or any(p.suffix not in {'.ts','.tsx','.js','.mjs','.cjs'} or p.name=='register.mjs' for p in changed):
        return candidates
    workspaces = {}
    for p in known:
        if p.name == 'package.json':
            name = json.loads(p.read_text()).get('name')
            if name: workspaces[name] = p.parent
    edges = {p: scope['dependencies'](p, known, workspaces) for p in known if p.suffix != '.json'}
    # Computed imports and unrecognized aliases cannot be safely resolved. Retain all
    # component tests when such a consumer exists (literal dynamic imports are resolved).
    for p in edges:
        source = p.read_text(errors='replace')
        if (re.search(r'(?:import|require)\s*\(\s*[^\s\x27\"]', source)
            or (re.search(r'[\x27\"](?:~|#[^\x27\"]*|@/)', source) and not p.is_relative_to(root/'web'))):
            relative = p.relative_to(root)
            owner = root.joinpath(*relative.parts[:2]) if relative.parts[0] in {'apps','packages'} else root/relative.parts[0]
            edges[p].update(q for q in known if q.is_relative_to(owner))
    impacted = set(changed)
    while True:
        more = {p for p,deps in edges.items() if deps & impacted} - impacted
        if not more: break
        impacted.update(more)
    if not any((root/f).resolve() in impacted for f in candidates): return candidates
    # Always retain security-boundary consumers in each affected component.
    selected = [f for f in candidates if (root/f).resolve() in impacted or re.search(r'auth|security|privacy|session|invitation|credential|reset', f, re.I)]
    return selected or candidates
