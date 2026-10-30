#!/usr/bin/env python3
"""Writes the launch assets from the wordmark SVG and the standard backdrop (DESIGN "The picture").

  python3 -I apps/native/scripts/make-boot-assets.py path/to/wordmark.svg

The wordmark is work/ui-mockup-2026-10-21/assets/wordmark.svg (the same file as the web's
web-next/src/shell/wordmark.svg). Needs pycairo for the Apple PDFs and PNGs.

Android: res/drawable/portico_wordmark.xml (176dp) and boot_wordmark.xml (the 288dp splash icon).
Apple: BootWordmark (vector PDFs: 176pt iPhone, 224pt iPad, 640pt Apple TV), BootBand (the phone's
560pt top band) and BootBackdrop (the full backdrop: iPad, Apple TV 1x and 2x). The glows are the
same fractions of the drawn area as ColourBackdrop (src-next/kit/artwork/Surfaces.tsx) and the
Android boot view (PorticoBoot.kt), so an image stretched to fill its view matches React exactly.
"""
import json
import os
import re
import sys
import xml.etree.ElementTree as ET

import cairo

HERE = os.path.dirname(os.path.abspath(__file__))
APP = os.path.dirname(HERE)
NS = '{http://www.w3.org/2000/svg}'
INK = (0x07, 0x10, 0x18)
ACCENT_DEEP, GENERATED_GREEN, AMBIENT = (0x2F, 0x86, 0xBB), (0x1E, 0x5B, 0x52), (0x15, 0x37, 0x49)
# (x, y, rx, ry, colour, stops), first glow on top.
BAND = [(0.96, -0.06, 0.9, 0.54, ACCENT_DEEP, [(0, 0.5), (0.48, 0.15), (0.76, 0)]),
        (-0.08, 0.3, 0.84, 0.44, GENERATED_GREEN, [(0, 0.5), (0.52, 0.13), (0.78, 0)]),
        (0.3, 0, 0.7, 0.4, AMBIENT, [(0, 0.8), (0.72, 0)])]
SIDE = [(0.86, 0.04, 0.44, 0.66, ACCENT_DEEP, [(0, 0.46), (0.46, 0.15), (0.74, 0)]),
        (0.64, 1.04, 0.42, 0.58, GENERATED_GREEN, [(0, 0.6), (0.5, 0.18), (0.76, 0)]),
        (0.04, 0, 0.38, 0.5, AMBIENT, [(0, 0.75), (0.72, 0)])]


def wordmark(path):
    svg = ET.parse(path).getroot()
    box = tuple(map(float, svg.get('viewBox').split()))
    paths = []
    for p in svg.iter(NS + 'path'):
        transform = None
        if p.get('transform'):
            m = re.fullmatch(r'translate\(([-\d.]+),([-\d.]+)\) scale\(([-\d.]+),([-\d.]+)\)', p.get('transform'))
            if not m:
                raise SystemExit('unsupported transform: ' + p.get('transform'))
            transform = tuple(map(float, m.groups()))
        paths.append((p.get('d'), transform))
    return box, paths


def trace(ctx, d):
    """Absolute M, L, H, V, Q and Z only (what the wordmark uses)."""
    tokens = re.findall(r'[MLHVQZmlhvqzCcSsTtAa]|-?\d*\.?\d+(?:e[-+]?\d+)?', d)
    i, command, x, y = 0, None, 0.0, 0.0
    def number():
        nonlocal i
        i += 1
        return float(tokens[i - 1])
    while i < len(tokens):
        if tokens[i].isalpha():
            command = tokens[i]
            i += 1
            if command not in 'MLHVQZ':
                raise SystemExit('unsupported path command: ' + command)
            if command == 'Z':
                ctx.close_path()
                continue
        if command == 'M':
            x, y = number(), number(); ctx.move_to(x, y); command = 'L'
        elif command == 'L':
            x, y = number(), number(); ctx.line_to(x, y)
        elif command == 'H':
            x = number(); ctx.line_to(x, y)
        elif command == 'V':
            y = number(); ctx.line_to(x, y)
        elif command == 'Q':
            qx, qy, ex, ey = number(), number(), number(), number()
            ctx.curve_to(x + 2 / 3 * (qx - x), y + 2 / 3 * (qy - y), ex + 2 / 3 * (qx - ex), ey + 2 / 3 * (qy - ey), ex, ey)
            x, y = ex, ey


