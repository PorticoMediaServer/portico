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
    m.shade = m.top.findNode("shade")
    m.title = m.top.findNode("title")
    m.sub = m.top.findNode("sub")
    m.track = m.top.findNode("track")
    m.bar = m.top.findNode("bar")
    m.badgePlate = m.top.findNode("badgePlate")
    m.badge = m.top.findNode("badge")
    c = colours()
    m.person = m.top.findNode("person")
    setType(m.top.findNode("faceInitials"), "personInitials", c.textSecondary)
    setType(m.top.findNode("faceName"), "cardTitle", c.text)
    setType(m.top.findNode("facePart"), "cardSubtitle", c.textTertiary)
    setType(m.title, "wideTitle", c.text)
    setType(m.sub, "cardSubtitle", c.textSecondary)
    setType(m.badge, "badge", c.actionInk)
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
        if name.boundingRect().height > 40 then part.translation = [-10, 222] else part.translation = [-10, 194]
        return
    end if
    ' A cover is shown whole, in its own shape; other art fills the card.
    overlay = item.overlay
    if item.art = ""
        ' no picture: the generated cover, with the title on it unless the card already words it
        m.art.loadDisplayMode = "noScale"
        m.art.uri = "pkg:/images/cover-" + coverTint(item.title).toStr() + ".jpg"
        if not overlay
            m.madeTitle.text = item.title
            m.madeGlyph.text = ucase(left(item.title, 1))
        end if
    else
        if item.whole then m.art.loadDisplayMode = "scaleToFit" else m.art.loadDisplayMode = "scaleToZoom"
        m.art.uri = item.art
    end if
    m.made.visible = (item.art = "" and not overlay)
    m.plate.visible = (item.art <> "" and (overlay or not item.whole))
    m.shade.visible = overlay
    m.title.visible = overlay
    m.sub.visible = overlay
    hasBar = overlay and item.progress >= 0   ' progress is drawn on wide cards only, as the mock-ups have it
    m.track.visible = hasBar
    m.bar.visible = hasBar
    if overlay
        m.title.text = item.title
        m.sub.text = item.sub
    end if
    if hasBar then m.bar.width = (m.w - 40) * item.progress
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
end sub

sub onFocus()
    p = m.top.focusPercent
    if not (m.top.rowListHasFocus or m.top.gridHasFocus) then p = 0
    grow(1 + (metrics().focusScale - 1) * p)
end sub

sub grow(s as float)
    if m.w = 0 then return
    m.under.scale = [s, s]
    m.over.scale = [s, s]
    w = m.w * s
    h = m.h * s
    m.clip.translation = [(m.w - w) / 2, (m.h - h) / 2]
    m.clip.maskSize = [w, h]
    m.art.width = w
    m.art.height = h
    m.shade.width = w
    m.shade.height = h * 0.62
    m.shade.translation = [0, h - h * 0.62]
end sub
