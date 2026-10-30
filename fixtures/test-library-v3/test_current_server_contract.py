"""Tie the offline fixture smoke to the canonical server contract in this tree."""
import json
from pathlib import Path
import re
import sys
import unittest

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
sys.path.insert(0, str(HERE))
import playback_smoke
import seed_state
from test_api_contract import FakeResponse, fixture_client, sent_query


class CurrentServerContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.api = json.loads((ROOT / "server/api/portico.openapi.json").read_text())

    def test_saved_requests_fit_the_generated_server_query_contract(self):
        operation = self.api["paths"]["/v1/saved-resources"]["get"]
        parameters = {p["name"]: p["schema"] for p in operation["parameters"]
                      if p["in"] == "query"}
        client = fixture_client([
            FakeResponse({"resources": [], "nextCursor": "next"}),
            FakeResponse({"resources": [], "nextCursor": ""}),
        ])
        seed_state.saved_resources(client, "collection")
        for request in client.opener.requests:
            query = sent_query(request)
            self.assertLessEqual(query.keys(), parameters.keys())
            limit = int(query["limit"][0])
            self.assertGreaterEqual(limit, parameters["limit"]["minimum"])
            self.assertLessEqual(limit, parameters["limit"]["maximum"])

    def test_playback_header_matches_the_current_server_protocol(self):
        source = (ROOT / "server/internal/playbackv1/errors.go").read_text()
        declaration = re.search(r"const\s+PlaybackProtocol\s*=\s*(\d+)", source)
        self.assertIsNotNone(declaration)
        self.assertEqual(playback_smoke.PLAYBACK_PROTOCOL["Portico-Playback"],
                         declaration.group(1))


if __name__ == "__main__":
    unittest.main()
