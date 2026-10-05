import unittest
import benchmark


class BuildProvenanceTests(unittest.TestCase):
    def test_cgo_setting_comes_from_executable_metadata(self):
        for setting in ('0', '1'):
            with self.subTest(setting=setting):
                info = f'iflint: go1.26.8\n\tbuild\tCGO_ENABLED={setting}\n\tbuild\tGOOS=darwin\n'
                self.assertEqual(benchmark.compiled_cgo(info), setting)
        with self.assertRaises(ValueError):
            benchmark.compiled_cgo('iflint: go1.26.8\n')
