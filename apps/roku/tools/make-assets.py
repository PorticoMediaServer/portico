#!/usr/bin/env python3
"""Draws every picture the Roku app ships (apps/roku/images) from the shared design tokens.

Roku cannot blur, clip to a rounded shape or draw a gradient, so the backdrop, the scrims, the
focus ring with its glow, rounded fills and the icons are pictures made here. Run it from the
repository root after changing a token or adding an icon:

    python3 apps/roku/tools/make-assets.py

Needs python3 with cairo, PIL and librsvg (gi), and node 22+ (to read the icon registry).
"""
import io, json, os, random, subprocess, sys

import cairo
import gi
gi.require_version('Rsvg', '2.0')
from gi.repository import Rsvg
from PIL import Image

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', '..'))
OUT = os.path.join(ROOT, 'apps', 'roku', 'images')
W, H = 1920, 1080

# packages/design/src/index.ts `colors`
INK, ACCENT, ACCENT_DEEP, GREEN, AMBIENT, FOCUS = '#071018', '#409fd4', '#2f86bb', '#1e5b52', '#153749', '#d5efff'

# The art window: where a title's backdrop sits on a page (shared by Backdrop.brs).
ART = (576, 0, 1344, 756)


def rgb(h):
    h = h.lstrip('#')
    return tuple(int(h[i:i + 2], 16) / 255 for i in (0, 2, 4))


def surface(w, h):
    s = cairo.ImageSurface(cairo.FORMAT_ARGB32, w, h)
    return s, cairo.Context(s)


def to_pil(s):
    return Image.frombuffer('RGBA', (s.get_width(), s.get_height()), bytes(s.get_data()), 'raw', 'BGRa', s.get_stride(), 1).copy()


def save_png(img, name):
    img.save(os.path.join(OUT, name), optimize=True)


def glow(ctx, w, h, x, y, rx, ry, colour, stops):
    """An SVG objectBoundingBox radial gradient: centre and radii as fractions of the box."""
    ctx.save()
    ctx.translate(x * w, y * h)
    ctx.scale(rx * w, ry * h)
    g = cairo.RadialGradient(0, 0, 0, 0, 0, 1)
    for at, alpha in stops:
        g.add_color_stop_rgba(at, *rgb(colour), alpha)
    ctx.set_source(g)
    ctx.arc(0, 0, 1, 0, 6.2832)
    ctx.fill()
    ctx.restore()


def dither(img, amount=3, seed=7):
    """A little fixed noise so the slow gradients do not band on a television panel."""
    rnd = random.Random(seed)
    noise = Image.frombytes('L', (256, 256), bytes(rnd.randrange(256) for _ in range(256 * 256)))
    tile = Image.new('L', img.size)
    for ty in range(0, img.size[1], 256):
        for tx in range(0, img.size[0], 256):
            tile.paste(noise, (tx, ty))
    r, g, b = img.convert('RGB').split()
    out = []
    for ch in (r, g, b):
        out.append(Image.eval(Image.blend(ch, Image.composite(Image.eval(ch, lambda v: min(255, v + amount)), Image.eval(ch, lambda v: max(0, v - amount)), tile.point(lambda v: 255 if v > 127 else 0)), 0.5), lambda v: v))
    return Image.merge('RGB', out)


def standard_backdrop():
    """kit/artwork/Surfaces.tsx StandardBackdrop, the tablet/desktop/TV recipe: ink under three glows."""
    s, ctx = surface(W, H)
    ctx.set_source_rgb(*rgb(INK))
    ctx.paint()
    glow(ctx, W, H, 0.04, 0, 0.38, 0.5, AMBIENT, [(0, 0.75), (0.72, 0)])
    glow(ctx, W, H, 0.64, 1.04, 0.42, 0.58, GREEN, [(0, 0.6), (0.5, 0.18), (0.76, 0)])
    glow(ctx, W, H, 0.86, 0.04, 0.44, 0.66, ACCENT_DEEP, [(0, 0.46), (0.46, 0.15), (0.74, 0)])
    return to_pil(s)


