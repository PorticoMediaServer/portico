"""Short-tier graph using the maintained legacy resolver, without its campaign exclusions."""
import ast
import json
from pathlib import Path
import re


def select(root, paths, candidates, diagnostics=None):
    def broad(reason):
        if diagnostics is not None: diagnostics['fallback']=reason
        return candidates
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
        reason=('no JS ownership for changed paths' if not changed else 'unknown or deleted path' if not changed<=known else 'configuration, loader or non-JS change')
        return broad(reason)
    workspaces = {}
    for p in known:
        if p.name == 'package.json':
            name = json.loads(p.read_text()).get('name')
            if name: workspaces[name] = p.parent
    edges = {p: scope['dependencies'](p, known, workspaces) for p in known if p.suffix != '.json'}
    # A computed import retains its entire owner. Index those owners once,
    # rather than walking every known path's parents for every such consumer.
    owner_files = {}
    for p in known:
        relative = p.relative_to(root)
        owner = relative.parts[:2] if relative.parts[0] in {'apps', 'packages'} else relative.parts[:1]
        owner_files.setdefault(owner, set()).add(p)
    # Computed imports and unrecognized aliases cannot be safely resolved. Retain all
    # component tests when such a consumer exists (literal dynamic imports are resolved).
    for p in edges:
        source = p.read_text(errors='replace')
        if (re.search(r'(?:import|require)\s*\(\s*[^\s\x27\"]', source)
            or (re.search(r'[\x27\"](?:~|#[^\x27\"]*|@/)', source) and not p.is_relative_to(root/'web'))):
            relative = p.relative_to(root)
            owner_parts = relative.parts[:2] if relative.parts[0] in {'apps', 'packages'} else relative.parts[:1]
            owner = root.joinpath(*owner_parts)
            edges[p].update(owner_files[owner_parts])
            if diagnostics is not None:
                diagnostics.setdefault('conservative_edges',[]).append({'consumer':str(relative),'scope':str(owner.relative_to(root)),'reason':'computed import or unresolved alias'})
    impacted = set(changed); parents={}
    while True:
        more = {p for p,deps in edges.items() if deps & impacted} - impacted
        if not more: break
        if diagnostics is not None:
            parents.update({p:sorted(edges[p]&impacted)[0] for p in more})
        impacted.update(more)
    if not any((root/f).resolve() in impacted for f in candidates): return broad('no matching test consumer in dependency graph')
    # Always retain security-boundary consumers in each affected component.
    selected = [f for f in candidates if (root/f).resolve() in impacted or re.search(r'auth|security|privacy|session|invitation|credential|reset', f, re.I)]
    if diagnostics is not None:
        diagnostics['dependency_edges']=[{'consumer':str(p.relative_to(root)),'dependency':str(dep.relative_to(root))} for p,dep in sorted(parents.items())]
        diagnostics['retained_boundary_files']=[f for f in selected if (root/f).resolve() not in impacted]
    return selected or broad('empty affected selection')
