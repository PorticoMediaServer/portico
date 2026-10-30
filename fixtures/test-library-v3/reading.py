#!/usr/bin/env python3
"""Deterministic synthetic Reading fixtures for the Portico test server.

Every publication is original: fictional titles, generated prose, generated
artwork and generated fixed-page PDFs. Nothing is fetched and no commercial
media, cover or font is copied.

Library roots and importer facts (verified in server source at c1c9ce7d5):

* ``book`` libraries admit ``.epub`` and ``.pdf``. A book EPUB is entity kind
  ebook (20); a PDF is also ebook. EPUB metadata is adopted when the background
  Reading preparation opens the publication.
* ``comic`` libraries admit ``.cbz``, ``.cbr`` (only in a ``portico_rar7z``
  build) and ``.pdf``. They become comic entities (21).
* Catalogue title/author initially come from the file name and folder:
  ``Author - Title.ext`` supplies an author only when the stem has exactly one
  ``" - "``. Series comes from an innermost non-generic folder with series
  evidence, or from the publication itself (EPUB calibre series meta or
  ComicInfo Series/Number); file metadata wins over path values.
* Magazine/newspaper membership requires an accepted explicit publication type
  (EPUB ``dc:type``; ComicInfo ``Format``), a series and an ISO issue date in
  the inventory file name (``YYYY-MM`` for magazines, ``YYYY-MM-DD`` for
  newspapers). Periodical endpoints read book libraries only, so these issues
  are EPUB.
* PDF text/author comes only from the document itself: a real text layer and
  exact ``Info/Author``. Server PDF covers are not rendered, so PDFs are
  deliberately coverless.

CBR is a genuine stored RAR5 container written by the small original writer
below (adapted from the project's CC0 fixture tooling). A ZIP renamed to
``.cbr`` is never produced, and no external RAR encoder is required. Server
admission still needs a ``portico_rar7z`` build; every official build takes that
tag (with ``portico_cb7``) from ``scripts/release-build-tags.py``, while a plain
untagged ``go build`` stays dark. Check the actual binary before qualifying CBR
ingestion.

Root sections (each is its own state-helper library root/type):
``Reading/Books`` (book), ``Reading/Comics`` (comic),
``Reading/Magazines`` (book), ``Reading/Newspapers`` (book),
``Reading/Mixed`` (book: EPUB books plus image-page PDF comics) and
``Reading/Large Books`` (book, 10,050 tiny EPUBs for CON-012).

``generate(root, ffmpeg, full=True)`` is deterministic and writes only inside
those sections. ``full=False`` emits a small representative subset (including
12 large-library samples), for fast local checks.
"""

from __future__ import annotations

from pathlib import Path
import binascii
import hashlib
import html
import io
import math
import random
import shutil
import struct
import tempfile
import zipfile

from PIL import Image, ImageDraw, ImageFont

try:  # imported by the sibling runner
    from common import art
except ImportError:  # imported as part of a package
    from .common import art


LARGE_LIBRARY_COUNT = 10050

LIBRARIES = [
    {"root": "Reading/Books", "kind": "book", "name": "Design Test Books"},
    {"root": "Reading/Comics", "kind": "comic", "name": "Design Test Comics"},
    {"root": "Reading/Magazines", "kind": "book", "name": "Design Test Magazines"},
    {"root": "Reading/Newspapers", "kind": "book", "name": "Design Test Newspapers"},
    {"root": "Reading/Mixed", "kind": "book", "name": "Design Test Mixed Reading"},
    {"root": "Reading/Large Books", "kind": "book", "name": "Design Test Large Books"},
]

GAPS: list[str] = []

FIXED_ZIP_TIME = (2024, 1, 1, 0, 0, 0)
FONT_BOLD = "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf"
FONT_REGULAR = "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"


# ---------------------------------------------------------------------------
# deterministic primitives


def _digest(*parts) -> int:
    raw = "\x1f".join(str(p) for p in parts).encode("utf-8")
    return int.from_bytes(hashlib.sha256(raw).digest()[:8], "big")


def _font(size: int, bold: bool = True):
    path = FONT_BOLD if bold else FONT_REGULAR
    try:
        return ImageFont.truetype(path, size)
    except OSError:
        return ImageFont.load_default(size)


def _wrap(draw, text: str, font, width: int) -> list[str]:
    words = text.split()
    lines: list[str] = []
    line = ""
    for word in words:
        candidate = word if not line else line + " " + word
        if draw.textlength(candidate, font=font) <= width:
            line = candidate
        else:
            if line:
                lines.append(line)
            line = word
    if line:
        lines.append(line)
    return lines


def _cover_bytes(path: Path, title: str, seed: int, size=(600, 900)) -> bytes:
    art(path, title, size=size, seed=seed)
    return path.read_bytes()


def _small_cover_bytes(title: str, seed: int) -> bytes:
    """Cheap deterministic thumbnail cover for the 10k-book library."""
    w, h = 120, 180
    digest = hashlib.sha256((str(seed) + title).encode("utf-8")).digest()
    bg = tuple(20 + x % 40 for x in digest[:3])
    im = Image.new("RGB", (w, h), bg)
    draw = ImageDraw.Draw(im)
    accent = tuple(110 + x % 120 for x in digest[3:6])
    draw.rectangle((0, 0, w, 4), fill=accent)
    draw.rectangle((8, h - 12, w - 8, h - 11), fill=accent)
    font = _font(10)
    lines = _wrap(draw, title, font, w - 16)[:4]
    y = 30
    for line in lines:
        draw.text((8, y), line, font=font, fill=(242, 236, 222))
        y += 13
    draw.text((8, h - 24), "PORTICO", font=_font(7), fill=accent)
    out = io.BytesIO()
    im.save(out, format="JPEG", quality=82, optimize=True)
    return out.getvalue()


