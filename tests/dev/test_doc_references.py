"""Documentation points at files and tests that exist.

ADRs name the code and tests that check each decision. This keeps those
names from going stale when code moves or a test is renamed.
"""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[2]

LINK = re.compile(r'\]\(([^)\s]+)\)')
CODE = re.compile(r'`([^`\n]+)`')
TOP = ('api/', 'calls/', 'cmd/', 'deployments/', 'docs/', 'internal/', 'migrations/', 'scripts/', 'tests/', 'web/')
GO_TEST = re.compile(r'^Test[A-Z]\w*$')
PY_TEST = re.compile(r'^test_\w+$')


def documents():
    return sorted([*ROOT.glob('*.md'), *ROOT.glob('docs/**/*.md')])


def broken_links(path, text):
    """Relative links of a Markdown file whose target does not exist."""
    found = []
    for target in LINK.findall(text):
        if re.match(r'[a-z][a-z+.-]*:', target) or target.startswith('#'):
            continue
        if not (path.parent / target.split('#', 1)[0]).exists():
            found.append(target)
    return found


def test_names():
    """Names of every Go and Python test function in the repository."""
    names = set()
    for source in [*ROOT.glob('internal/**/*_test.go'), *ROOT.glob('tests/**/*_test.go'), *ROOT.glob('cmd/**/*_test.go')]:
        names.update(re.findall(r'^func (Test\w+)\(', source.read_text(), re.M))
    for source in ROOT.glob('tests/**/test_*.py'):
        names.update(re.findall(r'^\s+def (test_\w+)\(', source.read_text(), re.M))
    return names


def missing_references(text, names, root=ROOT):
    """Code spans naming a repository path or test that does not exist."""
    found = []
    for span in CODE.findall(text):
        span = span.strip()
        if GO_TEST.match(span) or PY_TEST.match(span):
            if span not in names:
                found.append(span)
        elif span.startswith(TOP) and ' ' not in span and not any(c in span for c in '<>*{}$'):
            # `internal/media/preview.Worker` names a symbol of a package.
            path = re.sub(r'\.[A-Z]\w*$', '', span.split(':', 1)[0].rstrip('/'))
            if not (root / path).exists():
                found.append(span)
    return found


class DocumentReferences(unittest.TestCase):
    def test_relative_links_resolve(self):
        broken = {str(p.relative_to(ROOT)): b for p in documents() if (b := broken_links(p, p.read_text()))}
        self.assertEqual(broken, {})

    def test_adrs_name_existing_code_and_tests(self):
        names = test_names()
        self.assertIn('TestMutualTLSOverTCP', names)
        missing = {}
        for path in sorted(ROOT.glob('docs/adr/*.md')):
            if found := missing_references(path.read_text(), names):
                missing[path.name] = found
        self.assertEqual(missing, {})

    def test_stale_names_are_reported(self):
        text = ('`internal/shared/rpctls` `TestMutualTLSOverTCP` `test_repeat_preserves_credentials_and_keys` '
                '`internal/media/preview.Worker` `internal/gone` `TestRenamedAway` `test_renamed_away` `secrets/rpc/<service>` `go test ./...`')
        names = {'TestMutualTLSOverTCP', 'test_repeat_preserves_credentials_and_keys'}
        self.assertEqual(missing_references(text, names), ['internal/gone', 'TestRenamedAway', 'test_renamed_away'])
        page = ROOT / 'docs/adr/x.md'
        self.assertEqual(broken_links(page, '[a](0001-internal-mtls.md) [b](../gone.md#x) [c](https://e.org) [d](#top)'),
                         ['../gone.md#x'])


if __name__ == '__main__':
    unittest.main()
