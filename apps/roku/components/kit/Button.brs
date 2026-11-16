sub init()
    m.ring = m.top.findNode("ring")
    m.plate = m.top.findNode("plate")
    m.icon = m.top.findNode("icon")
    m.label = m.top.findNode("label")
    setType(m.label, "button")
end sub

sub onChange()
    c = colours()
    m.label.text = m.top.text
    x = 38
    if m.top.icon <> ""
        m.icon.uri = "pkg:/images/i-" + m.top.icon + "-28.png"
        m.icon.visible = true
        x = 80
    else
        m.icon.visible = false
    end if
    m.label.translation = [x, 0]
    w = x + m.label.boundingRect().width + 38
    m.plate.width = w
    m.top.buttonWidth = w
    if m.top.on
        m.plate.blendColor = "0xD8E6EEFF"
        m.label.color = c.actionInk
        m.icon.blendColor = c.actionInk
    else
        m.plate.blendColor = "0x1D3848E6"
        m.label.color = c.text
        m.icon.blendColor = c.text
    end if
    out = metrics().focusSpread + 6
    m.ring.translation = [-out, -out]
    m.ring.width = w + out * 2
    m.ring.height = 72 + out * 2
    m.ring.visible = m.top.on
end sub