def wordmark_pdf(box, paths, width, out):
    vx, vy, vw, vh = box
    scale = width / vw
    surface = cairo.PDFSurface(out, width, vh * scale)
    ctx = cairo.Context(surface)
    ctx.scale(scale, scale)
    ctx.translate(-vx, -vy)
    ctx.set_source_rgb(1, 1, 1)
    for d, transform in paths:
        ctx.save()
        if transform:
            tx, ty, sx, sy = transform
            ctx.translate(tx, ty); ctx.scale(sx, sy)
        trace(ctx, d)
        ctx.restore()
        ctx.fill()
    surface.finish()


def backdrop_png(glows, width, height, out):
    surface = cairo.ImageSurface(cairo.FORMAT_RGB24, width, height)
    ctx = cairo.Context(surface)
    ctx.set_source_rgb(*(c / 255 for c in INK))
    ctx.paint()
    for x, y, rx, ry, colour, stops in reversed(glows):
        gradient = cairo.RadialGradient(0, 0, 0, 0, 0, 1)
        for offset, alpha in stops:
            gradient.add_color_stop_rgba(offset, *(c / 255 for c in colour), alpha)
        matrix = cairo.Matrix(xx=rx * width, yy=ry * height, x0=x * width, y0=y * height)
        matrix.invert()
        gradient.set_matrix(matrix)
        ctx.set_source(gradient)
        ctx.paint()
    surface.write_to_png(out)


def imageset(directory, images, vector=False):
    os.makedirs(directory, exist_ok=True)
    contents = {'images': images, 'info': {'author': 'xcode', 'version': 1}}
    if vector:
        contents['properties'] = {'preserves-vector-representation': True}
    with open(os.path.join(directory, 'Contents.json'), 'w') as f:
        json.dump(contents, f, indent=2)
        f.write('\n')


def android_vectors(box, paths):
    vx, vy, vw, vh = box
    def body(indent):
        lines = []
        for d, transform in paths:
            if transform:
                tx, ty, sx, sy = transform
                lines += [f'{indent}<group android:translateX="{tx:g}" android:translateY="{ty:g}" android:scaleX="{sx:g}" android:scaleY="{sy:g}">',
                          f'{indent}    <path android:fillColor="#FFFFFF" android:pathData="{d}" />', f'{indent}</group>']
            else:
                lines.append(f'{indent}<path android:fillColor="#FFFFFF" android:pathData="{d}" />')
        return '\n'.join(lines)
    drawable = os.path.join(APP, 'android/app/src/main/res/drawable')
    head = '<?xml version="1.0" encoding="utf-8"?>\n<!-- Written by apps/native/scripts/make-boot-assets.py from the wordmark SVG (aspect 1774:405). -->\n'
    with open(os.path.join(drawable, 'portico_wordmark.xml'), 'w') as f:
        f.write(head + f'<vector xmlns:android="http://schemas.android.com/apk/res/android"\n    android:width="176dp" android:height="{176 * vh / vw:.2f}dp"\n'
                f'    android:viewportWidth="{vw:g}" android:viewportHeight="{vh:g}">\n    <group android:translateX="{-vx:g}" android:translateY="{-vy:g}">\n'
                f'{body("        ")}\n    </group>\n</vector>\n')
    # The Android 12 splash icon: the 288dp canvas, the wordmark 176dp wide in its centre.
    scale = 176 / vw
    with open(os.path.join(drawable, 'boot_wordmark.xml'), 'w') as f:
        f.write(head + '<!-- The splash icon: 288dp canvas, wordmark 176dp wide, centred (its corners stay inside the 96dp circle). -->\n'
                '<vector xmlns:android="http://schemas.android.com/apk/res/android"\n    android:width="288dp" android:height="288dp"\n'
                '    android:viewportWidth="288" android:viewportHeight="288">\n'
                f'    <group android:translateX="{(288 - 176) / 2:.4f}" android:translateY="{(288 - vh * scale) / 2:.4f}" android:scaleX="{scale:.8f}" android:scaleY="{scale:.8f}">\n'
                f'        <group android:translateX="{-vx:g}" android:translateY="{-vy:g}">\n{body("            ")}\n        </group>\n    </group>\n</vector>\n')


