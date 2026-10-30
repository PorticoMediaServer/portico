#!/usr/bin/env python3
"""Contract tests for the fixture API calls, without a server or network.

The fixtures talk to the real v1 API, so their request shapes are part of this
directory's contract. These tests drive the production helpers
(``seed_state.saved_resources``, ``seed_state.Client``,
``seed_state.Observer``, ``playback_smoke``) through a fake HTTP opener and
assert only what the current server accepts:

* ``GET /v1/saved-resources`` reads its continuation from the top-level
  ``nextCursor`` field, accepts no query key outside cursor/limit/kind/pinned/
  shape, and refuses a limit outside 1..100 with 400 ``invalid_cursor``
  (server ``httpapi.savedPageQuery`` and ``catalog.SavedResources``).
* A saved-resources cursor is signed over kind, shape, pinned and limit, so a
  page must repeat the same filters and limit.
* Playback start/stop declare ``Portico-Playback: 2``; the server answers 400
  ``client_update_required`` for a client that does not (server
  ``playbackv1.PlaybackProtocol`` and ``requireObservationProtocol``).
* Playback reporting is ``POST .../observer`` for the epoch, then
  ``PUT .../observation`` with epoch/seq/generation/positionMs/monotonicMs/
  playClockMs/seekSerial/rate (server ``playbackv1.ClaimObserver`` and
  ``Observe``). There is no playback ``/timeline`` route.

Run with
``python3 -m unittest discover -s fixtures/test-library -p test_api_contract.py``.
Set ``PORTICO_OPENAPI`` to a current ``server/api/portico.openapi.json`` to
also check the bodies against the registered schema.
"""
from __future__ import annotations

import ast
import json
import os
import sys
import unittest
import urllib.parse
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import playback_smoke  # noqa: E402
import seed_state  # noqa: E402

EPOCH = "A" * 22
LEASE = "2026-10-29T12:00:00Z"


class FakeResponse:
    """The subset of an HTTP response the fixture clients read."""

    def __init__(self, document=None, status=200):
        self.status = status
        self.headers = {"Content-Type": "application/json"}
        self._payload = b"" if document is None else json.dumps(document).encode()

    def read(self, limit=-1):
        return self._payload if limit is None or limit < 0 else self._payload[:limit]

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


class FakeOpener:
    """Queues responses and records the exact requests the helpers sent."""

    def __init__(self, responses):
        self.responses = list(responses)
        self.requests = []

    def open(self, request, timeout=None):
        self.requests.append(request)
        if not self.responses:
            raise AssertionError("unexpected request: " + request.full_url)
        return self.responses.pop(0)


def fixture_client(responses):
    client = seed_state.Client("http://fixture.invalid",
                               installation="fixture-installation", token="fixture-token")
    client.opener = FakeOpener(responses)
    return client


def sent_query(request):
    return urllib.parse.parse_qs(urllib.parse.urlsplit(request.full_url).query)


def sent_path(request):
    return urllib.parse.urlsplit(request.full_url).path


def sent_header(request, name):
    for key, value in request.header_items():
        if key.lower() == name.lower():
            return value
    return None


def sent_body(request):
    return json.loads(request.data) if request.data else None


class FakeClock:
    """A deterministic device clock; the tests never sleep."""

    def __init__(self):
        self.value = 0.0

    def __call__(self):
        return self.value

    def advance(self, seconds):
        self.value += seconds


def grant(epoch=EPOCH):
    return FakeResponse({"epoch": epoch, "everyMs": 1000, "leaseUntil": LEASE})


def ack(seq):
    return FakeResponse({"acceptedSeq": seq, "leaseUntil": LEASE})