def art_scrim(backdrop):
    """The backdrop itself, fading out to the right and up, laid over a title's art: the art
    then melts into the page with no edge, at the cost of one translucent layer."""
    x0, y0, w, h = ART
    piece = backdrop.crop((x0, y0, x0 + w, y0 + h)).convert('RGBA')
    alpha = Image.new('L', (w, h))
    px = alpha.load()

    def ease(t):
        t = max(0.0, min(1.0, t))
        return t * t * (3 - 2 * t)
    for yy in range(h):
        av = ease((yy / h - 0.42) / 0.58)          # bottom fade
        at = ease(1 - yy / (h * 0.10)) * 0.35      # a touch at the very top
        for xx in range(w):
            ah = 1 - ease(xx / (w * 0.62))         # left fade
            ar = ease((xx / w - 0.93) / 0.07) * 0.0
            a = 1 - (1 - ah) * (1 - av) * (1 - at) * (1 - ar)
            px[xx, yy] = int(a * 255 + 0.5)
    piece.putalpha(alpha)
    return piece


def rail_scrim(backdrop, w=200, solid=164, strength=0.88):
    """The backdrop's own left strip, laid over the page: a row that slides left goes dim under
    the rail, and where nothing is behind it the strip cannot be seen at all."""
    piece = backdrop.crop((0, 0, w, H)).convert('RGBA')
    alpha = Image.new('L', (w, H))
    px = alpha.load()
    for xx in range(w):
        t = 0.0 if xx < solid else (xx - solid) / (w - solid)
        a = int(strength * (1 - t * t * (3 - 2 * t)) * 255 + 0.5)
        for yy in range(H):
            px[xx, yy] = a
    piece.putalpha(alpha)
    return piece


def rounded(ctx, x, y, w, h, r):
    ctx.new_sub_path()
    ctx.arc(x + w - r, y + r, r, -1.5708, 0)
    ctx.arc(x + w - r, y + h - r, r, 0, 1.5708)
    ctx.arc(x + r, y + h - r, r, 1.5708, 3.1416)
    ctx.arc(x + r, y + r, r, 3.1416, 4.7124)
    ctx.close_path()


def nine(img, inset):
    """Wrap a picture as a Roku 9-patch: a 1 px frame whose black marks say which middle part stretches."""
    w, h = img.size
    out = Image.new('RGBA', (w + 2, h + 2), (0, 0, 0, 0))
    out.paste(img, (1, 1))
    px = out.load()
    for x in range(1 + inset, 1 + w - inset):
        px[x, 0] = (0, 0, 0, 255)
    for y in range(1 + inset, 1 + h - inset):
        px[0, y] = (0, 0, 0, 255)
    return out


def round_fill(r):
    size = r * 2 + 4
    s, ctx = surface(size, size)
    rounded(ctx, 0, 0, size, size, r)
    ctx.set_source_rgba(1, 1, 1, 1)
    ctx.fill()
    return nine(to_pil(s), r + 1)


def round_line(r, width=2):
    size = r * 2 + 4
    s, ctx = surface(size, size)
    rounded(ctx, width / 2, width / 2, size - width, size - width, r - width / 2)
    ctx.set_source_rgba(1, 1, 1, 1)
    ctx.set_line_width(width)
    ctx.stroke()
    return nine(to_pil(s), r + 1)


def focus_ring(r, ring=4, spread=30):
    """DESIGN.md platform notes: a pale ring and a soft glow. The glow is part of the picture.
    The ring sits `spread` px inside the picture's edge; Focus.brs outsets by the same number."""
    size = (spread + r) * 2 + 8
    s, ctx = surface(size, size)
    steps = 22
    for i in range(steps):
        t = i / (steps - 1)
        grow = spread * (1 - t)
        ctx.set_source_rgba(*rgb(ACCENT), 0.020 + 0.05 * t * t)
        rounded(ctx, spread - grow, spread - grow, size - 2 * (spread - grow), size - 2 * (spread - grow), r + grow)
        ctx.fill()
    # punch the inside out so the glow never tints the artwork, then draw the ring
    ctx.set_operator(cairo.OPERATOR_CLEAR)
    rounded(ctx, spread, spread, size - 2 * spread, size - 2 * spread, r)
    ctx.fill()
    ctx.set_operator(cairo.OPERATOR_OVER)
    ctx.set_source_rgba(*rgb(FOCUS), 1)
    ctx.set_line_width(ring)
    rounded(ctx, spread + ring / 2, spread + ring / 2, size - 2 * spread - ring, size - 2 * spread - ring, r - ring / 2)
    ctx.stroke()
    return nine(to_pil(s), spread + r + 1)


