' Keys are 75 x 72 on an 85 x 82 pitch, six to a row; the seventh row is Space (three wide),
' delete (one) and Clear (two).
sub init()
    m.keys = m.top.findNode("keys")
    m.ring = m.top.findNode("ring")
    m.letters = "abcdefghijklmnopqrstuvwxyz1234567890"
    for i = 0 to 35
        makeKey(mid(m.letters, i + 1, 1), "", (i mod 6) * 85, int(i / 6) * 82, 75, "keyLetter")
    end for
    makeKey("Space", "", 0, 492, 245, "button")
    makeKey("", "backspace", 255, 492, 75, "button")
    makeKey("Clear", "", 340, 492, 160, "button")
    m.row = 0
    m.col = 0
    m.top.observeField("focusedChild", "paint")
    paint()
end sub

sub makeKey(text as string, glyph as string, x as integer, y as integer, w as integer, role as string)
    key = m.keys.createChild("Key")
    key.role = role
    key.width = w
    key.height = 72
    key.translation = [x, y]
    key.text = text
    key.glyph = glyph
end sub

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
    has = m.top.hasFocus()
    at = cellIndex()
    for i = 0 to m.keys.getChildCount() - 1
        m.keys.getChild(i).on = (has and i = at)
    end for
    m.ring.visible = has
    out = metrics().focusSpread + 6
    key = m.keys.getChild(at)
    m.ring.translation = [key.translation[0] - out, key.translation[1] - out]
    m.ring.width = key.width + out * 2
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

' A letter sent by the Roku phone app's keyboard (or a script), or its delete key: typed into the
' box. The keyboard's own key presses, or a page's (`typed`) while focus is elsewhere on it.
function typeKey(key as string) as boolean
    if left(key, 4) = "Lit_"
        typed = mid(key, 5)
        if typed = "%20" or typed = " " then typed = " "
        if len(typed) = 1 and instr(1, m.letters + " ", lcase(typed)) > 0 then m.top.text = m.top.text + lcase(typed)
        return true
    else if key = "backspace"
        if len(m.top.text) > 0 then m.top.text = left(m.top.text, len(m.top.text) - 1)
        return true
    end if
    return false
end function

sub onTyped()
    typeKey(m.top.typed)
end sub

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
    else if typeKey(key)
        return true
    else if key = "replay" or key = "rewind"
        if len(m.top.text) > 0 then m.top.text = left(m.top.text, len(m.top.text) - 1)
        return true
    end if
    if key = "up" or key = "down" or key = "left" or key = "right"
        moved = pressKey(key)
        if key = "down" then return true    ' the bottom row stops; Up from the top row goes on to the scene (the now-playing card)
        return moved
    end if
    return false
end function
