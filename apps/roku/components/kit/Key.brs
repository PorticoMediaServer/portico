sub init()
    m.plate = m.top.findNode("plate")
    m.label = m.top.findNode("label")
    m.icon = m.top.findNode("icon")
    onChange()
end sub

sub onChange()
    w = m.top.width
    h = m.top.height
    m.plate.width = w
    m.plate.height = h
    m.label.width = w
    m.label.height = h
    setType(m.label, m.top.role)
    hasGlyph = (m.top.glyph <> "")
    m.icon.visible = hasGlyph
    if hasGlyph
        m.label.text = ""
        m.icon.uri = "pkg:/images/i-" + m.top.glyph + "-32.png"
        size = 32
        if h > 100 then size = 40      ' the number pad's tall keys
        m.icon.width = size
        m.icon.height = size
        m.icon.translation = [(w - size) / 2, (h - size) / 2]
    else
        m.label.text = m.top.text
    end if
    onPaint()
end sub

' The key in focus is lifted a shade and its text turns white (auth-04, tv-focus-22); the ring
' round it is the pad's.
sub onPaint()
    c = colours()
    if m.top.on
        m.plate.blendColor = c.raisedBright
        m.label.color = c.text
        m.icon.blendColor = c.text
    else
        m.plate.blendColor = c.key
        m.label.color = c.textSecondary
        m.icon.blendColor = c.textSecondary
    end if
end sub
