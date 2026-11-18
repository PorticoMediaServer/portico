sub init()
    m.under = m.top.findNode("under")
    m.over = m.top.findNode("over")
    m.clip = m.top.findNode("clip")
    m.plate = m.top.findNode("plate")
    m.made = m.top.findNode("made")
    m.madeGlyph = m.top.findNode("madeGlyph")
    m.madeRule = m.top.findNode("madeRule")
    m.madeTitle = m.top.findNode("madeTitle")
    m.art = m.top.findNode("art")
    m.tiles = m.top.findNode("tiles")
    m.shade = m.top.findNode("shade")
    m.title = m.top.findNode("title")
    m.sub = m.top.findNode("sub")
    m.track = m.top.findNode("track")
    m.bar = m.top.findNode("bar")
    m.badgePlate = m.top.findNode("badgePlate")
    m.badge = m.top.findNode("badge")
    c = colours()
    m.plate.blendColor = c.raised
    m.top.findNode("faceDisc").blendColor = c.raisedBright
    m.madeRule.color = c.coverRule
    m.track.color = c.trackOnArt
    m.bar.color = c.accent
    m.top.findNode("cornerPlate").blendColor = c.onArt
    m.top.findNode("tickPlate").blendColor = c.onArt
    m.badgePlate.blendColor = c.accent
    m.person = m.top.findNode("person")
    ' A page shown again by Back: a cut picture (a MaskGroup) left as it was does not draw again,
    ' so a row showed only the card in focus and its neighbours; each card makes its cut new.
    m.global.observeField("pageShownAgain", "onShownAgain")
    setType(m.top.findNode("faceInitials"), "personInitials", c.textSecondary)
    setType(m.top.findNode("faceName"), "cardTitle", c.text)
    setType(m.top.findNode("facePart"), "cardSubtitle", c.textTertiary)
    setType(m.title, "wideTitle", c.text)
    setType(m.sub, "cardSubtitle", c.textSecondary)
    setType(m.badge, "badge", c.actionInk)
    setType(m.top.findNode("corner"), "tag", c.text)
    setType(m.madeTitle, "coverTitle", c.text)
    setType(m.madeGlyph, "lockup", c.text)
    m.w = 0
    m.h = 0
end sub

sub onLayout()
    pad = metrics().cardPad
    w = m.top.width
    h = m.top.height - pad * 2
    if w = 0 or h <= 0 or (w = m.w and h = m.h) then return
    m.top.findNode("frame").translation = [0, pad]
    m.w = w
    m.h = h
    m.fullH = h
    m.under.scaleRotateCenter = [w / 2, h / 2]
    m.over.scaleRotateCenter = [w / 2, h / 2]
    m.plate.width = w
    m.plate.height = h
    m.madeRule.width = w * 0.09
    m.madeRule.translation = [w * 0.13, w * 0.12]
    m.madeTitle.width = w * 0.76
    m.madeTitle.translation = [w * 0.13, w * 0.12 + w * 0.077]
    m.madeGlyph.width = w
    m.madeGlyph.translation = [0, h - 128]
    m.clip.maskUri = "pkg:/images/mask-" + int(w).toStr() + "x" + int(h).toStr() + ".png"
    m.art.width = w
    m.art.height = h
    m.art.loadWidth = w
    m.art.loadHeight = h
    grow(1)
    m.title.width = w - 40
    m.title.translation = [20, h - 96]
    m.sub.width = w - 40
    m.sub.translation = [20, h - 60]
    m.track.width = w - 40
    m.track.translation = [20, h - 24]
    m.bar.translation = [20, h - 24]
    onContent()
end sub

