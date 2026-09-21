import unittest
from check_coverage import summarize


class CoverageGateTests(unittest.TestCase):
    def test_reject_empty_and_invalid(self):
        for text in ('', 'mode: atomic\n', 'mode: invalid\n', 'mode: count\nbad\n'):
            with self.assertRaises(ValueError):
                summarize(text)

    def test_exact_and_merge_duplicate_blocks(self):
        result = summarize('mode: atomic\na.go:1.1,2.2 2 0\na.go:1.1,2.2 2 1\n')
        self.assertEqual(result['total_statements'], 2)
        self.assertTrue(result['passed'])

    def test_rounding_is_not_success(self):
        result = summarize('mode: atomic\na.go:1.1,2.2 9999 1\nb.go:1.1,2.2 1 0\n')
        self.assertEqual(f"{result['percent']:.1f}", '100.0')
        self.assertFalse(result['passed'])

    def test_inconsistent_duplicate(self):
        with self.assertRaises(ValueError):
            summarize('mode: set\na.go:1.1,2.2 1 1\na.go:1.1,2.2 2 1\n')


if __name__ == '__main__':
    unittest.main()