def card_mask(w, h, r):
    """An alpha mask the size of a card, for MaskGroup: the one way Roku rounds a picture's corners."""
    s, ctx = surface(w, h)
    rounded(ctx, 0, 0, w, h, r)
    ctx.set_source_rgba(1, 1, 1, 1)
    ctx.fill()
    return to_pil(s)


# packages/design/src/generated-cover.ts, in order: the eight tints of a cover made for a title with none.
COVER_TINTS = [('#2f86bb', '#1e5b52'), ('#b4642a', '#5e2a22'), ('#8f2e24', '#3f2150'), ('#2e6a6f', '#1f4f7a'),
               ('#55709a', '#2a3a66'), ('#3f8f6e', '#153749'), ('#6a58a8', '#26305f'), ('#a8892a', '#5a3a1a')]


def generated_cover(first, second, w=216, h=324):
    """kit/artwork/Generated.tsx: a dark ground mixed with the second tint, a glow of each tint."""
    def mix(a, b, t):
        return tuple(x + (y - x) * t for x, y in zip(rgb(a), rgb(b)))
    s, ctx = surface(w, h)
    ctx.set_source_rgb(*mix('#0a1620', second, 0.24))
    ctx.paint()
    glow(ctx, w, h, 1.0, 0.0, 1.1, 740 / 1500, first, [(0, 1), (0.68, 0)])
    glow(ctx, w, h, 0.14, 1180 / 1500, 0.96, 640 / 1500, second, [(0, 1), (0.74, 0)])
    return to_pil(s)


def generated_backdrop(first, second, w=672, h=378):
    """kit.css .fb .gen: the backdrop of a title page or hero for a title with no picture, drawn in
    the art window's own shape (the cover's portrait picture, stretched, showed its glow as a band)."""
    s, ctx = surface(w, h)
    ctx.set_source_rgb(*rgb('#0c1a25'))
    ctx.paint()
    glow(ctx, w, h, 0.85, 0.10, 0.70, 1.20, first, [(0, 1), (0.60, 0)])
    glow(ctx, w, h, 0.60, 1.10, 0.60, 0.90, second, [(0, 1), (0.65, 0)])
    return to_pil(s)


def corner_mask(r, colour):
    """Four corner wedges in the page colour, for rounding a picture Roku cannot clip."""
    size = r * 2 + 4
    s, ctx = surface(size, size)
    ctx.set_source_rgba(*rgb(colour), 1)
    ctx.paint()
    ctx.set_operator(cairo.OPERATOR_CLEAR)
    rounded(ctx, 0, 0, size, size, r)
    ctx.fill()
    return nine(to_pil(s), r + 1)


def shade(w, h, stops, horizontal=False):
    s, ctx = surface(w, h)
    g = cairo.LinearGradient(0, 0, w if horizontal else 0, 0 if horizontal else h)
    for at, colour, alpha in stops:
        g.add_color_stop_rgba(at, *rgb(colour), alpha)
    ctx.set_source(g)
    ctx.paint()
    return to_pil(s)


def face_shade(size=216):
    """The light and shade over a profile's round face (auth.html .face: a 140-degree gradient from
    the colour mixed with 18% white, through the colour at 55%, to 60% colour and 40% ink), as a
    layer over the tinted disc, so one picture serves every face colour."""
    s, ctx = surface(size, size)
    half = size / 2 * 1.409          # the gradient line of 140 degrees across a square
    dx, dy = 0.6428 * half, 0.7660 * half
    g = cairo.LinearGradient(size / 2 - dx, size / 2 - dy, size / 2 + dx, size / 2 + dy)
    g.add_color_stop_rgba(0, 1, 1, 1, 18 / 118)
    g.add_color_stop_rgba(0.55, 1, 1, 1, 0)
    g.add_color_stop_rgba(0.55, *rgb(INK), 0)
    g.add_color_stop_rgba(1, *rgb(INK), 0.4)
    ctx.arc(size / 2, size / 2, size / 2, 0, 6.2832)
    ctx.set_source(g)
    ctx.fill()
    return to_pil(s)