def _comic_page_bytes(title: str, series: str, number: str, page: int, total: int,
                      seed: int, size=(720, 1080)) -> bytes:
    """Original interior comic page: panels, hatching and a caption slug."""
    w, h = size
    rnd = random.Random(seed)
    paper = (238, 232, 219)
    ink = (25, 24, 22)
    im = Image.new("RGB", size, paper)
    draw = ImageDraw.Draw(im)

    caption_h = int(h * .10)
    draw.rectangle((0, 0, w, caption_h), fill=(31, 36, 41))
    draw.text((24, 18), f"{series.upper()}  {number}", font=_font(20), fill=(238, 232, 219))
    draw.text((w - 120, 20), f"{page:02d} / {total:02d}", font=_font(16), fill=(190, 198, 205))

    top = caption_h + int(h * .025)
    gap = int(w * .025)
    left = gap
    right = w - gap
    mid = (left + right) // 2
    row_h = (h - top - gap * 2) // 2
    panels = [
        (left, top, mid - gap // 2, top + row_h),
        (mid + gap // 2, top, right, top + row_h),
        (left, top + row_h + gap, right, h - gap),
    ]
    for index, box in enumerate(panels):
        x0, y0, x1, y1 = box
        draw.rectangle(box, outline=ink, width=4)
        # horizon / architecture
        horizon = y0 + int((y1 - y0) * (.35 + .1 * rnd.random()))
        draw.line((x0 + 8, horizon, x1 - 8, horizon), fill=ink, width=2)
        for _ in range(4 + index):
            bw = int((x1 - x0) * (.08 + .16 * rnd.random()))
            bh = int((y1 - horizon) * (.25 + .55 * rnd.random()))
            bx = x0 + int((x1 - x0 - bw) * rnd.random())
            draw.rectangle((bx, y1 - bh - 8, bx + bw, y1 - 8), outline=ink, width=2)
        # figure
        fx = x0 + int((x1 - x0) * (.25 + .5 * rnd.random()))
        fy = y1 - int((y1 - y0) * .22)
        r = int((y1 - y0) * .055)
        draw.ellipse((fx - r, fy - r * 3, fx + r, fy - r), outline=ink, width=3)
        draw.line((fx, fy - r, fx, fy + r * 2), fill=ink, width=3)
        draw.line((fx - r, fy, fx + r, fy), fill=ink, width=3)
        draw.line((fx, fy + r * 2, fx - r, fy + r * 3), fill=ink, width=3)
        draw.line((fx, fy + r * 2, fx + r, fy + r * 3), fill=ink, width=3)
        # hatching for tone
        for step in range(5):
            xx = x0 + 12 + step * 14
            draw.line((xx, y0 + 10, xx + 20, y0 + 34), fill=(120, 118, 112), width=1)
        # speech bubble
        bx = x0 + int((x1 - x0) * .55)
        by = y0 + int((y1 - y0) * .12)
        draw.ellipse((bx, by, bx + 150, by + 62), outline=ink, width=2, fill=paper)
        draw.polygon([(bx + 32, by + 54), (bx + 20, by + 76), (bx + 58, by + 56)], fill=paper, outline=ink)
        for line_i in range(3):
            draw.line((bx + 18, by + 16 + line_i * 13, bx + 132, by + 16 + line_i * 13),
                      fill=ink, width=2)

    slug = f"{title}  |  page {page}"
    draw.text((24, h - 28), slug[:72], font=_font(12), fill=(80, 76, 70))
    out = io.BytesIO()
    im.save(out, format="JPEG", quality=84, optimize=True)
    return out.getvalue()


# ---------------------------------------------------------------------------
# original prose


_SENTENCE_TEMPLATES = [
    "The {thing} waited beyond the {place}, and {person} counted the {other} once more.",
    "Rain moved across the {place} while {person} folded the {thing} into a narrow envelope.",
    "Nobody in the {place} remembered why the {thing} had been painted {colour}.",
    "A {colour} lamp burned in the window, though {person} had promised to leave it dark.",
    "The {other} arrived on a Tuesday, carrying news of the {place} and a broken {thing}.",
    "By morning the {thing} was gone, and the {other} had taken the only honest map.",
    "Every door in the {place} opened inward, which made the {other} laugh until it hurt.",
    "The {thing} smelled of salt and old paper, and {person} could not throw it away.",
    "{person} wrote three letters to the {place}, then burned the fourth without reading it.",
    "Beyond the {place}, the {other} became a rumour that the children repeated for years.",
    "There was a quiet agreement about the {thing}, and it held until the {colour} tide came in.",
    "The clock above the {place} ran four minutes late, which everyone treated as a kindness.",
    "In the drawer was a photograph of the {other}, standing in front of a {colour} wall.",
    "The {thing} had no lock, only a small brass hook that {person} checked every night.",
    "A gull followed the boat for an hour, then gave up and returned to the {place}.",
    "The {other} said the {thing} was a kind of promise, and {person} chose to believe it.",
]

_VOCAB = {
    "thing": ["lantern", "ledger", "compass", "map", "coat", "letter", "camera", "bell",
              "notebook", "little engine", "paper boat", "blue key", "radio", "satchel"],
    "other": ["stranger", "cousin", "inspector", "neighbour", "postman", "archivist",
              "sailor", "watchmaker", "girl with the dog", "old surveyor", "diver"],
    "person": ["Mara", "Ivo", "Toma", "Nia", "Elias", "Ruth", "Sena", "Ada", "Jun",
               "Priya", "Cormac", "Hedda", "Amara", "Lior", "Tess"],
    "place": ["harbour", "station", "orchard", "archive", "north shore", "market",
              "lighthouse", "printing house", "canal", "observatory", "island"],
    "colour": ["green", "rust", "silver", "amber", "violet", "white", "salt-blue"],
}

_HE_SENTENCES = [
    "האור נשפך על {a} ועל {b} בשקט.",
    "דנה מצאה את {a} ליד {b} ולא אמרה דבר.",
    "הם הלכו אל {a} דרך {b} בזמן שהגשם ירד.",
    "בבוקר {b} היה קר, והלפיד על {a} עדיין בער.",
    "אף אחד לא זכר מי בנה את {a} מעל {b}.",
    "המכתב על {b} נשא את שמו של {a} הישן.",
]
_HE_WORDS = ["הים", "המגדל", "הדרך", "הסירה", "המנורה", "הבית", "הגן", "הספר",
             "השער", "החלון", "האבן", "הרוח", "האי", "הנמל", "הכוכב", "העץ"]


def _paragraphs(seed: int, words: int, hebrew: bool = False) -> list[str]:
    rnd = random.Random(seed)
    out: list[str] = []
    total = 0
    while total < words:
        sentences = []
        for _ in range(rnd.randint(3, 6)):
            if hebrew:
                sentence = rnd.choice(_HE_SENTENCES).format(
                    a=rnd.choice(_HE_WORDS), b=rnd.choice(_HE_WORDS))
            else:
                sentence = rnd.choice(_SENTENCE_TEMPLATES).format(
                    thing=rnd.choice(_VOCAB["thing"]),
                    other=rnd.choice(_VOCAB["other"]),
                    person=rnd.choice(_VOCAB["person"]),
                    place=rnd.choice(_VOCAB["place"]),
                    colour=rnd.choice(_VOCAB["colour"]),
                )
            sentences.append(sentence)
            total += len(sentence.split())
        out.append(" ".join(sentences))
        if total >= words:
            break
    return out


# ---------------------------------------------------------------------------
# EPUB


_EPUB_CSS = """body { margin: 5%; line-height: 1.45; }
h1 { font-size: 1.45em; }
p { margin: 0 0 .7em 0; }
.small { font-size: .8em; }
"""


def _xhtml(title: str, heading: str, paragraphs: list[str], illustrated: bool = False,
           body_class: str = "") -> bytes:
    parts = [
        '<?xml version="1.0" encoding="utf-8"?>',
        '<html xmlns="http://www.w3.org/1999/xhtml">',
        "<head>",
        f"<title>{html.escape(title)}</title>",
        '<link rel="stylesheet" type="text/css" href="style.css"/>',
        "</head>",
        f'<body class="{body_class}">' if body_class else "<body>",
        f"<h1>{html.escape(heading)}</h1>",
    ]
    if illustrated:
        parts.append('<img src="cover.jpg" alt="Cover"/>')
    for paragraph in paragraphs:
        parts.append(f"<p>{html.escape(paragraph)}</p>")
    parts += ["</body>", "</html>"]
    return "\n".join(parts).encode("utf-8")


def _epub_bytes(meta: dict, chapters: list[dict], cover: bytes | None,
                store_cover: bool = False) -> bytes:
    """A minimal, strict, deterministic EPUB 3 package with an NCX fallback."""
    title = meta["title"]
    author = meta.get("author") or ""
    language = meta.get("language") or "en"
    identifier = "urn:uuid:" + hashlib.sha256(
        (title + "\x00" + author).encode("utf-8")).hexdigest()[:32]
    uid = identifier.rsplit(":", 1)[-1]

    items = [
        '<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>',
        '<item id="style" href="style.css" media-type="text/css"/>',
    ]
    if cover is not None:
        items.append('<item id="cover-image" href="cover.jpg" media-type="image/jpeg" '
                     'properties="cover-image"/>')
    for index, _chapter in enumerate(chapters, 1):
        items.append(f'<item id="chapter{index}" href="chapter{index}.xhtml" '
                     'media-type="application/xhtml+xml"/>')
    spine = "".join(f'<itemref idref="chapter{index}"/>'
                    for index in range(1, len(chapters) + 1))

    metadata = [
        f'<dc:identifier id="pub-id">{html.escape(identifier)}</dc:identifier>',
        f"<dc:title>{html.escape(title)}</dc:title>",
    ]
    if author:
        metadata.append(f"<dc:creator>{html.escape(author)}</dc:creator>")
    metadata.append(f"<dc:language>{html.escape(language)}</dc:language>")
    if meta.get("publisher"):
        metadata.append(f"<dc:publisher>{html.escape(meta['publisher'])}</dc:publisher>")
    if meta.get("date"):
        metadata.append(f"<dc:date>{html.escape(meta['date'])}</dc:date>")
    if meta.get("description"):
        metadata.append(f"<dc:description>{html.escape(meta['description'])}</dc:description>")
    for subject in meta.get("subjects", []):
        metadata.append(f"<dc:subject>{html.escape(subject)}</dc:subject>")
    if meta.get("publication_type"):
        metadata.append(f"<dc:type>{html.escape(meta['publication_type'])}</dc:type>")
    if meta.get("series"):
        metadata.append(f'<meta name="calibre:series" content="{html.escape(meta["series"])}"/>')
        if meta.get("series_index") not in (None, ""):
            metadata.append('<meta name="calibre:series_index" content="'
                            f'{html.escape(str(meta["series_index"]))}"/>')
    if cover is not None:
        metadata.append('<meta name="cover" content="cover-image"/>')
    metadata.append('<meta property="dcterms:modified">2024-01-01T00:00:00Z</meta>')

    opf = f"""<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="pub-id">
 <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
  {' '.join(metadata)}
 </metadata>
 <manifest>
  {' '.join(items)}
 </manifest>
 <spine toc="ncx" page-progression-direction="{meta.get('direction', 'ltr')}">
  {spine}
 </spine>
</package>
"""

    nav_points = []
    for index, chapter in enumerate(chapters, 1):
        nav_points.append(
            f'<navPoint id="nav{index}" playOrder="{index}">'
            f"<navLabel><text>{html.escape(chapter['title'])}</text></navLabel>"
            f'<content src="chapter{index}.xhtml"/></navPoint>')
    # The reader's navigation tree permits no text node outside the single
    # root, so the declaration touches the root and the document ends at it.
    ncx = ('<?xml version="1.0" encoding="utf-8"?>'
           '<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">'
           f'<head><meta name="dtb:uid" content="{html.escape(identifier)}"/></head>'
           f"<docTitle><text>{html.escape(title)}</text></docTitle>"
           f"<navMap>{''.join(nav_points)}</navMap>"
           "</ncx>")

    container = """<?xml version="1.0" encoding="utf-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
 <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>
"""

    files: dict[str, bytes] = {
        "mimetype": b"application/epub+zip",
        "META-INF/container.xml": container.encode("utf-8"),
        "OEBPS/content.opf": opf.encode("utf-8"),
        "OEBPS/toc.ncx": ncx.encode("utf-8"),
        "OEBPS/style.css": _EPUB_CSS.encode("utf-8"),
    }
    if cover is not None:
        files["OEBPS/cover.jpg"] = cover
    for index, chapter in enumerate(chapters, 1):
        files[f"OEBPS/chapter{index}.xhtml"] = _xhtml(
            title, chapter["title"], chapter["paragraphs"],
            illustrated=bool(chapter.get("illustrated")))

    out = io.BytesIO()
    with zipfile.ZipFile(out, "w", allowZip64=False) as zf:
        first = zipfile.ZipInfo("mimetype", FIXED_ZIP_TIME)
        first.compress_type = zipfile.ZIP_STORED
        first.external_attr = 0o644 << 16
        first.create_system = 3
        zf.writestr(first, files["mimetype"])
        for name in sorted(files):
            if name == "mimetype":
                continue
            info = zipfile.ZipInfo(name, FIXED_ZIP_TIME)
            info.create_system = 3
            info.external_attr = 0o644 << 16
            if name == "OEBPS/cover.jpg" and store_cover:
                info.compress_type = zipfile.ZIP_STORED
            else:
                info.compress_type = zipfile.ZIP_DEFLATED
            zf.writestr(info, files[name], compresslevel=9)
    return out.getvalue()


def _write_epub(path: Path, meta: dict, chapters: list[dict], cover: bytes | None,
                store_cover: bool = False) -> int:
    path.parent.mkdir(parents=True, exist_ok=True)
    data = _epub_bytes(meta, chapters, cover, store_cover=store_cover)
    path.write_bytes(data)
    return len(data)


def _cover_or_none(work: Path, name: str, title: str, seed: int,
                   size=(600, 900), wanted: bool = True) -> bytes | None:
    if not wanted:
        return None
    return _cover_bytes(work / f"{name}.jpg", title, seed, size=size)


def _book_chapters(spec: dict) -> list[dict]:
    count = spec.get("chapters", 1)
    total_words = spec["words"]
    per = max(1, math.ceil(total_words / count))
    chapters = []
    for index in range(count):
        seed = _digest("book", spec["title"], index)
        paragraphs = _paragraphs(seed, per, hebrew=spec.get("hebrew", False))
        chapters.append({
            "title": spec.get("chapter_prefix", "Chapter ") + str(index + 1),
            "paragraphs": paragraphs,
            "illustrated": bool(spec.get("illustrated")) and index == 0,
        })
    return chapters


# ---------------------------------------------------------------------------
# CBZ / CBR


def _comicinfo(meta: dict) -> bytes:
    fields = {
        "Title": meta.get("volume_title", ""),
        "Series": meta.get("series", ""),
        "Number": meta.get("number", ""),
        "Writer": meta.get("writer", ""),
        "Publisher": "Portico Test Press",
        "Genre": meta.get("genre", ""),
        "Year": "2024",
        "Month": "3",
        "Summary": meta.get("summary", "Original synthetic pages for interface testing."),
        "LanguageISO": meta.get("language", "en"),
        "Manga": "YesAndRightToLeft" if meta.get("manga") else "No",
        "PageCount": str(meta.get("pages", 0)),
    }
    if meta.get("format") is not None:
        fields["Format"] = meta["format"]
    root = ["<?xml version=\"1.0\" encoding=\"utf-8\"?>", "<ComicInfo>"]
    for key, value in fields.items():
        root.append(f"<{key}>{html.escape(str(value))}</{key}>")
    root.append("</ComicInfo>")
    return "\n".join(root).encode("utf-8")


def _comic_pages(meta: dict) -> list[bytes]:
    pages = []
    for page in range(1, meta["pages"] + 1):
        pages.append(_comic_page_bytes(
            meta.get("volume_title") or meta["series"], meta["series"],
            meta.get("number", ""), page, meta["pages"],
            _digest("comic", meta["series"], meta.get("number", ""), page)))
    return pages


def _write_cbz(path: Path, meta: dict) -> int:
    path.parent.mkdir(parents=True, exist_ok=True)
    out = io.BytesIO()
    with zipfile.ZipFile(out, "w", allowZip64=False) as zf:
        info = zipfile.ZipInfo("ComicInfo.xml", FIXED_ZIP_TIME)
        info.create_system = 3
        info.external_attr = 0o644 << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        zf.writestr(info, _comicinfo(meta), compresslevel=9)
        for index, page in enumerate(_comic_pages(meta), 1):
            name = f"pages/p{index:03d}.jpg"
            info = zipfile.ZipInfo(name, FIXED_ZIP_TIME)
            info.create_system = 3
            info.external_attr = 0o644 << 16
            info.compress_type = zipfile.ZIP_STORED
            zf.writestr(info, page)
    data = out.getvalue()
    path.write_bytes(data)
    return len(data)


# Official RAR5 layout, stored method 0 only: signature, main block, file
# blocks, end block. Header CRCs cover the HEAD_SIZE varint and body. Adapted
# from the project's CC0 fixture tooling and confirmed by the server's pinned
# rardecode v2.4.5 reader against the existing walk CBR.
_RAR_SIGNATURE = b"Rar!\x1a\x07\x01\x00"
_RAR_BLOCK_ARC, _RAR_BLOCK_FILE, _RAR_BLOCK_END = 1, 2, 5
_RAR_HAS_EXTRA, _RAR_HAS_DATA = 0x0001, 0x0002
_RAR_FILE_MTIME, _RAR_FILE_CRC = 0x0002, 0x0004
_RAR_HOST_UNIX = 1
_RAR_MTIME = 1704067200  # 2024-01-01T00:00:00Z


def _rar_vint(value: int) -> bytes:
    if value < 0:
        raise ValueError("negative RAR vint")
    out = bytearray()
    while True:
        byte = value & 0x7F
        value >>= 7
        out.append(byte | (0x80 if value else 0))
        if not value:
            return bytes(out)


def _rar_block(kind: int, flags: int, fields: bytes, data: bytes = b"",
               extra: bytes = b"") -> bytes:
    body = bytearray()
    body += _rar_vint(kind)
    body += _rar_vint(flags | (_RAR_HAS_EXTRA if extra else 0) |
                      (_RAR_HAS_DATA if data else 0))
    if extra:
        body += _rar_vint(len(extra))
    if data:
        body += _rar_vint(len(data))
    body += fields + extra
    size = _rar_vint(len(body))
    crc = binascii.crc32(size + bytes(body)) & 0xFFFFFFFF
    return struct.pack("<I", crc) + size + bytes(body) + data


def _rar_file_block(name: str, payload: bytes) -> bytes:
    name_bytes = name.encode("utf-8")
    fields = bytearray()
    fields += _rar_vint(_RAR_FILE_MTIME | _RAR_FILE_CRC)
    fields += _rar_vint(len(payload))
    fields += _rar_vint(0o100644)
    fields += struct.pack("<I", _RAR_MTIME)
    fields += struct.pack("<I", binascii.crc32(payload) & 0xFFFFFFFF)
    fields += _rar_vint(0)  # stored, non-solid
    fields += _rar_vint(_RAR_HOST_UNIX)
    fields += _rar_vint(len(name_bytes))
    fields += name_bytes
    return _rar_block(_RAR_BLOCK_FILE, 0, bytes(fields), data=payload)


def _write_cbr(path: Path, meta: dict) -> int:
    """Write a genuine stored RAR5 CBR; never rename a ZIP."""
    members = [("ComicInfo.xml", _comicinfo(meta))]
    for index, page in enumerate(_comic_pages(meta), 1):
        members.append((f"pages/p{index:03d}.jpg", page))
    out = io.BytesIO()
    out.write(_RAR_SIGNATURE)
    out.write(_rar_block(_RAR_BLOCK_ARC, 0, _rar_vint(0)))
    for name, payload in members:
        out.write(_rar_file_block(name, payload))
    out.write(_rar_block(_RAR_BLOCK_END, 0, _rar_vint(0)))
    data = out.getvalue()
    if data[:8] != _RAR_SIGNATURE:
        raise RuntimeError("not a RAR5 container")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return len(data)


# ---------------------------------------------------------------------------
# PDF


def _pdf_string(value: str) -> bytes:
    raw = value.encode("latin-1", "replace")
    return (b"(" + raw.replace(b"\\", b"\\\\").replace(b"(", b"\\(")
            .replace(b")", b"\\)") + b")")


def _pdf_stream(body: bytes) -> bytes:
    return b"<< /Length " + str(len(body)).encode() + b" >>\nstream\n" + body + b"\nendstream"


def _pdf_text_lines(paragraphs: list[str], max_lines: int = 40) -> list[str]:
    lines: list[str] = []
    for paragraph in paragraphs:
        words = paragraph.split()
        for start in range(0, len(words), 11):
            lines.append(" ".join(words[start:start + 11]))
    if len(lines) > max_lines:
        lines = lines[:max_lines]
    while len(lines) < max_lines:
        lines.append("")
    return lines


def _pdf_text_content(lines: list[str]) -> bytes:
    parts = [b"BT", b"/F1 11 Tf", b"14 TL", b"54 738 Td"]
    for index, line in enumerate(lines):
        if index:
            parts.append(b"T*")
        parts.append(_pdf_string(line) + b" Tj")
    parts.append(b"ET")
    return b"\n".join(parts)


def _pdf_build(page_specs: list[dict], title: str, author: str) -> bytes:
    objects: dict[int, bytes] = {}
    objects[3] = (b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica "
                  b"/Encoding /WinAnsiEncoding >>")
    info = b"<< /Title " + _pdf_string(title) + b" /Producer " + \
        _pdf_string("Portico synthetic test library") + \
        b" /CreationDate (D:20240101000000Z)"
    if author:
        info += b" /Author " + _pdf_string(author)
    info += b" >>"
    objects[4] = info

    next_id = 5
    page_ids: list[int] = []
    for spec in page_specs:
        if "jpeg" in spec:
            width, height, jpeg = spec["width"], spec["height"], spec["jpeg"]
            image_id = next_id
            next_id += 1
            objects[image_id] = (
                b"<< /Type /XObject /Subtype /Image /Width " + str(width).encode() +
                b" /Height " + str(height).encode() +
                b" /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length " +
                str(len(jpeg)).encode() + b" >>\nstream\n" + jpeg + b"\nendstream")
            content = (f"q {width} 0 0 {height} 0 0 cm /Im0 Do Q").encode()
            resources = f"<< /XObject << /Im0 {image_id} 0 R >> >>".encode()
            media = f"[0 0 {width} {height}]".encode()
        else:
            content = _pdf_text_content(spec["lines"])
            resources = b"<< /Font << /F1 3 0 R >> >>"
            media = b"[0 0 612 792]"
        content_id = next_id
        next_id += 1
        objects[content_id] = _pdf_stream(content)
        page_id = next_id
        next_id += 1
        objects[page_id] = (
            b"<< /Type /Page /Parent 2 0 R /MediaBox " + media +
            b" /Resources " + resources + b" /Contents " + str(content_id).encode() + b" 0 R >>")
        page_ids.append(page_id)

    objects[1] = b"<< /Type /Catalog /Pages 2 0 R >>"
    objects[2] = (b"<< /Type /Pages /Kids [" +
                  b" ".join(str(pid).encode() + b" 0 R" for pid in page_ids) +
                  b"] /Count " + str(len(page_ids)).encode() + b" >>")

    if set(objects) != set(range(1, next_id)):
        raise RuntimeError("non-contiguous PDF object ids")

    out = io.BytesIO()
    out.write(b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
    offsets: dict[int, int] = {}
    for object_id in sorted(objects):
        offsets[object_id] = out.tell()
        out.write(f"{object_id} 0 obj\n".encode())
        out.write(objects[object_id])
        out.write(b"\nendobj\n")
    xref = out.tell()
    out.write(f"xref\n0 {next_id}\n".encode())
    out.write(b"0000000000 65535 f \n")
    for object_id in range(1, next_id):
        out.write(f"{offsets[object_id]:010d} 00000 n \n".encode())
    out.write(b"trailer\n<< /Size " + str(next_id).encode() +
              b" /Root 1 0 R /Info 4 0 R >>\nstartxref\n" +
              str(xref).encode() + b"\n%%EOF\n")
    return out.getvalue()


def _write_pdf(path: Path, page_specs: list[dict], title: str, author: str) -> int:
    path.parent.mkdir(parents=True, exist_ok=True)
    data = _pdf_build(page_specs, title, author)
    path.write_bytes(data)
    return len(data)


def _text_pdf_pages(words: int, seed: int) -> list[dict]:
    pages = []
    remaining = max(1, words)
    page = 0
    while remaining > 0:
        page += 1
        chunk = min(remaining, 440)  # 40 lines x 11 words
        paragraphs = _paragraphs(seed + page, chunk)
        lines = _pdf_text_lines(paragraphs)
        lines[-1] = f"Page {page}"
        pages.append({"lines": lines})
        remaining -= chunk
    return pages


def _image_pdf_pages(title: str, series: str, number: str, pages: int) -> list[dict]:
    specs = []
    for page in range(1, pages + 1):
        raw = _comic_page_bytes(title, series, number, page, pages,
                                _digest("pdfpage", title, page), size=(720, 1080))
        specs.append({"jpeg": raw, "width": 720, "height": 1080})
    return specs


# ---------------------------------------------------------------------------
# fixture plans


_BOOKS = [
    {"title": "The Quiet Cartographer", "author": "Mara Vell", "words": 26000,
     "chapters": 9, "genre": "Literary"},
    {"title": "A Map of Small Rooms", "author": "Ivo Perrin", "words": 18000,
     "chapters": 7, "genre": "Literary"},
    {"title": "The Orchard at Half-Light", "author": "Sena Okafor", "words": 32000,
     "chapters": 11, "genre": "Fantasy"},
    {"title": "North of the Weather", "author": "Toma Lindqvist", "words": 1400,
     "chapters": 1, "genre": "Short story"},
    {"title": "The Paper Astronomer", "author": "Ruth Achterberg", "words": 20000,
     "chapters": 6, "genre": "Science fiction"},
    {"title": "The Long Meridian", "author": "Elias Voss", "words": 46800,
     "chapters": 16, "genre": "Historical"},
    {"title": "A House of Tidal Clocks", "author": "Nia Calloway", "words": 46200,
     "chapters": 15, "genre": "Literary"},
    {"title": "Café & Comet", "author": "Zoë Marchand", "words": 26000,
     "chapters": 8, "genre": "Romance", "illustrated": True},
    {"title": "Graft", "author": "Ada Merrow", "words": 30000, "chapters": 10,
     "series": "The Glass Orchard", "series_index": 1, "genre": "Fantasy"},
    {"title": "Bloom", "author": "Ada Merrow", "words": 28000, "chapters": 9,
     "series": "The Glass Orchard", "series_index": 2, "genre": "Fantasy"},
    {"title": "The Salt Road", "author": "Priya Nandakumar", "words": 34000,
     "chapters": 12, "series": "The Salt Road", "series_index": 1, "genre": "Adventure"},
    {"title": "The Cartographer's Daughter", "author": "Priya Nandakumar", "words": 36000,
     "chapters": 12, "series": "The Salt Road", "series_index": 2, "genre": "Adventure"},
    {"title": "A Winter Crossing", "author": "Priya Nandakumar", "words": 31000,
     "chapters": 10, "series": "The Salt Road", "series_index": 3, "genre": "Adventure"},
    {"title": "Lantern Weather", "author": "Jun Otani", "words": 30000, "chapters": 10,
     "series": "Lantern Weather", "series_index": 1, "genre": "Fantasy"},
    {"title": "The Bell Divers", "author": "Jun Otani", "words": 29000, "chapters": 9,
     "series": "Lantern Weather", "series_index": 2, "genre": "Fantasy"},
    {"title": "The Far Shore", "author": "Jun Otani", "words": 33000, "chapters": 11,
     "series": "Lantern Weather", "series_index": 3, "genre": "Fantasy"},
    {"title": "The Last Lighthouse", "author": "Jun Otani", "words": 30500, "chapters": 10,
     "series": "Lantern Weather", "series_index": 4, "genre": "Fantasy"},
    {"title": "מגדל האור", "author": "דנה לוי", "words": 22000, "chapters": 8,
     "genre": "ספרות", "language": "he", "hebrew": True, "direction": "rtl"},
    {"title": "The Unfinished Atlas", "author": "Cormac Bell", "words": 21000,
     "chapters": 7, "genre": "Literary", "cover": False},
    {"title": "Notes from the Low Country", "author": "Hedda Sol", "words": 19000,
     "chapters": 6, "genre": "Essays", "cover": False},
    {"title": "A Field Guide to Imaginary Birds", "author": "P. J. Wren", "words": 12000,
     "chapters": 5, "genre": "Nature", "cover": False},
    {"title": "The Clockwork Tide", "author": "Amara Singh", "words": 27000,
     "chapters": 9, "genre": "Science fiction"},
    {"title": "The Museum of Almost", "author": "Lior Avraham", "words": 23000,
     "chapters": 8, "genre": "Literary"},
    {"title": "Twenty-One Windows", "author": "Tess Amory", "words": 16000,
     "chapters": 21, "genre": "Linked stories"},
]

_MAGAZINES = [
    {"series": "Signal & Static", "issues": [
        ("2024-01", "The Quiet Machines"), ("2024-02", "Rooms That Listen"),
        ("2024-03", "A Map of the Air"), ("2024-04", "The Repair Issue"),
        ("2024-05", "Small Systems"), ("2024-06", "The Long Signal")],
     "genre": "Technology", "cover": True},
    {"series": "Harbour Kitchen", "issues": [
        ("2024-02", "Salt and Smoke"), ("2024-03", "The Spring Table"),
        ("2024-04", "Bread at Midnight"), ("2024-05", "A Kitchen in Winter")],
     "genre": "Food", "cover": True},
    {"series": "The Fictional Review", "issues": [
        ("2024-01", "Letters from Nowhere"), ("2024-04", "The Unreliable Map")],
     "genre": "Culture", "cover": False},
]

_NEWSPAPERS = [
    {"series": "The Harbour Ledger", "days": 14, "editions": {
        "2024-03-07": ["City Final", "Late Edition"]}, "cover": True},
    {"series": "Northwind Daily", "days": 5, "editions": {}, "cover": True},
]

_COMIC_SERIES = [
    {"series": "The Lantern Keepers", "writer": "Iris Vane", "volumes": 6,
     "pages": 14, "genre": "Fantasy"},
    {"series": "Neon Harbour", "writer": "Dax Okoro", "volumes": 4,
     "pages": 12, "genre": "Science fiction"},
    {"series": "Ashfall Chronicles", "writer": "Mira Solberg", "volumes": 5,
     "pages": 13, "genre": "Adventure"},
    {"series": "Paper Moon", "writer": "Hana Sato", "volumes": 3,
     "pages": 12, "genre": "Manga", "manga": True, "language": "ja"},
    {"series": "The Quiet Signal", "writer": "Owen Rask", "volumes": 3,
     "pages": 10, "genre": "Mystery"},
]

_COMIC_STANDALONE = [
    {"series": "Salt & Static", "writer": "Nell Varga", "pages": 16,
     "volume_title": "A One-Shot", "genre": "Drama"},
    {"series": "The Gilded Antenna", "writer": "Ravi Menon", "pages": 12,
     "volume_title": "A One-Shot", "genre": "Science fiction"},
    {"series": "Small Gods of the Harbour", "writer": "Tess Amory", "pages": 18,
     "volume_title": "A One-Shot", "genre": "Fantasy", "format": None},
]

_CBR_SERIES = {"series": "Rift Wardens", "writer": "Kai Mercer", "volumes": 3,
               "pages": 10, "genre": "Adventure"}

_PDF_BOOKS = [
    {"title": "The Meridian Notebook", "author": "Elias Voss", "pages": 48},
    {"title": "A Short Atlas", "author": "Mara Vell", "pages": 16},
    {"title": "The Tidal Almanac", "author": "Nia Calloway", "pages": 32},
]

_PDF_COMICS = [
    {"title": "The Cartographer's Apprentice", "author": "Iris Vane", "pages": 8},
    {"title": "A Door in the Fog", "author": "Owen Rask", "pages": 6},
    {"title": "The Brass Orchard", "author": "Mira Solberg", "pages": 10},
    {"title": "Night Bus to Nowhere", "author": "Dax Okoro", "pages": 7},
]

_MIXED_BOOKS = [
    {"title": "The Borrowed Hour", "author": "Nell Varga", "words": 17000,
     "chapters": 6, "genre": "Literary"},
    {"title": "A Grammar of Tides", "author": "Hedda Sol", "words": 15000,
     "chapters": 5, "genre": "Essays"},
    {"title": "The Quiet Engine", "author": "Amara Singh", "words": 24000,
     "chapters": 8, "genre": "Science fiction"},
    {"title": "Field Notes from the Edge", "author": "Cormac Bell", "words": 13000,
     "chapters": 4, "genre": "Travel"},
]

_LARGE_ADJECTIVES = [
    "Quiet", "Amber", "Hidden", "Salt", "Winter", "Paper", "Distant", "Golden",
    "Restless", "Narrow", "Bright", "Folded", "Blue", "Late", "Small", "Open",
    "Northern", "Tidal", "Silver", "Patient", "Borrowed", "Glass", "Copper",
    "Silent", "Green", "Long", "Iron", "Soft", "Wild", "Faded", "Second",
    "Burning", "Inland", "Low", "Common", "Strange", "Wandering", "Final",
    "Early", "Deep",
]
_LARGE_NOUNS = [
    "Harbour", "Station", "Orchard", "Archive", "Room", "Window", "Garden",
    "Ledger", "Compass", "Bridge", "Island", "Lighthouse", "Market", "Clock",
    "Atlas", "Letter", "Engine", "Shore", "Chapel", "Observatory", "Canal",
    "Kitchen", "Workshop", "Library", "Meadow", "Tower", "Studio", "Ferry",
    "Quarry", "Hollow", "Terrace", "Gallery", "Kitchen", "Platform", "Courtyard",
    "Museum", "Garage", "Boathouse", "Greenhouse", "Foundry",
]
_LARGE_SUFFIXES = ["", " at Dawn", " in Winter", " After Rain", " Revisited",
                   " at Low Tide", " for Beginners"]
_LARGE_AUTHORS = [
    "Ada Merrow", "Ivo Perrin", "Sena Okafor", "Toma Lindqvist", "Ruth Achterberg",
    "Elias Voss", "Nia Calloway", "Zoë Marchand", "Priya Nandakumar", "Jun Otani",
    "Cormac Bell", "Hedda Sol", "P. J. Wren", "Amara Singh", "Lior Avraham",
    "Tess Amory", "Mara Vell", "Nell Varga", "Dax Okoro", "Iris Vane",
    "Mira Solberg", "Owen Rask", "Hana Sato", "Ravi Menon", "Kai Mercer",
    "Anika Dune", "Bo Lindgren", "Celia Rook", "Dario Fenn", "Emet Vale",
    "Fiona Kest", "Galen Moss", "Hana Wilde", "Ines Tarr", "Jonas Reed",
    "Kira Sol", "Lena Quill", "Milo Vance", "Nora Pike", "Orla Grey",
    "Piet Soren", "Quinn Ash", "Rosa Linn", "Sami Holt", "Tara Wynn",
    "Ulla Frost", "Vera Lang", "Wes Callow", "Xenia Marr", "Yara Flint",
]


def _entry(kind: str, title: str, path: Path, root: Path, features: list[str],
           size: int = 0) -> dict:
    relative = path.relative_to(root).as_posix()
    library_root = None
    library_kind = None
    for library in LIBRARIES:
        if relative.startswith(library["root"] + "/"):
            library_root = library["root"]
            library_kind = library["kind"]
            break
    return {
        "kind": kind,
        "title": title,
        "path": relative,
        "features": features,
        "library_root": library_root,
        "library_kind": library_kind,
        "bytes": size,
    }


# ---------------------------------------------------------------------------
# section generators


def _generate_books(root: Path, work: Path, full: bool) -> list[dict]:
    entries = []
    plans = _BOOKS if full else _BOOKS[:6]
    for spec in plans:
        chapters = _book_chapters(spec)
        meta = {
            "title": spec["title"],
            "author": spec["author"],
            "language": spec.get("language", "en"),
            "direction": spec.get("direction", "ltr"),
            "publication_type": "book",
            "series": spec.get("series"),
            "series_index": spec.get("series_index"),
            "publisher": "Portico Test Press",
            "date": "2024-01-01",
            "description": "Original synthetic prose generated for interface testing.",
            "subjects": [spec["genre"]],
        }
        cover = _cover_or_none(work, "book-" + str(len(entries)), spec["title"],
                               _digest("cover", spec["title"]),
                               wanted=spec.get("cover", True))
        path = root / "Reading/Books" / f"{spec['author']} - {spec['title']}.epub"
        size = _write_epub(path, meta, chapters, cover)
        features = ["epub", "dc:type=book", "dc:creator", "ncx-chapters",
                    f"chapters={len(chapters)}", f"genre={spec['genre']}"]
        word_total = sum(len(p.split()) for c in chapters for p in c["paragraphs"])
        features.append("long-read" if word_total >= 45000 else "short-read")
        if spec.get("series"):
            features += ["calibre-series", f"series={spec['series']}",
                         f"series-index={spec['series_index']}"]
        if spec.get("language", "en") != "en":
            features.append(f"language={spec['language']}")
        if spec.get("direction") == "rtl":
            features.append("rtl")
        if spec.get("hebrew"):
            features.append("hebrew-text")
        if not spec.get("cover", True):
            features.append("no-cover-on-purpose")
        if spec.get("illustrated"):
            features.append("illustrated-chapter")
        if len(chapters) > 15:
            features.append("many-chapters")
        entries.append(_entry("epub", spec["title"], path, root, features, size))
    return entries


def _generate_comics(root: Path, work: Path, full: bool) -> list[dict]:
    entries = []
    series_plans = _COMIC_SERIES if full else _COMIC_SERIES[:2]
    volume_limit = 6 if full else 2
    for series in series_plans:
        for volume in range(1, min(series["volumes"], volume_limit) + 1):
            meta = {
                "series": series["series"],
                "number": str(volume),
                "volume_title": f"Volume {volume}",
                "writer": series["writer"],
                "genre": series["genre"],
                "pages": series["pages"],
                "manga": series.get("manga", False),
                "language": series.get("language", "en"),
                "format": "book",
            }
            title = f"{series['series']} {volume:02d}"
            path = root / "Reading/Comics" / series["series"] / f"{title}.cbz"
            size = _write_cbz(path, meta)
            features = ["cbz", "comicinfo", "series-folder", "multi-volume",
                        f"series={series['series']}", f"volume={volume}",
                        f"pages={series['pages']}", "writer=ComicInfo"]
            if series.get("manga"):
                features += ["manga-rtl", "language=ja"]
            if volume == 1:
                features.append("first-volume")
            entries.append(_entry("cbz", title, path, root, features, size))

    standalone_plans = _COMIC_STANDALONE if full else _COMIC_STANDALONE[:1]
    for item in standalone_plans:
        meta = {
            "series": item["series"],
            "number": "1",
            "volume_title": item["volume_title"],
            "writer": item["writer"],
            "genre": item["genre"],
            "pages": item["pages"],
            "format": item.get("format", "book"),
        }
        path = root / "Reading/Comics" / f"{item['series']}.cbz"
        size = _write_cbz(path, meta)
        features = ["cbz", "comicinfo", "one-shot", f"pages={item['pages']}"]
        if item.get("format", "book") is None:
            features.append("format-absent=unknown-type")
        entries.append(_entry("cbz", item["series"], path, root, features, size))

    if full:
        for volume in range(1, _CBR_SERIES["volumes"] + 1):
            meta = {
                "series": _CBR_SERIES["series"],
                "number": str(volume),
                "volume_title": f"Volume {volume}",
                "writer": _CBR_SERIES["writer"],
                "genre": _CBR_SERIES["genre"],
                "pages": _CBR_SERIES["pages"],
                "format": "book",
            }
            title = f"{_CBR_SERIES['series']} {volume:02d}"
            path = (root / "Reading/Comics" / _CBR_SERIES["series"] /
                    f"{_CBR_SERIES['writer']} - {title}.cbr")
            size = _write_cbr(path, meta)
            features = ["cbr", "rar5", "actual-rar-container", "stored-method-0",
                        "non-solid", "multi-volume", "author-from-filename",
                        "path-series-number", "cbr-comicinfo-reader-gap",
                        f"series={_CBR_SERIES['series']}", f"volume={volume}",
                        f"pages={_CBR_SERIES['pages']}",
                        "requires-portico_rar7z-build"]
            entries.append(_entry("cbr", title, path, root, features, size))

    for item in (_PDF_COMICS if full else _PDF_COMICS[:1]):
        pages = _image_pdf_pages(item["title"], item["title"], "1", item["pages"])
        path = root / "Reading/Comics" / f"{item['author']} - {item['title']}.pdf"
        size = _write_pdf(path, pages, item["title"], item["author"])
        entries.append(_entry(
            "pdf", item["title"], path, root,
            ["pdf", "comic-pdf", "image-pages", "fixed-page",
             f"pages={item['pages']}", "info-author"], size))
    return entries


def _periodical_issue(root: Path, work: Path, series: str, date: str, headline: str,
                      publication_type: str, genre: str, cover_wanted: bool,
                      index: int, seed_tag: str) -> tuple[Path, int]:
    words = 5200 if publication_type == "magazine" else 2600
    chapters = _book_chapters({"title": series + " " + date, "words": words,
                               "chapters": 4 if publication_type == "magazine" else 2})
    meta = {
        "title": f"{series} — {headline}",
        "author": series,
        "language": "en",
        "publication_type": publication_type,
        "series": series,
        "series_index": index,
        "publisher": "Portico Test Press",
        "date": date,
        "description": "Original synthetic periodical issue.",
        "subjects": [genre],
    }
    book_type = "Magazines" if publication_type == "magazine" else "Newspapers"
    title = f"{series} - {date} - {headline}"
    path = root / f"Reading/{book_type}" / f"{title}.epub"
    cover = _cover_or_none(work, "periodical-" + seed_tag, series + " " + date,
                           _digest("periodical", series, date, headline),
                           size=(600, 900), wanted=cover_wanted)
    size = _write_epub(path, meta, chapters, cover)
    return path, size


def _generate_magazines(root: Path, work: Path, full: bool) -> list[dict]:
    entries = []
    plans = _MAGAZINES if full else _MAGAZINES[:2]
    for magazine in plans:
        issues = magazine["issues"] if full else magazine["issues"][:2]
        for index, (date, headline) in enumerate(issues, 1):
            path, size = _periodical_issue(
                root, work, magazine["series"], date, headline, "magazine",
                magazine["genre"], magazine["cover"], index,
                _digest("mag", magazine["series"], date).__str__())
            features = ["epub", "dc:type=magazine", "periodical", "calibre-series",
                        f"series={magazine['series']}", f"issue-date={date}",
                        "month-precision", f"issue-index={index}"]
            if not magazine["cover"]:
                features.append("no-cover-on-purpose")
            entries.append(_entry(
                "epub", f"{magazine['series']} - {date} - {headline}",
                path, root, features, size))
    return entries


def _generate_newspapers(root: Path, work: Path, full: bool) -> list[dict]:
    entries = []
    plans = _NEWSPAPERS if full else _NEWSPAPERS[:1]
    for paper in plans:
        days = paper["days"] if full else min(2, paper["days"])
        for day in range(1, days + 1):
            date = f"2024-03-{day:02d}"
            editions = paper["editions"].get(date, ["Morning Edition"])
            if not full and day == 1:
                editions = editions[:1]
            for edition_index, edition in enumerate(editions, 1):
                path, size = _periodical_issue(
                    root, work, paper["series"], date, edition, "newspaper",
                    "News", paper["cover"], day,
                    _digest("news", paper["series"], date, edition).__str__())
                features = ["epub", "dc:type=newspaper", "periodical", "calibre-series",
                            f"series={paper['series']}", f"issue-date={date}",
                            "day-precision", f"edition={edition}"]
                if len(editions) > 1:
                    features += ["duplicate-edition-date", "same-day-editions"]
                entries.append(_entry(
                    "epub", f"{paper['series']} - {date} - {edition}",
                    path, root, features, size))
    return entries


def _generate_pdf_books(root: Path, work: Path, full: bool) -> list[dict]:
    entries = []
    plans = _PDF_BOOKS if full else _PDF_BOOKS[:2]
    for spec in plans:
        path = root / "Reading/Books" / f"{spec['author']} - {spec['title']}.pdf"
        pages = _text_pdf_pages(spec["pages"] * 440, _digest("pdfbook", spec["title"]))
        if len(pages) > spec["pages"]:
            pages = pages[:spec["pages"]]
        size = _write_pdf(path, pages, spec["title"], spec["author"])
        entries.append(_entry(
            "pdf", spec["title"], path, root,
            ["pdf", "text-layer", "info-author", "fixed-page",
             f"pages={spec['pages']}", "pdf-text-count"], size))

    if full:
        title = "Portico Sketchbook"
        author = "Iris Vane"
        path = root / "Reading/Books" / f"{author} - {title}.pdf"
        pages = _image_pdf_pages(title, title, "1", 24)
        size = _write_pdf(path, pages, title, author)
        entries.append(_entry(
            "pdf", title, path, root,
            ["pdf", "image-only", "no-text-layer", "fixed-page", "pages=24",
             "no-cover-rendered"], size))
    return entries


def _generate_mixed(root: Path, work: Path, full: bool) -> list[dict]:
    entries = []
    books = _MIXED_BOOKS if full else _MIXED_BOOKS[:2]
    for spec in books:
        chapters = _book_chapters(spec)
        meta = {
            "title": spec["title"], "author": spec["author"], "language": "en",
            "publication_type": "book", "publisher": "Portico Test Press",
            "date": "2024-01-01", "description": "Original synthetic prose.",
            "subjects": [spec["genre"]],
        }
        cover = _cover_or_none(work, "mixed-" + str(len(entries)), spec["title"],
                               _digest("mixed", spec["title"]))
        path = root / "Reading/Mixed" / f"{spec['author']} - {spec['title']}.epub"
        size = _write_epub(path, meta, chapters, cover)
        entries.append(_entry(
            "epub", spec["title"], path, root,
            ["epub", "mixed-library", "dc:type=book", "ncx-chapters"], size))

    comics = _PDF_COMICS[:2] if full else _PDF_COMICS[:1]
    for item in comics:
        pages = _image_pdf_pages(item["title"], item["title"], "1", item["pages"])
        path = root / "Reading/Mixed" / f"{item['author']} - {item['title']}.pdf"
        size = _write_pdf(path, pages, item["title"], item["author"])
        entries.append(_entry(
            "pdf", item["title"], path, root,
            ["pdf", "comic-pdf", "mixed-library", "image-pages", "fixed-page",
             f"pages={item['pages']}"], size))
    return entries


def _large_titles(count: int) -> list[str]:
    if count <= 0:
        return []
    titles: list[str] = []
    for suffix in _LARGE_SUFFIXES:
        for adjective in _LARGE_ADJECTIVES:
            for noun in _LARGE_NOUNS:
                title = f"The {adjective} {noun}{suffix}"
                if title not in titles:
                    titles.append(title)
                if len(titles) >= count:
                    return titles
    return titles[:count]


def _generate_large(root: Path, work: Path, count: int = LARGE_LIBRARY_COUNT) -> list[dict]:
    entries = []
    titles = _large_titles(count)
    if len(titles) < count:
        raise RuntimeError("not enough deterministic large-library titles")
    covers = [_small_cover_bytes(f"Large Library {index + 1}", 9000 + index)
              for index in range(24)]
    chapter_words = 55
    for index, title in enumerate(titles):
        author = _LARGE_AUTHORS[index % len(_LARGE_AUTHORS)]
        chapter = {
            "title": "Chapter 1",
            "paragraphs": _paragraphs(_digest("large", title), chapter_words),
        }
        meta = {
            "title": title,
            "author": author,
            "language": "en",
            "publication_type": "book",
            "publisher": "Portico Test Press",
            "date": "2024-01-01",
        }
        path = root / "Reading/Large Books" / f"{author} - {title}.epub"
        size = _write_epub(path, meta, [chapter], covers[index % len(covers)],
                           store_cover=True)
        features = ["epub", "large-library", "tiny-file", "shared-cover-variant",
                    f"cover-variant={index % 24 + 1}", f"{count}-items"]
        if count > 10000:
            features += ["con-012", "above-10000-matches"]
        else:
            features.append("large-library-sample")
        entries.append(_entry("epub", title, path, root, features, size))
    return entries


# ---------------------------------------------------------------------------
# entry point


def generate(root: Path, ffmpeg: str, full: bool = True) -> list[dict]:
    """Generate the Reading fixtures under *root* and return manifest entries."""
    root = Path(root)
    root.mkdir(parents=True, exist_ok=True)
    GAPS.clear()
    for library in LIBRARIES:
        target = root / library["root"]
        if target.exists():
            shutil.rmtree(target)

    work = Path(tempfile.mkdtemp(prefix="portico-reading-art-"))
    entries: list[dict] = []
    try:
        entries += _generate_books(root, work, full)
        entries += _generate_comics(root, work, full)
        entries += _generate_magazines(root, work, full)
        entries += _generate_newspapers(root, work, full)
        entries += _generate_pdf_books(root, work, full)
        entries += _generate_mixed(root, work, full)
        entries += _generate_large(root, work, LARGE_LIBRARY_COUNT if full else 12)
    finally:
        shutil.rmtree(work, ignore_errors=True)

    entries.sort(key=lambda entry: entry["path"])
    return entries


__all__ = ["generate", "LIBRARIES", "GAPS", "LARGE_LIBRARY_COUNT"]