class SavedResourcesContractTests(unittest.TestCase):
    def test_pages_with_the_accepted_limit_and_the_top_level_cursor(self):
        client = fixture_client([
            FakeResponse({"resources": [{"id": "first"}], "nextCursor": "opaque+/="}),
            FakeResponse({"resources": [{"id": "second"}], "nextCursor": ""}),
        ])

        resources = seed_state.saved_resources(client, "collection")

        self.assertEqual([resource["id"] for resource in resources], ["first", "second"])
        self.assertEqual(len(client.opener.requests), 2)
        first, second = client.opener.requests
        self.assertEqual(urllib.parse.urlsplit(first.full_url).path, "/v1/saved-resources")
        self.assertEqual(sent_query(first), {"kind": ["collection"], "limit": ["100"]})
        self.assertEqual(sent_query(second), {"kind": ["collection"], "limit": ["100"],
                                              "cursor": ["opaque+/="]})
        for request in client.opener.requests:
            self.assertLessEqual(int(sent_query(request)["limit"][0]), 100)

    def test_an_exhausted_first_page_sends_one_request(self):
        client = fixture_client([FakeResponse({"resources": [{"id": "only"}],
                                               "nextCursor": ""})])

        resources = seed_state.saved_resources(client, "view")

        self.assertEqual([resource["id"] for resource in resources], ["only"])
        self.assertEqual(len(client.opener.requests), 1)

    def test_a_repeated_cursor_ends_paging(self):
        client = fixture_client([
            FakeResponse({"resources": [{"id": "first"}], "nextCursor": "same"}),
            FakeResponse({"resources": [{"id": "second"}], "nextCursor": "same"}),
        ])

        resources = seed_state.saved_resources(client, "view")

        self.assertEqual([resource["id"] for resource in resources], ["first", "second"])
        self.assertEqual(len(client.opener.requests), 2)

    def test_maximum_bounds_the_collected_resources(self):
        client = fixture_client([FakeResponse({"resources": [{"id": "first"},
                                                              {"id": "second"}],
                                               "nextCursor": "more"})])

        resources = seed_state.saved_resources(client, "collection", maximum=1)

        self.assertEqual([resource["id"] for resource in resources], ["first"])
        self.assertEqual(len(client.opener.requests), 1)

    def test_a_repeated_cursor_is_reported_not_silent(self):
        client = fixture_client([
            FakeResponse({"resources": [{"id": "first"}], "nextCursor": "same"}),
            FakeResponse({"resources": [{"id": "second"}], "nextCursor": "same"}),
        ])
        report = {}

        seed_state.saved_resources(client, "view", report=report)

        self.assertIn({"step": "saved-resources", "kind": "view", "code": "cursor_repeated",
                       "collected": 2}, report["warnings"])

    def test_a_truncated_window_is_reported_not_silent(self):
        client = fixture_client([FakeResponse({"resources": [{"id": "first"},
                                                              {"id": "second"}],
                                               "nextCursor": "more"})])
        report = {}

        resources = seed_state.saved_resources(client, "collection", maximum=1, report=report)

        self.assertEqual([resource["id"] for resource in resources], ["first"])
        self.assertIn({"step": "saved-resources", "kind": "collection",
                       "code": "cursor_truncated", "maximum": 1, "collected": 2},
                      report["warnings"])


class PlaybackProtocolHeaderTests(unittest.TestCase):
    def test_request_declares_the_observation_protocol(self):
        client = fixture_client([FakeResponse({"presentation": {"url": "/media/1"}},
                                               status=201)])

        page = playback_smoke.request(
            client, "POST", "/v1/playback/sessions", {"itemId": "item-1"},
            {"Idempotency-Key": "start-key-1"}, ok=(201,))

        self.assertEqual(page, {"presentation": {"url": "/media/1"}})
        request = client.opener.requests[0]
        self.assertEqual(sent_header(request, "Portico-Playback"), "2")
        self.assertEqual(sent_header(request, "Idempotency-Key"), "start-key-1")

    def test_start_helper_returns_once_the_presentation_exists(self):
        client = fixture_client([FakeResponse({"presentation": {"url": "/media/1"}},
                                               status=201)])

        session = playback_smoke.start(client, "item-1")

        self.assertEqual(session["presentation"]["url"], "/media/1")
        self.assertEqual(sent_header(client.opener.requests[0], "Portico-Playback"), "2")

    def test_stop_helper_declares_the_observation_protocol(self):
        client = fixture_client([FakeResponse(status=204)])

        playback_smoke.stop(client, {"id": "session-1"})

        request = client.opener.requests[0]
        self.assertEqual(request.get_method(), "DELETE")
        self.assertEqual(sent_header(request, "Portico-Playback"), "2")


