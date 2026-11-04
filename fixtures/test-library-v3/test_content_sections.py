"""The fixture walks exact identities through every canonical section page."""
import copy
import unittest
from urllib.parse import parse_qs, urlsplit
from verify import content_section


class SectionClient:
    def __init__(self, pages):
        self.pages, self.paths = iter(pages), []

    def read(self, path):
        self.paths.append(path)
        return next(self.pages)


def page(ids, cursor="", total=3):
    return {"scope": {"libraryId": "lib", "entityId": "book"},
            "sections": [{"id": "files", "totalCount": total,
                          "entries": [{"id": i} for i in ids], "nextCursor": cursor}]}


class ContentSections(unittest.TestCase):
    def test_short_page_with_cursor_retains_all_ids_and_hierarchy(self):
        client = SectionClient([page(["part-1"], "opaque"), page(["part-2", "part-3"])])
        self.assertEqual([x["id"] for x in content_section(client, "lib", "book", "book", "files")],
                         ["part-1", "part-2", "part-3"])
        self.assertEqual(parse_qs(urlsplit(client.paths[1]).query)["cursor"], ["opaque"])
        for path in client.paths:
            query = parse_qs(urlsplit(path).query)
            self.assertEqual(query["entityId"], ["book"])
            self.assertEqual(query["section"], ["files"])

    def test_incomplete_duplicate_or_changed_hierarchy_fails(self):
        wrong = copy.deepcopy(page(["part-2", "part-3"]))
        wrong["scope"]["entityId"] = "another-book"
        for pages in [[page(["part-1"])], [page(["part-1"], "next"), page(["part-1", "part-3"])],
                      [page(["part-1"], "next"), wrong]]:
            with self.subTest(pages=pages), self.assertRaises(ValueError):
                content_section(SectionClient(pages), "lib", "book", "book", "files")


if __name__ == "__main__":
    unittest.main()
