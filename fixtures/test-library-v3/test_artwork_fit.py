"""Generated artwork keeps its title inside the frame (FX-P3): every drawn line sits above the
footer on posters, square covers, wide stills and logos."""
from pathlib import Path
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import common

FONT = '/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf'
LONG = 'The Salt Road · Tamsin Okafor and the Long Way Home Across the Marshes'


class ArtworkFitTests(unittest.TestCase):
    def setUp(self):
        from PIL import Image, ImageDraw
        self.draw = ImageDraw.Draw(Image.new('RGB', (10, 10)))

    def test_title_fits_every_frame_the_library_draws(self):
        for w, h in [(600, 900), (600, 600), (400, 225), (1920, 1080), (800, 310), (300, 300)]:
            for title in ['Static Garden', 'The Salt Road · Tamsin Okafor', LONG]:
                size = max(18, int(w * .067))
                top, bottom = h * .75, h * .95 - max(12, int(w * .023)) * .6
                font, lines = common.fit_title(self.draw, title, FONT, size, w * .85, bottom - top)
                with self.subTest(frame=(w, h), title=title):
                    self.assertGreaterEqual(len(lines), 1)
                    self.assertLessEqual(top + len(lines) * font.size * 1.15, bottom + 1)
                    for line in lines:
                        self.assertLessEqual(self.draw.textlength(line, font=font), w * .85 + 1)

    def test_a_square_cover_keeps_a_short_title_whole_and_ends_a_long_one_on_an_ellipsis(self):
        _, short = common.fit_title(self.draw, 'The Salt Road · Tamsin Okafor', FONT, 40, 510, 600 * .2 - 8)
        self.assertEqual(' '.join(short), 'The Salt Road · Tamsin Okafor')
        _, long = common.fit_title(self.draw, LONG * 2, FONT, 40, 510, 600 * .2 - 8)
        self.assertTrue(long[-1].endswith('…'))

    def test_art_writes_square_and_wide_files(self):
        with tempfile.TemporaryDirectory() as directory:
            for name, size in [('square.png', (600, 600)), ('wide.jpg', (400, 225)), ('logo.png', (800, 310))]:
                path = Path(directory) / name
                common.art(path, LONG, size=size, seed=3, transparent=name == 'logo.png')
                self.assertTrue(path.stat().st_size > 0)


if __name__ == '__main__':
    unittest.main()