class SeedHistoryFlowTests(unittest.TestCase):
    def test_history_claims_observes_and_stops_without_a_timeline_route(self):
        client = fixture_client([
            FakeResponse(status=204),
            FakeResponse({"id": "session-1",
                          "presentation": {"url": "/media/1", "generation": 4}}, status=201),
            grant(),
            ack(1),
            FakeResponse(status=204),
        ])
        report = {"created": {"history": []}}

        created = seed_state.start_history(client, "item-1", 0.25, report, duration_ms=100000)

        self.assertTrue(created)
        self.assertEqual([sent_path(request) for request in client.opener.requests], [
            "/v1/me/devices/current/capabilities",
            "/v1/playback/sessions",
            "/v1/playback/sessions/session-1/observer",
            "/v1/playback/sessions/session-1/observation",
            "/v1/playback/sessions/session-1",
        ])
        start, stop = client.opener.requests[1], client.opener.requests[4]
        self.assertEqual(start.get_method(), "POST")
        self.assertEqual(stop.get_method(), "DELETE")
        self.assertEqual(sent_body(stop), {"positionMs": 25000})
        self.assertEqual(sent_body(client.opener.requests[2]), {"generation": 4})
        self.assertEqual(sent_body(client.opener.requests[3])["state"], "paused")
        self.assertEqual(sent_body(client.opener.requests[3])["positionMs"], 25000)
        self.assertEqual(report["created"]["history"], ["item-1"])
        self.assertNotIn("/timeline", "".join(
            sent_path(request) for request in client.opener.requests))


