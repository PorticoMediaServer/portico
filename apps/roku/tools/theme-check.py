#!/usr/bin/env python3
"""Every frame number, colour and type role the app reads must be in source/Theme.brs.
BrightScript only finds a missing one when the screen opens (and then the app stops), so this
reads the sources instead. Run from apps/roku: python3 tools/theme-check.py"""
import glob, re, sys
theme = open('source/Theme.brs').read()
def body(name):
    i = theme.index('function %s()' % name)
    return theme[i:theme.index('end function', i)]
frames = body('frames')
frames = frames[frames.index('return {') + 8:]
tree = {}
for key, inner in re.findall(r'^\s{8}(\w+): \{(.*?)\}\s*,?\s*$', frames, re.M | re.S):
    tree[key] = set(re.findall(r'(\w+):', inner))
for key in re.findall(r'(\w+): -?\d+', re.sub(r'\{.*?\}', '', frames, flags=re.S)):
    tree.setdefault(key, None)
colours = set(re.findall(r'(\w+): (?:"0x|\[")', body('colours')))
roles = set(re.findall(r'(\w+): \[\d+, \d+, \d+\]', body('typeRoles')))
bad = []
# A name given twice in one table: BrightScript keeps the later one without a word.
for table in ('frames', 'colours', 'typeRoles'):
    text = re.sub(r"'[^\n]*", '', body(table))          # comments out
    text = re.sub(r'"[^"]*"', '""', text)                # and what is inside strings
    seen = [set()]
    for part in re.finditer(r'[{}]|(\w+):', text[text.index('return {') + 7:]):
        if part.group(0) == '{': seen.append(set())
        elif part.group(0) == '}': seen.pop()
        elif part.group(1) in seen[-1]: bad.append('%s() names %s twice' % (table, part.group(1)))
        else: seen[-1].add(part.group(1))
for path in sorted(glob.glob('components/**/*.brs', recursive=True) + glob.glob('source/*.brs')):
    src = open(path).read()
    uses = [('', a, b) for a, b in re.findall(r'frames\(\)\.(\w+)(?:\.(\w+))?', src)]
    # a name set from frames() holds a frame: `m.` names for the whole file, plain names for their own sub
    parts = re.split(r'(?m)^(?=(?:sub|function) )', src)
    kept = dict(re.findall(r'\b(m\.\w+) = frames\(\)(?:\.(\w+))?\s*$', src, re.M))
    for part in parts:
        held = dict(kept)
        held.update(re.findall(r'^\s*(\w+) = frames\(\)(?:\.(\w+))?\s*$', part, re.M))
        for name, frame in held.items():
            for a, b in re.findall(r'(?<![\w.])' + re.escape(name) + r'\.(\w+)(?:\.(\w+))?', part):
                uses.append((frame, a, b))
    for frame, a, b in uses:
        if frame:
            a, b = frame, a
        if a not in tree:
            bad.append('%s: frames().%s' % (path, a))
        elif b and tree[a] is not None and b not in tree[a] and not re.search(r'\.' + b + r' = ', src) and not b[0].isupper() and b not in ('count', 'toStr'):
            bad.append('%s: frames().%s.%s' % (path, a, b))
    for c in set(re.findall(r'\bc\.(\w+)', src)) | set(re.findall(r'colours\(\)\.(\w+)', src)):
        if c not in colours and re.search(r'\bc = colours\(\)', src) or (c not in colours and 'colours().' + c in src):
            bad.append('%s: colours().%s' % (path, c))
    for r in set(re.findall(r'(?:setType\([^,\n]+, |fontFor\()"(\w+)"', src)):
        if r not in roles:
            bad.append('%s: type role %s' % (path, r))
for line in sorted(set(bad)):
    print(line if 'twice' in line else 'MISSING ' + line)
print('theme check: %d missing or given twice' % len(set(bad)))
sys.exit(1 if bad else 0)
