"""Short-tier graph using the maintained legacy resolver, without its campaign exclusions."""
import json
from pathlib import Path
import re


def select(root, paths, candidates, diagnostics=None):
    def broad(reason):
        if diagnostics is not None: diagnostics['fallback']=reason
        return candidates
    known = {p.resolve() for base in ['packages','web','apps','website'] for p in (root/base).rglob('*')
             if p.suffix in {'.ts','.tsx','.js','.mjs','.cjs','.json'} and p.is_file()
             and not set(p.parts)&{'node_modules','dist','build','third_party','.git'}}
    paths = [p for p in paths if not p.startswith(('apikit/',))]
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
    edges = {p: dependencies(root, p, known, workspaces) for p in known if p.suffix != '.json'}
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


def dependencies(root, file, known, workspaces):
    result = set()
    # Include imports, requires, new URL(..., import.meta.url), and bounded harness module maps.
    for spec in re.findall(r"['\"]([^'\"\n]+)['\"]", file.read_text(errors="replace")):
        if spec.startswith("."):
            base = file.parent / spec
        elif spec.startswith("@core/"):
            base = root / "packages/client-core/src" / spec[6:]
        elif spec in {"@core", "@design", "@i18n"}:
            package = {"@core": "client-core", "@design": "design", "@i18n": "i18n"}[spec]
            base = root / "packages" / package / "src/index.ts"
        elif spec.startswith("@/") and file.is_relative_to(root / "web"):
            base = root / "web/src" / spec[2:]
        else:
            workspace = next((name for name in workspaces if spec == name or spec.startswith(name + "/")), None)
            if not workspace:
                continue
            directory = workspaces[workspace]
            exports = json.loads((directory / "package.json").read_text()).get("exports", {})
            key = "." + spec[len(workspace):]
            target = exports if isinstance(exports, str) and key == "." else exports.get(key) if isinstance(exports, dict) else None
            if isinstance(target, dict):
                target = target.get("import") or target.get("default") or target.get("types")
            base = directory / (target if isinstance(target, str) else spec[len(workspace):].lstrip("/") or "src/index")
        base = base.resolve()
        candidates = [base, *(Path(str(base) + ext) for ext in (".ts", ".tsx", ".js", ".mjs", ".cjs")),
                      *(base / ("index" + ext) for ext in (".ts", ".tsx", ".js", ".mjs"))]
        if base.suffix in {".js", ".jsx"}:
            candidates.extend([base.with_suffix(".ts"), base.with_suffix(".tsx")])
        result.update(candidate for candidate in candidates if candidate in known)
    return result
