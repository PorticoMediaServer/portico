' Laid out from frames().savedView: the words `pad` inside, covers `cover` wide along the foot.
sub init()
    c = colours()
    f = frames().savedView
    m.f = f
    m.plate = m.top.findNode("plate")
    m.plate.width = f.width
    m.plate.height = f.height
    m.top.findNode("tile").blendColor = c.raisedBright
    m.top.findNode("tile").translation = [f.pad, f.pad]
    m.top.findNode("icon").blendColor = c.textSecondary
    m.top.findNode("icon").translation = [f.pad + 12, f.pad + 12]
    name = m.top.findNode("name")
    setType(name, "rowHeading", c.text)
    name.width = f.width - f.pad * 2 - 72
    name.height = 52
    name.vertAlign = "center"
    name.translation = [f.pad + 72, f.pad]
    setType(m.top.findNode("library"), "metaStrong", c.accentStrong)
    m.top.findNode("library").translation = [f.pad, f.pad + 72]
    words = m.top.findNode("words")
    setType(words, "body", c.textSecondary)
    words.width = f.width - f.pad * 2
    words.translation = [f.pad, f.pad + 108]
    found = m.top.findNode("found")
    setType(found, "metaStrong", c.text)
    found.width = 260
    found.translation = [f.width - f.pad - 260, f.height - f.pad - 30]
    ring = m.top.findNode("ring")
    out = metrics().focusSpread + 6
    ring.translation = [-out, -out]
    ring.width = f.width + out * 2
    ring.height = f.height + out * 2
end sub

sub onContent()
    item = m.top.itemContent
    if item = invalid then return
    f = m.f
    m.top.findNode("name").text = item.title
    m.top.findNode("library").text = item.library
    m.top.findNode("words").text = item.words
    m.top.findNode("found").text = item.found
    covers = m.top.findNode("covers")
    covers.removeChildrenIndex(covers.getChildCount(), 0)
    x = f.pad
    for each uri in item.covers
        poster = covers.createChild("Poster")
        poster.uri = uri
        poster.loadDisplayMode = "scaleToZoom"
        poster.width = f.cover
        poster.height = int(f.cover * 1.5)
        poster.translation = [x, f.height - f.pad - poster.height]
        x = x + f.cover + 12
    end for
    onFocus()
end sub

sub onFocus()
    c = colours()
    on = (m.top.gridHasFocus and m.top.focusPercent > 0.5)
    m.top.findNode("ring").visible = on
    if on then m.plate.blendColor = c.raisedBright else m.plate.blendColor = c.raised
end sub