sub onContent()
    item = m.top.itemContent
    if item = invalid or m.w = 0 then return
    isPerson = (item.kind = "person")
    m.person.visible = isPerson
    m.top.findNode("frame").getChild(0).visible = not isPerson
    m.under.visible = not isPerson     ' a face has no plate behind it (tv-detail frame 4)
    m.clip.visible = not isPerson
    m.over.visible = not isPerson
    if isPerson
        x = (m.w - 150) / 2
        for each id in ["faceDisc", "faceInitials", "faceClip"]
            m.top.findNode(id).translation = [x, 0]
        end for
        m.top.findNode("facePhoto").uri = item.art
        m.top.findNode("faceInitials").text = item.badge
        name = m.top.findNode("faceName")
        name.width = m.w + 20
        name.translation = [-10, 162]
        name.text = item.title
        part = m.top.findNode("facePart")
        part.width = m.w + 20
        part.text = item.sub
        ' a name is a card title: one line, ending in an ellipsis, so the part always sits under it
        part.translation = [-10, 194]
        onFocus()
        return
    end if
    ' A row's next page is on its way: one still card of the row's shape stands at its end
    ' (tv-focus frame 5), the plain plate and nothing on it.
    c = colours()
    if item.skeleton = true
        m.clip.visible = false
        m.over.visible = false
        m.h = m.fullH
        if item.artHeight <> invalid and item.artHeight > 0 then m.h = item.artHeight     ' a grid's: its picture's box, its words' place left empty
        m.under.translation = [0, 0]
        m.plate.height = m.h
        m.plate.blendColor = c.raisedHigh
        m.plate.visible = true
        return
    end if
    m.plate.blendColor = c.raised
    ' A cover is shown whole, in its own shape; other art fills the card.
    ' An episode card: the picture is 16:9 at the top and the words go under it.
    under = (item.under = true)
    if under then m.h = m.w * 9 / 16 else m.h = m.fullH
    if under and item.artHeight <> invalid and item.artHeight > 0 then m.h = item.artHeight
    ' An audiobook or album with no cover gets a square generated one, as its real covers are square.
    m.shiftY = 0
    if item.art = "" and not item.overlay and not under and m.fullH > m.w and (item.kind = "audiobook" or item.kind = "album" or item.kind = "track")
        m.h = m.w
        m.shiftY = (m.fullH - m.w) / 2
    end if
    m.under.scaleRotateCenter = [m.w / 2, m.h / 2]
    m.over.scaleRotateCenter = [m.w / 2, m.h / 2]
    m.under.translation = [0, m.shiftY]
    m.over.translation = [0, m.shiftY]
    m.madeGlyph.translation = [0, m.h - 128]
    m.art.height = m.h
    m.art.loadHeight = m.h
    m.plate.height = m.h
    m.clip.maskUri = "pkg:/images/mask-" + int(m.w).toStr() + "x" + int(m.h).toStr() + ".png"
    grow(1)
    overlay = item.overlay
    if item.art = ""
        ' no picture: the generated cover, with the title on it unless the card already words it
        m.art.loadDisplayMode = "noScale"
        m.art.uri = "pkg:/images/cover-" + coverTint(item.title).toStr() + ".jpg"
        ' (a grid's card words it under the cover too, but the cover carries the name: rule 12)
        if not overlay and (not under or item.oneLine = true)
            m.madeTitle.text = item.title
            m.madeGlyph.text = ucase(left(item.title, 1))
        end if
    else
        if item.whole then m.art.loadDisplayMode = "scaleToFit" else m.art.loadDisplayMode = "scaleToZoom"
        m.art.uri = item.art
    end if
    m.made.visible = (item.art = "" and not overlay and (not under or item.oneLine = true))
    ' four posters, when the content carries them, cover the one picture
    mosaic = (item.tiles <> invalid and item.tiles.count() = 4)
    m.tiles.visible = mosaic
    if mosaic
        for i = 0 to 3
            m.tiles.getChild(i).uri = item.tiles[i]
        end for
    end if
    m.plate.visible = (item.art <> "" and (overlay or not item.whole))
    m.shade.visible = overlay
    m.title.visible = overlay
    m.sub.visible = overlay
    hasBar = (overlay or under) and item.progress >= 0   ' progress is drawn on wide cards only, as the mock-ups have it
    m.track.visible = hasBar
    m.bar.visible = hasBar
    c = colours()
    wordsWidth = m.w - 40
    if overlay
        m.title.text = item.title
        m.sub.text = item.sub
        m.title.width = m.w - 40
        m.title.wrap = false
        if item.whole and item.art <> ""
            ' beside a cover the words have less room: the title takes a second line before it is cut
            wordsWidth = m.w - m.h - 30
            m.title.width = wordsWidth
            m.title.wrap = true
            m.title.maxLines = 2
        end if
        m.title.translation = [20, m.h - 96]
        if m.title.boundingRect().height > 44 then m.title.translation = [20, m.h - 128]
        m.sub.translation = [20, m.h - 60]
        setType(m.title, "wideTitle", c.text)
    else if under
        m.title.visible = true
        m.sub.visible = true
        m.title.text = item.title
        m.sub.text = item.sub
        m.title.width = m.w
        if item.oneLine = true
            ' a poster in a grid or row: its title and year one line each, the title ending in an ellipsis
            m.title.wrap = false
            setType(m.title, "cardTitle", c.text)
            setType(m.sub, "cardSubtitle", c.textTertiary)
            m.title.translation = [0, m.h + 14]
            m.sub.translation = [0, m.h + 48]
        else
            m.title.wrap = true
            m.title.maxLines = 2
            setType(m.title, "episodeTitle", c.text)
            setType(m.sub, "cardSubtitle", c.textSecondary)
            m.title.translation = [0, m.h + 26]
            ' the episode's number sits under the title however many lines it takes, with the same gap
            m.sub.translation = [0, m.h + 26 + m.title.boundingRect().height + 4]
        end if
    end if
    m.track.translation = [20, m.h - 24]
    m.bar.translation = [20, m.h - 24]
    if under
        m.track.translation = [0, m.h - 6]
        m.bar.translation = [0, m.h - 6]
        m.track.width = m.w
        m.track.visible = false
    else
        m.track.width = wordsWidth
    end if
    if hasBar and under then m.bar.width = m.w * item.progress
    if hasBar and not under then m.bar.width = wordsWidth * item.progress
    corner = m.top.findNode("corner")
    cornerPlate = m.top.findNode("cornerPlate")
    hasCorner = (item.corner <> invalid and item.corner <> "")
    corner.visible = hasCorner
    cornerPlate.visible = hasCorner
    if hasCorner
        corner.text = item.corner
        cw = corner.boundingRect().width + 24
        cornerPlate.width = cw
        cornerPlate.translation = [m.w - cw - 14, 14]
        corner.translation = [m.w - cw - 2, 14]
    end if
    m.top.findNode("tick").visible = (item.played = true)
    m.top.findNode("tickPlate").visible = (item.played = true)
    if item.badge <> "" and item.played = true then m.top.findNode("tick").visible = false
    if item.badge <> "" and item.played = true then m.top.findNode("tickPlate").visible = false
    if item.badge <> ""
        m.badge.text = item.badge
        bw = m.badge.boundingRect().width + 22
        m.badgePlate.width = bw
        m.badgePlate.height = 34
        m.badgePlate.translation = [12, 12]
        m.badge.translation = [23, 17]
    end if
    m.badge.visible = (item.badge <> "")
    m.badgePlate.visible = (item.badge <> "")
    ' a card handed new content keeps whatever focus look it last had; draw the one it has now
    ' (a grid reloaded while its toolbar had focus kept a ring on its first card)
    onFocus()