class PlaybackObserverTests(unittest.TestCase):
    def test_claim_posts_only_the_generation_and_requires_the_grant(self):
        client = fixture_client([grant()])

        result = seed_state.claim_observer(client, "session-1", 7)

        self.assertEqual(result, {"epoch": EPOCH, "everyMs": 1000, "leaseUntil": LEASE})
        request = client.opener.requests[0]
        self.assertEqual(request.get_method(), "POST")
        self.assertEqual(sent_path(request), "/v1/playback/sessions/session-1/observer")
        self.assertEqual(sent_body(request), {"generation": 7})
        self.assertEqual(sent_header(request, "Portico-Playback"), "2")

    def test_a_malformed_grant_is_refused_not_accepted(self):
        for document, field in (
                ({"epoch": "short", "everyMs": 1000, "leaseUntil": LEASE}, "epoch"),
                ({"epoch": EPOCH, "everyMs": 0, "leaseUntil": LEASE}, "everyMs"),
                ({"epoch": EPOCH, "everyMs": 1000, "leaseUntil": "tomorrow"}, "leaseUntil"),
                ({"epoch": EPOCH, "everyMs": 1000}, "leaseUntil")):
            with self.subTest(field=field):
                client = fixture_client([FakeResponse(document)])
                with self.assertRaises(seed_state.Refusal) as caught:
                    seed_state.claim_observer(client, "session-1", 7)
                self.assertEqual(caught.exception.code, "observer_grant_shape")
                self.assertEqual(caught.exception.detail, field)

    def test_samples_follow_the_production_observer_sequence(self):
        clock = FakeClock()
        client = fixture_client([grant(), ack(1), ack(2)])
        observer = seed_state.Observer(client, "session-1", 7, now=clock)

        observer.observe("playing", 0)
        clock.advance(1.5)
        observer.observe("paused", 2500, seeked=True)

        claim_request, first, second = client.opener.requests
        self.assertEqual(sent_path(claim_request), "/v1/playback/sessions/session-1/observer")
        self.assertEqual(sent_body(claim_request), {"generation": 7})
        self.assertEqual(sent_body(first), {
            "epoch": EPOCH, "state": "playing", "seq": 1, "generation": 7,
            "positionMs": 0, "monotonicMs": 0, "playClockMs": 0,
            "seekSerial": 0, "rate": 1.0})
        self.assertEqual(sent_body(second), {
            "epoch": EPOCH, "state": "paused", "seq": 2, "generation": 7,
            "positionMs": 2500, "monotonicMs": 1500, "playClockMs": 1500,
            "seekSerial": 1, "rate": 1.0})
        self.assertEqual(sent_path(first), "/v1/playback/sessions/session-1/observation")
        self.assertEqual(sent_path(second), "/v1/playback/sessions/session-1/observation")
        for request in client.opener.requests:
            self.assertEqual(sent_header(request, "Portico-Playback"), "2")

    def test_the_play_clock_counts_only_time_actually_playing(self):
        clock = FakeClock()
        client = fixture_client([grant(), ack(1), ack(2), ack(3)])
        observer = seed_state.Observer(client, "session-1", 7, now=clock)

        observer.observe("playing", 0)
        clock.advance(2.0)
        observer.observe("paused", 2000)
        clock.advance(3.0)
        observer.observe("paused", 2000)

        _, playing, paused, still_paused = client.opener.requests
        self.assertEqual(sent_body(playing)["playClockMs"], 0)
        self.assertEqual(sent_body(paused)["playClockMs"], 2000)
        self.assertEqual(sent_body(paused)["monotonicMs"], 2000)
        self.assertEqual(sent_body(still_paused)["playClockMs"], 2000)
        self.assertEqual(sent_body(still_paused)["monotonicMs"], 5000)

    def test_an_epoch_change_is_surfaced_not_retried(self):
        client = fixture_client([
            grant(EPOCH),
            FakeResponse({"error": {"code": "observation_epoch_changed",
                                    "message": "Another player now reports this playback."}}, 409),
        ])
        observer = seed_state.Observer(client, "session-1", 7, now=FakeClock())

        with self.assertRaises(seed_state.Refusal) as caught:
            observer.observe("playing", 1000)

        self.assertEqual(caught.exception.code, "observation_epoch_changed")
        self.assertEqual(caught.exception.status, 409)
        self.assertEqual(len(client.opener.requests), 2)

    def test_an_ack_without_accepted_seq_is_refused_not_accepted(self):
        client = fixture_client([grant(), FakeResponse({"leaseUntil": LEASE})])
        observer = seed_state.Observer(client, "session-1", 7, now=FakeClock())

        with self.assertRaises(seed_state.Refusal) as caught:
            observer.observe("playing", 0)

        self.assertEqual(caught.exception.code, "observation_ack_shape")
        self.assertEqual(caught.exception.detail, "acceptedSeq")

    def test_an_ended_sample_stops_further_reporting(self):
        client = fixture_client([grant(), ack(1)])
        observer = seed_state.Observer(client, "session-1", 7, now=FakeClock())

        observer.observe("ended", 5000)

        with self.assertRaises(seed_state.Refusal) as caught:
            observer.observe("playing", 0)
        self.assertEqual(caught.exception.code, "observer_ended")
        self.assertEqual(len(client.opener.requests), 2)

    def test_the_fixture_modules_name_no_playback_timeline_route(self):
        for name in ("seed_state.py", "playback_smoke.py"):
            source = Path(__file__).with_name(name).read_text(encoding="utf-8")
            tree = ast.parse(source)
            for node in ast.walk(tree):
                if isinstance(node, ast.Constant) and isinstance(node.value, str):
                    self.assertNotIn("/timeline", node.value, name)

    def test_bodies_match_the_current_openapi(self):
        default = Path(__file__).resolve().parents[2] / "server/api/portico.openapi.json"
        spec_path = default
        self.assertTrue(spec_path.is_file(), "canonical server OpenAPI must be present")
        spec = json.loads(spec_path.read_text(encoding="utf-8"))
        observation = spec.get("paths", {}).get("/v1/playback/sessions/{id}/observation", {})
        claim = spec.get("paths", {}).get("/v1/playback/sessions/{id}/observer", {})
        self.assertIn("put", observation, "current observation route must remain registered")
        self.assertIn("post", claim, "current observer route must remain registered")
        body_schema = observation["put"]["requestBody"]["content"]["application/json"]["schema"]
        self.assertLessEqual(set(body_schema["required"]), set(body_schema["properties"]))
        claim_schema = claim["post"]["requestBody"]["content"]["application/json"]["schema"]
        self.assertEqual(claim_schema["required"], ["generation"])

        client = fixture_client([grant(), ack(1), ack(1)])
        observer = seed_state.Observer(client, "session-1", 7, now=FakeClock())
        observer.observe("paused", 2500)
        sample = sent_body(client.opener.requests[1])
        self.assertEqual(set(sample), set(body_schema["required"]))
        self.assertLessEqual(set(sample), set(body_schema["properties"]))
        self.assertEqual(set(sent_body(client.opener.requests[0])), {"generation"})


if __name__ == "__main__":
    unittest.main()
