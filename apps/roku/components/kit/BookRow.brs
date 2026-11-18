sub init()
    c = colours()
    setType(m.top.findNode("number"), "groupLabel", c.accentStrong)
    setType(m.top.findNode("title"), "rowHeading", c.text)
    setType(m.top.findNode("sub"), "factLabel", c.textTertiary)
    setType(m.top.findNode("place"), "bookPlace", c.accentStrong)
    setType(m.top.findNode("length"), "meta", c.textSecondary)
    m.top.findNode("coverBack").blendColor = c.raised
    m.top.findNode("plate").blendColor = c.raisedBright
    ' the place on the cover, as on a card's picture
    m.top.findNode("track").color = c.trackOnArt
    m.top.findNode("bar").color = c.accentStrong
end sub

' The frame's grid: the cover's column (140), the words, the length's column (200), 32 between them,
' 20 before the cover and 26 after the length.
sub layout()
    model = m.top.model
    w = m.top.rowWidth
    h = m.top.rowHeight
    coverY = (h - 128) / 2
    m.top.findNode("coverBack").translation = [20, coverY]
    m.top.findNode("coverClip").translation = [20, coverY]
    m.top.findNode("cover").uri = ""
    if model.art <> invalid then m.top.findNode("cover").uri = model.art
    track = m.top.findNode("track")
    bar = m.top.findNode("bar")
    track.visible = (model.progress <> invalid)
    bar.visible = track.visible
    track.width = 112
    track.translation = [28, coverY + 128 - 14]
    bar.translation = track.translation
    if track.visible then bar.width = 112 * model.progress
    ' the words, stacked and set on the row's middle: "Book 1", the title, who read it, your place
    x = 20 + 140 + 32
    width = w - x - 32 - 200 - 26
    lines = []
    for each part in [["number", 24, 4], ["title", 38, 0], ["sub", 27, 5], ["place", 26, 12]]
        label = m.top.findNode(part[0])
        label.text = ""
        if model[part[0]] <> invalid then label.text = model[part[0]]
        label.width = width
        if label.text <> "" then lines.push({label: label, height: part[1], gap: part[2]})
    end for
    total = 0
    for i = 0 to lines.count() - 1
        total = total + lines[i].height
        if i > 0 then total = total + lines[i].gap
    end for
    y = (h - total) / 2
    for i = 0 to lines.count() - 1
        if i > 0 then y = y + lines[i].gap
        lines[i].label.translation = [x, y]
        y = y + lines[i].height
    end for
    length = m.top.findNode("length")
    length.text = ""
    if model.length <> invalid then length.text = model.length
    length.translation = [w - 26 - 200, h / 2 - 14]
    paint()
end sub

sub paint()
    w = m.top.rowWidth
    h = m.top.rowHeight
    on = m.top.on
    plate = m.top.findNode("plate")
    plate.width = w
    plate.height = h
    plate.visible = on
    ring = m.top.findNode("ring")
    out = metrics().focusSpread + 6
    ring.translation = [-out, -out]
    ring.width = w + out * 2
    ring.height = h + out * 2
    ring.visible = on
end sub
