"""Claude Code PreToolUse adapter for explicitly enrolled local maintenance paths.

Input: Claude's Edit / Write / NotebookEdit event on stdin. The adjacent JSON
contains watched locations, not the authoritative maintenance destination. Ask
Magpie's `library where` for that on each guarded edit. Never log tool content.
"""
import json
import os
from pathlib import Path
import subprocess
import sys


def inside(child, parent):
    try:
        return os.path.commonpath([child, parent]) == parent
    except ValueError:  # other drive
        return False


def forms(p):
    return {os.path.normcase(os.path.abspath(p)), os.path.normcase(os.path.realpath(p))}


def check(event, config, run=subprocess.run):
    if event.get('tool_name') not in {'Edit', 'Write', 'NotebookEdit'}:
        return
    data = event['tool_input']
    filename = data.get('file_path') or data.get('notebook_path')
    if not filename:
        raise ValueError('未提供目标路径')
    if not os.path.isabs(filename):
        filename = os.path.join(event['cwd'], filename)
    seen = set()
    for entry in config['watch']:
        if not any(inside(c, r) for c in forms(filename) for r in forms(entry['root'])):
            continue
        if entry.get('blocked'):
            raise ValueError('这是自动同步存储，不能作为维护目录。')
        key = (entry['kind'], entry['name'])
        if key in seen:
            continue
        seen.add(key)
        result = run([config['cli'], 'library', 'where', *key, filename], stdin=subprocess.DEVNULL,
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=15, check=False)
        if result.returncode:
            raise ValueError('目标不是已登记的维护路径。请先运行 library where ' + ' '.join(key))


def main():
    try:
        config = json.loads(Path(__file__).with_name('watch.json').read_bytes())
        check(json.load(sys.stdin), config)
    except (KeyError, OSError, ValueError, subprocess.SubprocessError) as exc:
        # Exit 2 blocks a Claude PreToolUse action. Do not echo configuration,
        # tool input, subprocess output or exception text containing JSON data.
        print(str(exc) if type(exc) is ValueError and str(exc).startswith(('目标不是', '这是自动', '未提供')) else '无法验证维护位置；未允许本次写入。', file=sys.stderr)
        return 2
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