end sub

sub onFocus()
    p = m.top.focusPercent
    if not (m.top.rowListHasFocus or m.top.gridHasFocus) then p = 0
    k = metrics()
    grow(1 + (focusZoom() - 1) * p)
    ring = m.top.findNode("ownRing")
    inGrid = (m.top.gridHasFocus and p > 0)
    ring.visible = inGrid
    item = m.top.itemContent
    if inGrid and item <> invalid and item.kind = "person"
        ' a round ring round the face (an artist in a music grid), which sits centred at the card's top
        across = 150 * k.focusScale * 296 / 216
        ring.uri = "pkg:/images/focus-disc.png"
        ring.opacity = p
        ring.width = across
        ring.height = across
        ring.translation = [(m.w - across) / 2, m.person.translation[1] + 75 - across / 2]
        return
    end if
    ring.uri = "pkg:/images/focus-14.9.png"
    if inGrid
        out = k.focusSpread + 6
        w = m.w * focusZoom()
        h = m.h * focusZoom()
        ring.opacity = p
        ring.translation = [(m.w - w) / 2 - out, (m.h - h) / 2 - out]
        ring.width = w + out * 2
        ring.height = h + out * 2
    end if
end sub

sub grow(s as float)
    if m.w = 0 then return
    if m.redraw = invalid then m.redraw = 0
    item = m.top.itemContent
    aside = (item <> invalid and item.overlay = true and item.whole = true and item.art <> "")
    m.under.scale = [s, s]
    m.over.scale = [s, s]
    w = m.w * s
    h = m.h * s
    if item <> invalid and item.under = true
        ' words under a picture stay their size and place (grown, they would slide under the rail or
        ' the next card); only the picture grows, and its progress line with it
        m.over.scale = [1, 1]
        m.bar.translation = [(m.w - w) / 2, (m.h + h) / 2 - 6]
        if item.progress >= 0 then m.bar.width = w * item.progress
    end if
    shiftY = 0
    if m.shiftY <> invalid then shiftY = m.shiftY
    m.clip.translation = [(m.w - w) / 2, (m.h - h) / 2 + shiftY]
    m.clip.maskSize = [w, h + m.redraw]
    m.art.width = w
    m.art.height = h
    m.art.translation = [0, 0]
    for i = 0 to 3
        tile = m.tiles.getChild(i)
        tile.width = w / 2
        tile.height = h / 2
        tile.translation = [(i mod 2) * w / 2, int(i / 2) * h / 2]
    end for
    if aside
        ' a cover on a wide card: whole, at the right, with the title and progress beside it on the plate
        m.art.width = h - 24 * s
        m.art.height = h - 24 * s
        m.art.translation = [w - h + 12 * s, 12 * s]
    end if
    m.shade.visible = (item <> invalid and item.overlay = true and not aside)
    m.shade.width = w
    m.shade.height = h * 0.62
    m.shade.translation = [0, h - h * 0.62]
end sub

' The cut is made a hundredth of a pixel taller or back again: a real change, so it is drawn anew.
sub onShownAgain()
    if m.w = 0 then return
    if m.redraw = 0.01 then m.redraw = 0 else m.redraw = 0.01
    onFocus()
end sub