def svg_png(svg, w, h):
    handle = Rsvg.Handle.new_from_data(svg.encode())
    s, ctx = surface(w, h)
    rect = Rsvg.Rectangle()
    rect.x, rect.y, rect.width, rect.height = 0, 0, w, h
    handle.render_document(ctx, rect)
    return to_pil(s)


def icon_svg(entry):
    sw = entry.get('strokeWidth', 1.75)
    parts = []
    for e in entry['elements']:
        e = dict(e)
        kind = e.pop('type')
        fill = 'white' if e.pop('fill', None) else 'none'
        stroke = 'none' if e.pop('stroke', None) == 'none' else 'white'
        attrs = ' '.join('%s="%s"' % (k, v) for k, v in e.items())
        parts.append('<%s %s fill="%s" stroke="%s"/>' % (kind, attrs, fill, stroke))
    return ('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" stroke-width="%s" stroke-linecap="round" '
            'stroke-linejoin="round">%s</svg>') % (sw, ''.join(parts))


def read_ts(expr, imports):
    code = imports + '\nprocess.stdout.write(JSON.stringify(' + expr + '))'
    return json.loads(subprocess.run(['node', '--input-type=module', '-e', code], cwd=ROOT, check=True, capture_output=True, text=True).stdout)


# Icon name in the registry -> the sizes (px) the app draws it at.
# Entries are kept one per line in alphabetical order so that changes to different screens do not
# collide; a new entry goes in its alphabetical place.
ICONS = {
    'bell': [28],
    'book': [32],
    'bookmark': [28, 32],
    'browser': [28],
    'check': [28],
    'chevronDown': [20, 24, 28],
    'chevronUp': [20, 24, 28],
    'chevronUpDown': [24],
    'close': [28],
    'collection': [28],
    'dvr': [28],
    'film': [32],
    'folder': [32],
    'forward': [24, 28],
    'globe': [28],
    'grip': [26],
    'heart': [28],
    'heartFilled': [28],
    'home': [28, 32],
    'info': [28],
    'keyboard': [28],
    'list': [28],
    'listPlus': [28],
    'link': [28],
    'live': [32],
    'lock': [28],
    'lyrics': [28],
    'maximize': [28],
    'more': [28],
    'music': [28, 32],
    'next': [32],
    'pause': [40],
    'people': [28, 32],
    'phone': [28],
    'play': [28, 40],
    'plus': [28],
    'previous': [32],
    'profile': [28],
    'quality': [28],
    'refresh': [28],
    'repeat': [28, 32],
    'restart': [28],
    'search': [32],
    'server': [28, 32],
    'settings': [32],
    'shield': [28],
    'shuffle': [28, 32],
    'signOut': [28],
    'sliders': [28],
    'sort': [28],
    'star': [28],
    'starFilled': [28],
    'stop': [28],
    'subtitles': [28],
    'success': [28],
    'tablet': [28],
    'trash': [28],
    'tv': [32],
    'tvDevice': [28],
    'warning': [28, 32],
    'watched': [28],
}