def apple(box, paths):
    ios = os.path.join(APP, 'ios/Portico/Images.xcassets')
    tv = os.path.join(APP, 'tvos/Portico/Images.xcassets')
    # The wordmark's own size is the launch screen's size: no width constraint, one per idiom.
    directory = os.path.join(ios, 'BootWordmark.imageset')
    imageset(directory, [{'filename': 'BootWordmark-iphone.pdf', 'idiom': 'iphone'}, {'filename': 'BootWordmark-ipad.pdf', 'idiom': 'ipad'}], vector=True)
    wordmark_pdf(box, paths, 176, os.path.join(directory, 'BootWordmark-iphone.pdf'))
    wordmark_pdf(box, paths, 224, os.path.join(directory, 'BootWordmark-ipad.pdf'))
    directory = os.path.join(tv, 'BootWordmark.imageset')
    imageset(directory, [{'filename': 'BootWordmark-tv.pdf', 'idiom': 'tv'}], vector=True)
    wordmark_pdf(box, paths, 640, os.path.join(directory, 'BootWordmark-tv.pdf'))
    # The phone's top band (560pt tall at any width) and the full backdrop, each for its idiom only.
    directory = os.path.join(ios, 'BootBand.imageset')
    imageset(directory, [{'filename': 'BootBand.png', 'idiom': 'iphone', 'scale': '3x'}])
    backdrop_png(BAND, 1290, 1680, os.path.join(directory, 'BootBand.png'))
    directory = os.path.join(ios, 'BootBackdrop.imageset')
    imageset(directory, [{'filename': 'BootBackdrop.png', 'idiom': 'ipad', 'scale': '2x'}])
    backdrop_png(SIDE, 2048, 2732, os.path.join(directory, 'BootBackdrop.png'))
    directory = os.path.join(tv, 'BootBackdrop.imageset')
    imageset(directory, [{'filename': 'BootBackdrop.png', 'idiom': 'tv', 'scale': '1x'}, {'filename': 'BootBackdrop@2x.png', 'idiom': 'tv', 'scale': '2x'}])
    backdrop_png(SIDE, 1920, 1080, os.path.join(directory, 'BootBackdrop.png'))
    backdrop_png(SIDE, 3840, 2160, os.path.join(directory, 'BootBackdrop@2x.png'))
    for root in (ios, tv):
        directory = os.path.join(root, 'PorticoInk.colorset')
        os.makedirs(directory, exist_ok=True)
        colour = {'color-space': 'srgb', 'components': {'alpha': '1.000', 'red': '0x07', 'green': '0x10', 'blue': '0x18'}}
        with open(os.path.join(directory, 'Contents.json'), 'w') as f:
            json.dump({'colors': [{'color': colour, 'idiom': 'universal'},
                                  {'appearances': [{'appearance': 'luminosity', 'value': 'dark'}], 'color': colour, 'idiom': 'universal'}],
                       'info': {'author': 'xcode', 'version': 1}}, f, indent=2)
            f.write('\n')


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit(__doc__)
    box, paths = wordmark(sys.argv[1])
    android_vectors(box, paths)
    apple(box, paths)
