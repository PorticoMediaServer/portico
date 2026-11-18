' The label sits 30 above the box, the note 10 under it (kit .fld).
sub init()
    c = colours()
    setType(m.top.findNode("name"), "groupLabel", c.textTertiary)
    setType(m.top.findNode("value"), "field", c.text)
    m.top.findNode("name").translation = [4, 0]
    m.top.findNode("box").translation = [0, 30]
    m.top.findNode("edge").translation = [0, 30]
    m.top.findNode("value").translation = [24, 30]
    m.top.findNode("edge").blendColor = c.accentStrong
    m.top.findNode("caret").color = c.accentStrong
    onChange()
end sub

sub onChange()
    c = colours()
    w = m.top.fieldWidth
    m.top.findNode("name").text = m.top.label
    plate = m.top.findNode("box")
    plate.width = w
    m.top.findNode("edge").width = w
    value = m.top.findNode("value")
    value.width = w - 48
    if m.top.text <> ""
        value.text = m.top.text
        value.color = c.text
    else
        value.text = m.top.placeholder
        value.color = c.textTertiary
    end if
    if m.top.on then plate.blendColor = c.glassOn else plate.blendColor = c.glass
    ring = m.top.findNode("ring")
    out = metrics().focusSpread + 6
    ring.translation = [-out, 30 - out]
    ring.width = w + out * 2
    ring.height = 72 + out * 2
    ring.visible = m.top.on
    m.top.findNode("edge").visible = m.top.typing and not m.top.on
    caret = m.top.findNode("caret")
    caret.visible = m.top.typing
    ' the caret follows what is typed (a label measures its words only while it has no width)
    typed = 0
    if m.top.text <> ""
        value.width = 0
        typed = value.boundingRect().width
        if typed > w - 48 then typed = w - 48
        value.width = w - 48
    end if
    caret.translation = [24 + typed + 4, 30 + 17]
    under = m.top.findNode("under")
    under.text = m.top.note
    under.width = w
    under.translation = [4, 30 + 72 + 10]
    if m.top.refused then setType(under, "help", c.danger) else setType(under, "help", c.textTertiary)
end sub
