"""Offline live source wiring: per-channel HLS names and rolling-window flags."""
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import MagicMock, call, patch

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import live
import serve_live

ROOT = Path("/srv/portico-test-library-v3/Live")
FFMPEG = "/opt/portico-ffmpeg/bin/ffmpeg"


class FakeEncoder:
    """Stand-in encoder that records terminate/kill/wait instead of running."""

    def __init__(self):
        self.terminated = False
        self.killed = False
        self.waits = []

    def terminate(self):
        self.terminated = True

    def kill(self):
        self.killed = True

    def wait(self, timeout=None):
        self.waits.append(timeout)
        return 0

    def poll(self):
        return None


def run_main():
    with patch.object(sys, "argv", ["serve_live.py", str(ROOT)]):
        serve_live.main()


class LiveSourceTests(unittest.TestCase):
    def test_each_channel_has_its_own_rolling_playlist_and_segments(self):
        commands = [serve_live.encoder_command(FFMPEG, ROOT, channel)
                    for channel in range(1, serve_live.CHANNELS + 1)]
        self.assertEqual(serve_live.CHANNELS, 3)
        self.assertEqual([Path(command[-1]).name for command in commands],
                         ["channel-1.m3u8", "channel-2.m3u8", "channel-3.m3u8"])
        segment_patterns = []
        for command in commands:
            self.assertEqual(command[command.index("-c") + 1], "copy")
            self.assertIn("-re", command)
            self.assertEqual(command[command.index("-stream_loop") + 1], "-1")
            self.assertEqual(command[command.index("-i") + 1], str(ROOT / "source.mp4"))
            self.assertEqual(command[command.index("-hls_time") + 1], "2")
            self.assertEqual(command[command.index("-hls_list_size") + 1], "30")
            self.assertEqual(command[command.index("-hls_delete_threshold") + 1], "15")
            flags = command[command.index("-hls_flags") + 1]
            self.assertEqual(set(flags.split("+")), {"delete_segments", "temp_file"})
            segment_patterns.append(command[command.index("-hls_segment_filename") + 1])
        # One shared source is fine; playlists and segments must never collide.
        self.assertEqual([Path(pattern).name for pattern in segment_patterns],
                         ["channel-1-%06d.ts", "channel-2-%06d.ts", "channel-3-%06d.ts"])
        self.assertEqual(len(set(segment_patterns)), 3)

    def test_generated_playlist_urls_match_the_served_routes(self):
        self.assertEqual(len(live.CHANNELS), serve_live.CHANNELS)
        for channel in range(1, serve_live.CHANNELS + 1):
            url = live.channel_url(channel)
            self.assertEqual(url, serve_live.playlist_url(serve_live.PORT, channel))
            self.assertEqual(Path(url).name,
                             Path(serve_live.encoder_command(FFMPEG, ROOT, channel)[-1]).name)

    def test_generated_m3u_lists_one_playlist_per_channel(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            media = root / "Live" / "source.mp4"
            media.parent.mkdir()
            media.write_bytes(b"placeholder media")
            # Artwork and encoding are substituted; playlist naming runs intact.
            with patch.object(live, "art"):
                live.generate(root, "unused-ffmpeg", full=False)
            playlist = (root / "Live" / "channels.m3u").read_text()
        urls = [line for line in playlist.splitlines() if line.startswith("http://")]
        self.assertEqual(urls, [live.channel_url(channel) for channel in (1, 2, 3)])

    def test_http_bind_failure_stops_every_encoder(self):
        encoders = [FakeEncoder() for _ in range(serve_live.CHANNELS)]
        with patch.object(serve_live.subprocess, "Popen", side_effect=encoders), \
             patch.object(serve_live.http.server, "ThreadingHTTPServer",
                          side_effect=OSError("address in use")):
            with self.assertRaises(OSError):
                run_main()
        self.assertEqual(len(encoders), serve_live.CHANNELS)
        for encoder in encoders:
            self.assertTrue(encoder.terminated)
            self.assertEqual(encoder.waits, [10])

    def test_sink_bind_failure_closes_http_server_and_stops_every_encoder(self):
        encoders = [FakeEncoder() for _ in range(serve_live.CHANNELS)]
        http_server = MagicMock()
        with patch.object(serve_live.subprocess, "Popen", side_effect=encoders), \
             patch.object(serve_live.http.server, "ThreadingHTTPServer",
                          side_effect=[http_server, OSError("webhook address in use")]):
            with self.assertRaises(OSError):
                run_main()
        # The first listening socket must not leak when the sink cannot bind.
        http_server.server_close.assert_called_once_with()
        for encoder in encoders:
            self.assertTrue(encoder.terminated)
            self.assertEqual(encoder.waits, [10])

    def test_encoder_spawn_failure_stops_already_started_children(self):
        started = FakeEncoder()
        with patch.object(serve_live.subprocess, "Popen",
                          side_effect=[started, OSError("ffmpeg missing")]), \
             patch.object(serve_live.http.server, "ThreadingHTTPServer") as http_server:
            with self.assertRaises(OSError):
                run_main()
        http_server.assert_not_called()
        self.assertTrue(started.terminated)
        self.assertEqual(started.waits, [10])

    def test_stop_encoders_kills_child_that_outlives_the_bounded_wait(self):
        stubborn, exited = FakeEncoder(), FakeEncoder()
        stubborn.wait = MagicMock(side_effect=[subprocess.TimeoutExpired("ffmpeg", 10), 0])
        serve_live.stop_encoders([stubborn, exited])
        self.assertTrue(stubborn.terminated)
        self.assertTrue(stubborn.killed)
        self.assertEqual(stubborn.wait.call_args_list, [call(timeout=10), call()])
        self.assertTrue(exited.terminated)
        self.assertEqual(exited.waits, [10])


if __name__ == "__main__":
    unittest.main()
