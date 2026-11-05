import importlib.util
from pathlib import Path
import unittest
from urllib.parse import parse_qs, urlsplit

spec = importlib.util.spec_from_file_location('fixture_server', Path(__file__).with_name('e2e-server.py'))
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)

class BookContent(unittest.TestCase):
    def test_recognition_book_files_exhaust_short_pages_and_keep_ids(self):
        class API:
            paths = []
            def get(self, path):
                self.paths.append(path)
                query = parse_qs(urlsplit(path).query)
                assert query['view'] == ['book'] and query['entityId'] == ['book']
                assert query['section'] == ['files']
                continuation = query.get('cursor') == ['next']
                return {'scope': {'libraryId': 'lib', 'entityId': 'book'},
                        'sections': [{'id': 'files', 'totalCount': 2,
                                      'entries': [{'id': 'part-2' if continuation else 'part-1'}],
                                      'nextCursor': '' if continuation else 'next'}]}
        api = API()
        document = fixture.book_files(api, 'lib', 'book')
        self.assertEqual([row['id'] for row in document['files']], ['part-1', 'part-2'])
        self.assertEqual(document['nextCursor'], '')
        self.assertEqual(len(api.paths), 2)
        self.assertTrue(all('/v1/libraries/lib/content?' in p for p in api.paths))

if __name__ == '__main__':
    unittest.main()
