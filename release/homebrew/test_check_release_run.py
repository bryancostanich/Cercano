import copy
import unittest
from unittest.mock import patch
import check_release_run as c


class ProvenanceTest(unittest.TestCase):
    def setUp(self):
        self.release = {'tag_name': 'v0.20.3', 'draft': False}
        self.run = {'head_sha': 'source', 'path': '.github/workflows/release-macos.yml',
                    'event': 'workflow_dispatch', 'status': 'completed',
                    'head_repository': {'full_name': 'bryancostanich/Cercano'}}
        self.jobs = {'total_count': 2, 'jobs': [
            {'name': 'Build, sign, notarize and verify', 'conclusion': 'success'},
            {'name': 'Publish to GitHub Releases', 'conclusion': 'success'}]}

    def execute(self):
        with patch.object(c, 'api', side_effect=[self.release, {'sha': 'source'}, self.run, self.jobs]):
            return c.check('0.20.3', '123')

    def test_valid_release(self):
        self.assertEqual(self.execute(), 'source')

    def test_failed_tap_does_not_require_republishing(self):
        self.run['conclusion'] = 'failure'
        self.jobs['jobs'].append({'name': 'Update Homebrew tap', 'conclusion': 'failure'})
        self.jobs['total_count'] = 3
        self.assertEqual(self.execute(), 'source')

    def test_unpublished_release_refused(self):
        self.release['draft'] = True
        with self.assertRaises(c.Failure): self.execute()

    def test_wrong_tag_refused(self):
        self.release['tag_name'] = 'v0.20.0'
        with self.assertRaises(c.Failure): self.execute()

    def test_wrong_source_refused(self):
        self.run['head_sha'] = 'other'
        with self.assertRaises(c.Failure): self.execute()

    def test_untrusted_workflow_refused(self):
        for field, value in [('path', '.github/workflows/ci.yml'), ('event', 'pull_request'),
                             ('status', 'in_progress'), ('head_repository', {'full_name': 'other/repo'})]:
            original = copy.deepcopy(self.run)
            self.run[field] = value
            with self.subTest(field=field), self.assertRaises(c.Failure): self.execute()
            self.run = original

    def test_incomplete_jobs_refused(self):
        for index in [0, 1]:
            self.jobs['jobs'][index]['conclusion'] = 'failure'
            with self.assertRaises(c.Failure): self.execute()
            self.jobs['jobs'][index]['conclusion'] = 'success'

    def test_invalid_run_id_never_requests_network(self):
        with patch.object(c, 'api') as api:
            with self.assertRaises(c.Failure): c.check('0.20.3', '../other')
            api.assert_not_called()


if __name__ == '__main__': unittest.main()