def main():
    os.makedirs(OUT, exist_ok=True)
    backdrop = standard_backdrop()
    dither(backdrop).save(os.path.join(OUT, 'backdrop.jpg'), quality=92, subsampling=0)
    save_png(art_scrim(backdrop), 'art-scrim.png')
    save_png(rail_scrim(backdrop), 'rail-scrim.png')
    for r in (6, 10, 14, 20, 36):
        save_png(round_fill(r), 'round-%d.9.png' % r)
    for r in (10, 14, 20):
        save_png(round_line(r), 'line-%d.9.png' % r)
    for r in (14, 20):
        save_png(focus_ring(r + 4), 'focus-%d.9.png' % r)
    for w, h in ((214, 320), (400, 225), (214, 214), (384, 216), (200, 300), (200, 200)):
        save_png(card_mask(w, h, 14), 'mask-%dx%d.png' % (w, h))
    # the artist page's circle, cut from album art (tv-more frame 8)
    save_png(card_mask(412, 412, 206), 'mask-circle-412.png')
    save_png(shade(8, 256, [(0, '#000000', 0), (0.45, '#03080c', 0.35), (1, '#03080c', 0.92)]), 'card-shade.png')
    save_png(shade(8, 256, [(0, INK, 0), (1, INK, 1)]), 'fade-down.png')
    save_png(shade(8, 256, [(0, INK, 1), (1, INK, 0)]), 'fade-up.png')
    # the next episode's still on the Up next card (tv-player frame 5)
    save_png(card_mask(256, 144, 12), 'mask-256x144.png')
    # a fade in white, for tinting to whatever it lies on (the rail's panel)
    save_png(shade(8, 64, [(0, '#ffffff', 0), (1, '#ffffff', 1)]), 'fade-white-down.png')
    # the light and shade over a profile's face (Who's watching)
    save_png(face_shade(), 'face-shade.png')
    # a book's cover on an author's or a series' row (collections frame 44)
    save_png(card_mask(128, 128, 10), 'mask-128x128.png')

    for i, (first, second) in enumerate(COVER_TINTS):
        generated_cover(first, second).convert('RGB').save(os.path.join(OUT, 'cover-%d.jpg' % i), quality=88)
        dither(generated_backdrop(first, second)).save(os.path.join(OUT, 'tint-%d.jpg' % i), quality=90)

    icons = read_ts('icons', "import {icons} from './packages/design/src/icons.ts';")
    for name, sizes in ICONS.items():
        for size in sizes:
            save_png(svg_png(icon_svg(icons[name]), size, size), 'i-%s-%d.png' % (name, size))

    # Glyphs the registry does not hold yet (tv.md: "a microphone and a backspace glyph"), in its style,
    # and the two skip arrows without the registry's own seconds, since the app draws 10 and 30 itself.
    extra = {
        'backspace': {'elements': [{'type': 'path', 'd': 'M21 5H9l-7 7 7 7h12a1 1 0 0 0 1-1V6a1 1 0 0 0-1-1z'}, {'type': 'path', 'd': 'M17 9.5l-5 5M12 9.5l5 5'}]},
        'skip-back': {'elements': icons['back10']['elements'][:2]},
        'skip-forward': {'elements': icons['forward10']['elements'][:2]},
        'spinner': {'elements': [{'type': 'path', 'd': 'M12 3a9 9 0 1 0 9 9'}], 'strokeWidth': 2.25},
    }
    for name, size in (('backspace', 32), ('skip-back', 44), ('skip-forward', 44), ('spinner', 64)):
        save_png(svg_png(icon_svg(extra[name]), size, size), 'i-%s-%d.png' % (name, size))

    brand = read_ts('brandSymbol', "import {brandSymbol} from './apps/native/assets/brandSymbol.ts';")
    save_png(svg_png(brand, 33, 44), 'mark.png')  # already white
    wordmark = Image.open(os.path.join(ROOT, 'apps', 'native', 'assets', 'wordmark.png')).convert('RGBA')
    save_png(wordmark.resize((193, 44), Image.LANCZOS), 'wordmark.png')

    # Channel art for the Roku home screen and the splash: the backdrop with the wordmark.
    def poster(w, h, mark_w):
        base = backdrop.resize((w, h), Image.LANCZOS).convert('RGBA')
        mark = wordmark.resize((mark_w, round(mark_w * wordmark.size[1] / wordmark.size[0])), Image.LANCZOS)
        base.alpha_composite(mark, ((w - mark.size[0]) // 2, (h - mark.size[1]) // 2))
        return base.convert('RGB')
    poster(540, 405, 360).save(os.path.join(OUT, 'channel-icon-fhd.png'), optimize=True)
    poster(290, 218, 196).save(os.path.join(OUT, 'channel-icon-hd.png'), optimize=True)
    poster(1920, 1080, 480).save(os.path.join(OUT, 'splash-fhd.jpg'), quality=90)
    print('wrote', len(os.listdir(OUT)), 'files to', os.path.relpath(OUT, ROOT))


if __name__ == '__main__':
    sys.exit(main())
