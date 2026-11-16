' Keys are 75 x 72 on an 85 x 82 pitch, six to a row; the seventh row is Space (three wide),
' delete (one) and Clear (two).
sub init()
    c = colours()
    m.keys = m.top.findNode("keys")
    m.ring = m.top.findNode("ring")
    m.letters = "abcdefghijklmnopqrstuvwxyz1234567890"
    m.cells = []
    for i = 0 to 35
        m.cells.push(makeKey(mid(m.letters, i + 1, 1), (i mod 6) * 85, int(i / 6) * 82, 75, "padKey"))
    end for
    m.cells.push(makeKey("Space", 0, 492, 245, "button"))
    m.cells.push(makeKey("⌫", 255, 492, 75, "button"))
    m.cells.push(makeKey("Clear", 340, 492, 160, "button"))
    m.row = 0
    m.col = 0
    m.top.observeField("focusedChild", "paint")
    paint()
end sub

function makeKey(text as string, x as integer, y as integer, w as integer, role as string) as object
    plate = m.keys.createChild("Poster")
    plate.uri = "pkg:/images/round-14.9.png"
    plate.width = w
    plate.height = 72
    plate.translation = [x, y]
    label = m.keys.createChild("Label")
    label.text = text
    label.width = w
    label.height = 72
    label.horizAlign = "center"
    label.vertAlign = "center"
    label.translation = [x, y]
    setType(label, role)
    if role = "padKey" then label.font.size = 34
    icon = invalid
    if text = "⌫"
        ' the delete key is a drawn glyph: the typeface has no such character
        label.text = ""
        icon = m.keys.createChild("Poster")
        icon.uri = "pkg:/images/i-backspace-32.png"
        icon.width = 32
        icon.height = 32
        icon.translation = [x + (w - 32) / 2, y + 20]
    end if
    return {plate: plate, label: label, icon: icon, x: x, y: y, w: w}
end function

' Which cell has focus: rows 0 to 5 are the letters and digits, row 6 the three wide keys.
function cellIndex() as integer
    if m.row < 6 then return m.row * 6 + m.col
    if m.col <= 2 then return 36
    if m.col = 3 then return 37
    return 38
end function

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

sub paint()
    c = colours()
    has = m.top.hasFocus()
    at = cellIndex()
    for i = 0 to m.cells.count() - 1
        cell = m.cells[i]
        if has and i = at
            cell.plate.blendColor = "0xD8E6EEFF"
            cell.label.color = c.actionInk
            if cell.icon <> invalid then cell.icon.blendColor = c.actionInk
        else
            cell.plate.blendColor = "0x10212DE6"
            cell.label.color = c.textSecondary
            if cell.icon <> invalid then cell.icon.blendColor = c.textSecondary
        end if
    end for
    m.ring.visible = has
    out = metrics().focusSpread + 6
    cell = m.cells[at]
    m.ring.translation = [cell.x - out, cell.y - out]
    m.ring.width = cell.w + out * 2
    m.ring.height = 72 + out * 2
end sub

function pressKey(key as string) as boolean
    if key = "right"
        if m.row = 6
            if m.col <= 2
                m.col = 3
            else if m.col = 3
                m.col = 4
            else
                return false
            end if
        else if m.col < 5
            m.col = m.col + 1
        else
            return false     ' the last column: the results
        end if
    else if key = "left"
        if m.row = 6
            if m.col >= 4
                m.col = 3
            else if m.col = 3
                m.col = 0
            else
                return false
            end if
        else if m.col > 0
            m.col = m.col - 1
        else
            return false     ' the first column: the rail
        end if
    else if key = "down"
        if m.row >= 6 then return false
        m.row = m.row + 1
    else if key = "up"
        if m.row = 0 then return false
        m.row = m.row - 1
    else
        return false
    end if
    paint()
    return true
end function

function onKeyEvent(key as string, press as boolean) as boolean
    holdKey(key, press)
    if not press then return false
    if key = "OK"
        at = cellIndex()
        text = m.top.text
        if at < 36
            text = text + mid(m.letters, at + 1, 1)
        else if at = 36
            if text <> "" and right(text, 1) <> " " then text = text + " "
        else if at = 37
            if len(text) > 0 then text = left(text, len(text) - 1)
        else
            text = ""
        end if
        m.top.text = text
        return true
    else if left(key, 4) = "Lit_"
        ' a letter sent by the Roku phone app's keyboard (or a script)
        typed = mid(key, 5)
        if typed = "%20" or typed = " " then typed = " "
        if len(typed) = 1 and instr(1, m.letters + " ", lcase(typed)) > 0 then m.top.text = m.top.text + lcase(typed)
        return true
    else if key = "backspace"
        if len(m.top.text) > 0 then m.top.text = left(m.top.text, len(m.top.text) - 1)
        return true
    else if key = "replay" or key = "rewind"
        if len(m.top.text) > 0 then m.top.text = left(m.top.text, len(m.top.text) - 1)
        return true
    end if
    if key = "up" or key = "down" or key = "left" or key = "right"
        moved = pressKey(key)
        if key = "up" or key = "down" then return true    ' the top and bottom rows stop
        return moved
    end if
    return false
end function
