' The frame's stars: 56 across, 12 apart, after the label and 10 more.
sub init()
    c = colours()
    label = m.top.findNode("label")
    setType(label, "rateLabel", c.textTertiary)
    x = label.boundingRect().width + 10 + 12
    m.discs = []
    for i = 0 to 4
        disc = m.top.findNode("discs").createChild("Group")
        disc.translation = [x + i * (56 + 12), 0]
        plate = disc.createChild("Poster")
        plate.uri = "pkg:/images/disc.png"
        plate.width = 56
        plate.height = 56
        icon = disc.createChild("Poster")
        icon.width = 28
        icon.height = 28
        icon.translation = [14, 14]
        m.discs.push({x: x + i * (56 + 12), plate: plate, icon: icon})
    end for
    m.focused = 0
    m.top.observeField("focusedChild", "paint")
    paint()
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

' Lit up to the rating; the one in focus is the pale plate with the ring.
sub paint()
    c = colours()
    has = m.top.hasFocus()
    ring = m.top.findNode("ring")
    ring.visible = false
    for i = 0 to 4
        disc = m.discs[i]
        lit = (i < m.top.stars)
        if lit then disc.icon.uri = "pkg:/images/i-starFilled-28.png" else disc.icon.uri = "pkg:/images/i-star-28.png"
        if has and i = m.focused
            disc.plate.blendColor = c.plateOn
            disc.icon.blendColor = c.actionInk
            across = 56 * 296 / 216
            ring.width = across
            ring.height = across
            ring.translation = [disc.x + 28 - across / 2, 28 - across / 2]
            ring.visible = true
        else if lit
            disc.plate.blendColor = c.ratedPlate
            disc.icon.blendColor = c.accentStrong
        else
            disc.plate.blendColor = c.glass
            disc.icon.blendColor = c.textSecondary
        end if
    end for
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "right"
        if m.focused < 4 then m.focused = m.focused + 1
        paint()
        return true
    else if key = "left"
        if m.focused = 0 then return false
        m.focused = m.focused - 1
        paint()
        return true
    else if key = "OK"
        m.top.pressed = m.focused + 1
        return true
    end if
    return false
end function
