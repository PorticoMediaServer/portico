sub init()
    m.nodes = []
    m.focused = 0
    m.places = {}        ' where each button stood the last time the row was drawn
    m.top.observeField("focusedChild", "paint")
end sub

sub onButtons()
    ' the button in focus keeps it when the row is redrawn (a label that flips must not move focus)
    kept = ""
    if m.ids <> invalid and m.focused < m.ids.count() then kept = m.ids[m.focused]
    m.ids = []
    for each spec in m.top.buttons
        m.ids.push(spec.id)
    end for
    for i = 0 to m.ids.count() - 1
        if m.ids[i] = kept then m.focused = i
    end for
    m.top.removeChildrenIndex(m.top.getChildCount(), 0)
    m.nodes = []
    for each spec in m.top.buttons
        b = m.top.createChild("Button")
        if spec.progress <> invalid then b.progress = spec.progress
        if spec.detail <> invalid then b.detail = spec.detail
        b.danger = (spec.danger = true)
        b.icon = spec.icon
        b.text = spec.text
        m.nodes.push(b)
    end for
    widenFlexible()
    x = 0
    places = {}
    for i = 0 to m.nodes.count() - 1
        m.nodes[i].translation = [x, 0]
        places[m.ids[i]] = x
        x = x + m.nodes[i].buttonWidth + 18
    end for
    m.places = places
    if m.focused >= m.nodes.count() then m.focused = 0
    paint()
end sub

' Rule 12: the flexible button takes up what a neighbour left, so the first button after it that
' was drawn before lands where it stood. A flexible button too wide for that keeps its own width
' (the row then grows: a title read again after playing, never a change made from the row).
sub widenFlexible()
    flex = -1
    for i = 0 to m.ids.count() - 1
        if m.ids[i] = m.top.flexible then flex = i
    end for
    if flex < 0 then return
    x = 0
    for i = 0 to flex - 1
        x = x + m.nodes[i].buttonWidth + 18
    end for
    between = 0
    for i = flex + 1 to m.ids.count() - 1
        if m.places[m.ids[i]] <> invalid
            width = m.places[m.ids[i]] - x - between - 18
            if width > m.nodes[flex].buttonWidth then m.nodes[flex].minWidth = width
            return
        end if
        between = between + m.nodes[i].buttonWidth + 18
    end for
end sub

sub onActive()
    if m.top.active then m.top.setFocus(true)
    paint()
end sub

sub paint()
    has = m.top.hasFocus()
    for i = 0 to m.nodes.count() - 1
        m.nodes[i].on = (has and i = m.focused)
    end for
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return false
    if key = "right"
        if m.focused < m.nodes.count() - 1 then m.focused = m.focused + 1
        paint()
        return true   ' the end of the row: nothing moves
    else if key = "left"
        if m.focused = 0 then return false   ' the page's left edge
        m.focused = m.focused - 1
        paint()
        return true
    else if key = "OK"
        if m.nodes.count() > 0 then m.top.pressed = m.top.buttons[m.focused].id
        return true
    end if
    return false
end function
