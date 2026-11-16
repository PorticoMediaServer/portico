' Keys are 148 x 118 on a 168 x 138 pitch: 1 2 3 / 4 5 6 / 7 8 9 / delete 0, then the extra button.
sub init()
    m.boxes = m.top.findNode("boxes")
    m.keys = m.top.findNode("keys")
    m.ring = m.top.findNode("ring")
    m.clear = m.top.findNode("clear")
    m.clear.observeField("fire", "onClear")
    m.value = ""
    m.col = 0
    m.row = 0
    m.top.observeField("focusedChild", "paint")
    reset()
end sub

sub reset()
    c = colours()
    m.value = ""
    m.keys.removeChildrenIndex(m.keys.getChildCount(), 0)
    labels = ["1", "2", "3", "4", "5", "6", "7", "8", "9", "⌫", "0"]
    for i = 0 to labels.count() - 1
        plate = m.keys.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        plate.blendColor = "0x10212DE6"
        plate.width = 148
        plate.height = 118
        plate.translation = [(i mod 3) * 168, int(i / 3) * 138]
        text = m.keys.createChild("Label")
        text.width = 148
        text.height = 118
        text.horizAlign = "center"
        text.vertAlign = "center"
        text.translation = plate.translation
        text.text = labels[i]
        setType(text, "padKey", c.textSecondary)
        if labels[i] = "⌫"
            ' the delete key is a drawn glyph: the typeface has no such character
            text.text = ""
            glyph = m.keys.createChild("Poster")
            glyph.uri = "pkg:/images/i-backspace-32.png"
            glyph.width = 40
            glyph.height = 40
            glyph.blendColor = c.textSecondary
            glyph.translation = [plate.translation[0] + 54, plate.translation[1] + 39]
        end if
    end for
    if m.top.extra <> ""
        plate = m.keys.createChild("Poster")
        plate.uri = "pkg:/images/round-14.9.png"
        plate.blendColor = "0x10212DE6"
        plate.width = 484
        plate.height = 72
        plate.translation = [0, 572]
        text = m.keys.createChild("Label")
        text.width = 484
        text.height = 72
        text.horizAlign = "center"
        text.vertAlign = "center"
        text.translation = [0, 572]
        text.text = m.top.extra
        setType(text, "button", c.textSecondary)
    end if
    m.boxes.removeChildrenIndex(m.boxes.getChildCount(), 0)
    m.boxes.translation = m.top.boxesAt
    for i = 0 to m.top.digits - 1
        frame = m.boxes.createChild("Poster")
        frame.uri = "pkg:/images/line-14.9.png"
        frame.width = 104
        frame.height = 116
        frame.translation = [i * 122, 0]
        dot = m.boxes.createChild("Poster")
        dot.uri = "pkg:/images/round-10.9.png"
        dot.width = 24
        dot.height = 24
        dot.translation = [i * 122 + 40, 46]
    end for
    paint()
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

sub paint()
    c = colours()
    for i = 0 to m.top.digits - 1
        frame = m.boxes.getChild(i * 2)
        dot = m.boxes.getChild(i * 2 + 1)
        if frame <> invalid
            tint = c.line
            if i = len(m.value) then tint = c.accent
            if m.bad = true then tint = c.danger
            frame.blendColor = tint
            dot.visible = i < len(m.value)
            if m.bad = true then dot.blendColor = c.danger else dot.blendColor = c.textSecondary
        end if
    end for
    m.ring.visible = m.top.hasFocus()
    out = metrics().focusSpread + 6
    if m.row = 4
        m.ring.translation = [-out, 572 - out]
        m.ring.width = 484 + out * 2
        m.ring.height = 72 + out * 2
    else
        m.ring.translation = [m.col * 168 - out, m.row * 138 - out]
        m.ring.width = 148 + out * 2
        m.ring.height = 118 + out * 2
    end if
end sub

sub onWrong()
    if not m.top.wrong then return
    m.bad = true
    paint()
    m.clear.control = "start"
end sub

sub onClear()
    m.bad = false
    m.value = ""
    paint()
end sub

sub erase()
    if len(m.value) > 0 then m.value = left(m.value, len(m.value) - 1)
    paint()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    hasExtra = (m.top.extra <> "")
    if key = "right"
        if m.row < 4 and m.col < 2 and not (m.row = 3 and m.col = 1) then m.col = m.col + 1
    else if key = "left"
        if m.row < 4 and m.col > 0 then m.col = m.col - 1
    else if key = "down"
        if m.row < 3 and not (m.row = 2 and m.col = 2) then m.row = m.row + 1 else if (m.row = 3 or (m.row = 2 and m.col = 2)) and hasExtra then m.row = 4
    else if key = "up"
        if m.row = 4
            m.row = 3
            if m.col = 2 then m.row = 2
        else if m.row > 0
            m.row = m.row - 1
        end if
    else if key = "play" or key = "replay"
        erase()
    else if key = "OK"
        if m.bad = true then return true
        if m.row = 4
            m.top.extraPressed = true
        else if m.row = 3 and m.col = 0
            erase()
        else
            digit = "0"
            if m.row < 3 then digit = (m.row * 3 + m.col + 1).toStr()
            if len(m.value) < m.top.digits then m.value = m.value + digit
            paint()
            if len(m.value) = m.top.digits then m.top.entered = m.value
        end if
    else
        return false
    end if
    paint()
    return true
end function
