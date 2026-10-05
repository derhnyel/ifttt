"""Keep user documentation links valid when reference sections move."""
import re
import unittest
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]


def prose(path):
    return re.sub(r'^```.*?^```[^\n]*$', '', path.read_text(), flags=re.M | re.S)


def anchors(path):
    headings = re.findall(r'^#{1,6}\s+(.+)$', prose(path), flags=re.M)
    return {re.sub(r'[^\w\- ]', '', heading.lower()).replace(' ', '-')
            for heading in headings}


class DocumentationTests(unittest.TestCase):
    def test_internal_links_resolve_to_files_and_sections(self):
        files = [ROOT / name for name in ('README.md', 'CONTRIBUTING.md',
                                         'AGENTS.md', 'vscode-extension/README.md')]
        files.extend(sorted((ROOT / 'docs').glob('*.md')))
        for source in files:
            for target in re.findall(r'\[[^\]\n]+\]\(([^)\n]+)\)', prose(source)):
                with self.subTest(source=source.relative_to(ROOT), target=target):
                    url = urlsplit(target)
                    if url.scheme:
                        if url.netloc != 'github.com':
                            continue
                        repo = '/derhnyel/ifttt'
                        if url.path == repo:
                            destination = ROOT / 'README.md'
                        elif url.path.startswith(repo + '/blob/main/'):
                            destination = ROOT / unquote(url.path[len(repo + '/blob/main/'):])
                        else:
                            continue  # Issues, releases and external repositories.
                    else:
                        destination = source.parent / unquote(url.path) if url.path else source
                    self.assertTrue(destination.is_file(), f'{destination} does not exist')
                    if url.fragment:
                        self.assertIn(unquote(url.fragment), anchors(destination))


if __name__ == '__main__':
    unittest.main()
