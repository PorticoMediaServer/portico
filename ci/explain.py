"""Describe the existing selection; never decide which tests execute."""
from html import escape


def selection_reasons(paths, full, selected, go, components, js, go_fallbacks=()):
    reasons = {c: [] for c in selected}
    broad = []
    if full:
        broad.append('Explicit full inventory request')
        for c in selected: reasons[c].append({'reason': 'full inventory'})
    else:
        for path in paths:
            reached, _ = components([path])
            for component in reached:
                if component not in reasons: continue
                if path.startswith(component + '/'):
                    reason = 'direct component change'
                elif path.startswith('packages/'):
                    reason = 'shared package dependency fanout'
                elif component == 'packages' and path.startswith('web/'):
                    reason = 'web -> packages dependency'
                else:
                    reason = 'unmapped/root change -> conservative component inventory'
                    broad.append(path + ': ' + reason)
                reasons[component].append({'changed': path, 'reason': reason})
    if go:
        if go_fallbacks: go_reason='full Go inventory: '+'; '.join(go_fallbacks)
        elif full: go_reason = 'explicit full inventory'
        elif any(p.endswith(('/go.mod', '/go.sum')) for p in paths): go_reason = 'Go manifest changed: full Go inventory'
        elif not any(p.startswith(('server/', 'apikit/')) for p in paths): go_reason = 'cross-component/root or contracts change: full Go inventory'
        else: go_reason = 'owning Go packages, reverse imports/test imports and tier-required packages; unknown ownership or non-Go inputs retain full inventory'
    else: go_reason = 'no Go change selected'
    return {'full': full, 'broad_reasons': sorted(set(broad)), 'components': reasons, 'go': go_reason, 'js': js}


def render_summary(plan):
    why = plan['selection_diagnostics']
    lines = ['## CI selection\n', 'Changed paths:\n', '<pre>' + escape('\n'.join(p[:1000] for p in plan['paths'][:200]) or '(none; full request or empty diff)') + '</pre>\n']
    lines += ['Components and dependency reasons:\n']
    for component, reasons in why['components'].items():
        lines.append('<details><summary>' + escape(component) + '</summary><pre>' + escape('\n'.join(
            (r.get('changed', '') + ': ' if r.get('changed') else '') + r['reason'] for r in reasons[:100])) + '</pre></details>\n')
    lines += ['Go: ' + escape(why['go']) + '\n',
              f"Selected Go parents: {len(plan['inventory'])}; selected packages: {len(plan['packages'])}; tier exclusions: {len(plan['tier_exclusions'])}.\n",
              'Go shard estimates (test seconds; checkout, compilation, helper and transfer costs excluded):\n',
              '<pre>' + '\n'.join(f"{i}: {len(shard)} parents, {seconds:.1f}s" for i, (shard, seconds) in enumerate(zip(plan['shards'], plan['predicted_seconds']))) + '</pre>\n']
    for item in plan['js']:
        component = item['component']; detail = why['js'].get(component, {})
        estimates = ', '.join(f'{v:.1f}s' for v in item.get('predicted_seconds', [])) or 'unmeasured'
        lines.append(f"{escape(component)}: {len(item['files'])} files; estimates: {estimates}; " + escape(detail.get('fallback', detail.get('mode', 'dependency graph plus retained security consumers'))) + '\n')
        if detail.get('conservative_edges'):
            lines.append('<details><summary>Conservative resolver scopes</summary><pre>'+escape('\n'.join(e['consumer']+' -> '+e['scope']+': '+e['reason'] for e in detail['conservative_edges'][:100]))+'</pre></details>\n')
        if detail.get('dependency_edges'):
            lines.append('<details><summary>Dependency edges</summary><pre>' + escape('\n'.join(
                e['consumer'] + ' -> ' + e['dependency'] for e in detail['dependency_edges'][:100])) + '</pre></details>\n')
    if why['broad_reasons']:
        lines.append('Broad coverage reasons:<pre>' + escape('\n'.join(why['broad_reasons'])) + '</pre>\n')
    result=[]; size=0
    for block in lines:
        length=len(block.encode())
        if size+length>800000:
            result.append('Additional diagnostics are retained in the full ci-plan.json selection artifact.\n')
            break
        result.append(block);size+=length
    return '\n'.join(result) + '\n'
