sub init()
    m.ring = m.top.findNode("ring")
    m.plate = m.top.findNode("plate")
    m.icon = m.top.findNode("icon")
    m.label = m.top.findNode("label")
    m.track = m.top.findNode("track")
    m.bar = m.top.findNode("bar")
    m.detail = m.top.findNode("detail")
    setType(m.label, "button")
    setType(m.detail, "metaStrong")
end sub

sub onChange()
    c = colours()
    m.label.text = m.top.text
    x = 38
    if m.top.icon <> ""
        m.icon.uri = "pkg:/images/i-" + m.top.icon + "-28.png"
        m.icon.visible = true
        x = 80
        if m.top.text = "" then x = 38
    else
        m.icon.visible = false
    end if
    m.label.translation = [x, 0]
    w = x + m.label.boundingRect().width + 38
    if m.top.text = "" then w = 72 + (x - 38)      ' an icon alone is a square button
    if m.top.text = "" and m.top.icon <> "" then m.icon.translation = [22, 22] else m.icon.translation = [36, 22]
    hasBar = m.top.progress >= 0
    m.track.visible = hasBar
    m.bar.visible = hasBar
    m.detail.visible = (m.top.detail <> "")
    if hasBar
        m.track.translation = [w - 14, 33]
        m.bar.translation = [w - 14, 33]
        m.bar.width = 100 * m.top.progress
        w = w + 100 + 24
    end if
    if m.top.detail <> ""
        m.detail.text = m.top.detail
        m.detail.translation = [w - 24, 0]
        w = w + m.detail.boundingRect().width + 14
    end if
    if m.top.minWidth > w
        ' wider than its words: they move to the middle, the plate takes the whole width
        shift = (m.top.minWidth - w) / 2
        for each part in [m.icon, m.label, m.track, m.bar, m.detail]
            part.translation = [part.translation[0] + shift, part.translation[1]]
        end for
        w = m.top.minWidth
    end if
    m.plate.width = w
    m.top.buttonWidth = w
    if m.top.on
        m.plate.blendColor = c.plateOn
        m.label.color = c.actionInk
        m.icon.blendColor = c.actionInk
        m.detail.color = c.actionInk
        m.track.color = c.trackOn
        m.bar.color = c.accent
    else
        m.detail.color = c.text
        m.track.color = c.track
        m.bar.color = c.accent
        m.plate.blendColor = c.plate
        if m.top.switchedOn then m.plate.blendColor = c.chipOn
        m.label.color = c.text
        m.icon.blendColor = c.text
    end if
    out = metrics().focusSpread + 6
    m.ring.translation = [-out, -out]
    m.ring.width = w + out * 2
    m.ring.height = 72 + out * 2
    m.ring.visible = m.top.on
end sub
