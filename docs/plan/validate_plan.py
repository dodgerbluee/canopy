#!/usr/bin/env python3
"""Validate planning artifacts only; no application dependencies or network."""
from pathlib import Path
import json
import re

ROOT = Path(__file__).resolve().parent
plan = json.loads((ROOT / 'task-graph.json').read_text())
nodes, groups = plan['nodes'], plan['groups']
assert not (nodes.keys() & groups.keys()), 'A group cannot dispatch as a task'
visited, active, order = set(), set(), []

def walk(name):
    assert name in nodes, f'Unknown or grouping dependency: {name}'
    assert name not in active, f'Dependency cycle: {name}'
    if name in visited:
        return
    active.add(name)
    for dep in nodes[name]['dependsOn']:
        walk(dep)
    active.remove(name)
    visited.add(name)
    order.append(name)

for name in nodes:
    walk(name)
for group, children in groups.items():
    assert children and all(child in nodes for child in children), group

def closure(names):
    result = set()
    def add(name):
        if name not in result:
            result.add(name)
            for dep in nodes[name]['dependsOn']:
                add(dep)
    for name in names:
        add(name)
    return result

def validate_release_gates(gates):
    previous = set()
    for release in ('R1', 'R2', 'R3', 'R4'):
        current = closure(gates[release])
        assert previous <= current, f'{release} omits prior release acceptance'
        previous = current

validate_release_gates(plan['gates'])
# Meaningful negative fixture: intermediate release must not omit R1 restore.
invalid_gates = dict(plan['gates'], R2=['T28'])
try:
    validate_release_gates(invalid_gates)
except AssertionError:
    pass
else:
    raise AssertionError('Gate validator accepted missing R1 portability')

r1 = closure(plan['gates']['R1'])
assert all(nodes[name]['release'] == 'R1' for name in r1), 'R1 depends on later release'
assert {'T23', 'T33', 'T38.a'} <= closure(['T38.final']), 'Final restore omits care/AI'
assert {'T01.care','T02.care'} <= closure(['T21'])
assert {'T01.ai','T02.ai'} <= closure(['T29'])
assert {'T35','T36','T37','T38.final','T39','T40'} <= closure(['T41'])
assert closure(['T41']) == set(nodes), 'Release gate leaves orphan task'

broken = []
for path in [ROOT.parent.parent / 'README.md', *ROOT.glob('*.md')]:
    for href in re.findall(r'\]\(([^)]+)\)', path.read_text()):
        if not href.startswith(('http:', 'https:', '#')):
            if not (path.parent / href.split('#')[0]).exists():
                broken.append(f'{path.name}: {href}')
assert not broken, f'Broken links: {broken}'
print(f'PASS: {len(nodes)} executable nodes; {len(groups)} non-executable groups; DAG acyclic')
print(f'PASS: R1 dependency closure {len(r1)} nodes, no care/AI prerequisite')
print('PASS: cumulative release gates including negative missing-portability fixture')
print('PASS: staged schema freeze, final restore, final review dependencies and local links')
print('No application implementation or runtime tests asserted by this check.')
