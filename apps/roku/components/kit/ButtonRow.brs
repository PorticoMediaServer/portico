sub init()
    m.nodes = []
    m.focused = 0
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
    x = 0
    for each spec in m.top.buttons
        b = m.top.createChild("Button")
        if spec.progress <> invalid then b.progress = spec.progress
        if spec.detail <> invalid then b.detail = spec.detail
        b.danger = (spec.danger = true)
        b.icon = spec.icon
        b.text = spec.text
        b.translation = [x, 0]
        x = x + b.buttonWidth + 18
        m.nodes.push(b)
    end for
    if m.focused >= m.nodes.count() then m.focused = 0
    paint()
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
