import os
from pathlib import Path
from tempfile import TemporaryDirectory
from types import SimpleNamespace
import unittest
from guard import check


class GuardTest(unittest.TestCase):
    def test_only_marked_roots_invoke_where(self):
        with TemporaryDirectory() as t:
            root = Path(t)
            watched = root / 'tools' / 'sample'
            cfg = {'cli':'binary', 'watch':[{'root':str(watched), 'kind':'skill', 'name':'sample'}]}
            calls = []
            def run(args, **kwargs):
                calls.append(args)
                return SimpleNamespace(returncode=1)
            def edit(file):
                check({'tool_name':'Write','cwd':t,'tool_input':{'file_path':file}},cfg,run)
            with self.assertRaises(ValueError):edit(os.path.join('tools','sample','new'))
            edit(os.path.join('tools','sample2','new'))
            self.assertEqual(calls, [['binary','library','where','skill','sample', str(watched/'new')]])
            cfg['watch']=[{'root':str(watched),'blocked':True}]
            with self.assertRaises(ValueError):edit(str(watched/'new'))
            self.assertEqual(len(calls),1)


if __name__ == '__main__': unittest.main()
