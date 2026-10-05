import tempfile
import unittest
from pathlib import Path
import benchmark
import benchmark_scaling


class ScalingFixtureTests(unittest.TestCase):
    def test_reverse_fixtures_leave_referrer_unchanged_and_ignore_bad_file(self):
        with tempfile.TemporaryDirectory() as directory:
            for kind in ('delete', 'label'):
                root = Path(directory) / kind
                self.assertEqual(benchmark_scaling.fixture(root, kind, 3, 2), 1)
                diff = benchmark.git(root, 'diff', 'HEAD').decode()
                self.assertNotIn('diff --git a/source_0.go', diff)
                self.assertIn('diff --git a/target_0.go', diff)
                self.assertEqual(diff.count('diff --git'), 4)
                self.assertNotIn('ignored', benchmark.git(root, 'ls-files').decode())
                if kind == 'delete':
                    self.assertIn('+++ /dev/null', diff)
                else:
                    self.assertIn('+// LINT.IfChange(renamed)', diff)

    def test_local_fixture_changes_pair_and_requested_ordinary_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / 'local'
            self.assertEqual(benchmark_scaling.fixture(root, 'local', 3, 2), 0)
            diff = benchmark.git(root, 'diff', 'HEAD').decode()
            self.assertEqual(diff.count('diff --git'), 5)
            self.assertIn('diff --git a/source_0.go', diff)
            self.assertIn('diff --git a/target_0.go', diff)


if __name__ == '__main__':
    unittest.main()
