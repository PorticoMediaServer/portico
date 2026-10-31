"""No AI authors, co-authors or generated-by lines in the commits a change brings (docs/ENGINEERING-RULES.md).

Checks only identities and trailer-style lines, never subject or body prose, so a commit that
merely mentions a tool by name is not refused. Runs in the planner job, which has full history.
"""
import json
import os
import re
import subprocess
import sys
from pathlib import Path

from plan import ROOT, base_head

AI = r'(?:claude|anthropic|openai|chatgpt|codex|copilot|gemini|deepseek|\bgpt[-\s]?\d|\bllm\b|\bai assistant\b)'
IDENTITY = re.compile(AI, re.IGNORECASE)
TRAILER = re.compile(r'^\s*(?:co-authored-by|signed-off-by|generated-by|assisted-by|ai-assisted-by|authored-by)\s*:.*' + AI, re.IGNORECASE | re.MULTILINE)
GENERATED = re.compile(r'^\s*(?:\W\s*)?generated (?:with|by)\b.*' + AI, re.IGNORECASE | re.MULTILINE)
SEPARATOR = '\x1e'


def problems(records):
    """records: (sha, author, committer, message). Returns one line per offending commit."""
    out = []
    for sha, author, committer, message in records:
        why = []
        if IDENTITY.search(author):
            why.append('author')
        if IDENTITY.search(committer):
            why.append('committer')
        if TRAILER.search(message):
            why.append('trailer')
        if GENERATED.search(message):
            why.append('generated-by line')
        if why:
            out.append(f'{sha[:12]}: AI {", ".join(why)}')
    return out


def records(base, head):
    raw = subprocess.run(['git', 'log', '--no-merges', f'--format=%H%x1f%an <%ae>%x1f%cn <%ce>%x1f%B{SEPARATOR}', f'{base}..{head}'],
                         cwd=ROOT, check=True, capture_output=True, text=True).stdout
    for entry in raw.split(SEPARATOR):
        entry = entry.strip('\n')
        if entry:
            sha, author, committer, message = entry.split('\x1f', 3)
            yield sha, author, committer, message


def main():
    event_path = os.environ.get('GITHUB_EVENT_PATH')
    event = json.loads(Path(event_path).read_text()) if event_path else {}
    if not os.environ.get('GITHUB_EVENT_NAME') in ('pull_request', 'merge_group') and not os.environ.get('CI_BASE'):
        print('commit bylines: no commit range (full or manual run without a base)')
        return 0
    base, head = base_head(event, os.environ)
    found = problems(list(records(base, head)))
    for line in found:
        print(line + '. No AI authors, co-authors or generated-by lines; see docs/ENGINEERING-RULES.md.')
    if found:
        return 1
    print('commit bylines clean')
    return 0


if __name__ == '__main__':
    sys.exit(main())
