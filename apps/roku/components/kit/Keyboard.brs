' Keys are 72 high on an 82 pitch; across, the keyboard's width is shared by its columns (Search:
' six keys of 75 on an 85 pitch). After the characters comes the row of Space (three keys wide),
' delete (one) and Clear (two).
sub init()
    m.keys = m.top.findNode("keys")
    m.ring = m.top.findNode("ring")
    m.row = 0
    m.col = 0
    m.top.observeField("focusedChild", "paint")
    build()
end sub

sub build()
    m.keys.removeChildrenIndex(m.keys.getChildCount(), 0)
    m.letters = m.top.characters
    m.cols = m.top.columns
    m.count = len(m.letters)
    m.rows = int((m.count + m.cols - 1) / m.cols)      ' the row of Space, delete and Clear is m.rows
    pitch = int((m.top.width + 10) / m.cols)
    for i = 0 to m.count - 1
        makeKey(mid(m.letters, i + 1, 1), "", (i mod m.cols) * pitch, int(i / m.cols) * 82, pitch - 10, "keyLetter")
    end for
    y = m.rows * 82
    makeKey("Space", "", 0, y, pitch * 3 - 10, "button")
    makeKey("", "backspace", pitch * 3, y, pitch - 10, "button")
    makeKey("Clear", "", pitch * 4, y, pitch * 2 - 10, "button")
    if m.top.capitals then onCapitals()
    paint()
end sub

sub onCapitals()
    if m.count = invalid then return
    last = 25
    if m.count - 1 < last then last = m.count - 1
    for i = 0 to last
        letter = mid(m.letters, i + 1, 1)
        if m.top.capitals then letter = ucase(letter)
        m.keys.getChild(i).text = letter
    end for
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

' Which cell has focus: the rows of characters, then the three wide keys.
function cellIndex() as integer
    if m.row < m.rows then return m.row * m.cols + m.col
    if m.col <= 2 then return m.count
    if m.col = 3 then return m.count + 1
    return m.count + 2
end function

' A short last row of characters: the column moves in to its last key.
sub keepInRow()
    if m.row < m.rows and m.row * m.cols + m.col >= m.count then m.col = m.count - 1 - m.row * m.cols
end sub

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
        if m.row = m.rows
            if m.col <= 2
                m.col = 3
            else if m.col = 3
                m.col = 4
            else
                return false
            end if
        else if m.col < m.cols - 1 and m.row * m.cols + m.col + 1 < m.count
            m.col = m.col + 1
        else
            return false     ' the last column: the results
        end if
    else if key = "left"
        if m.row = m.rows
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
        if m.row >= m.rows then return false
        m.row = m.row + 1
        keepInRow()
    else if key = "up"
        if m.row = 0 then return false
        m.row = m.row - 1
        keepInRow()
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
        ' the key as this keyboard has it (Search's letters are small, an invitation code's capitals)
        if len(typed) = 1 and instr(1, m.letters, lcase(typed)) > 0 then typed = lcase(typed)
        if len(typed) = 1 and instr(1, m.letters, ucase(typed)) > 0 then typed = ucase(typed)
        if typed = " " or (len(typed) = 1 and instr(1, m.letters, typed) > 0) then m.top.text = m.top.text + typed
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
        if at < m.count
            text = text + mid(m.letters, at + 1, 1)
        else if at = m.count
            if text <> "" and right(text, 1) <> " " then text = text + " "
        else if at = m.count + 1
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
