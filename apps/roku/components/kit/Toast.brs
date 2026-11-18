' The note's right edge and top line are frames().toast; it is as wide as its words.
sub init()
    c = colours()
    m.top.findNode("plate").blendColor = c.promptPlate
    m.top.findNode("mark").blendColor = c.danger
    setType(m.top.findNode("words"), "bodyStrong", c.text)
    m.top.findNode("timer").observeField("fire", "hide")
    m.top.observeField("focusedChild", "paintButton")
end sub

sub onShow()
    if not m.top.show
        hide()
        return
    end if
    layout()
    m.top.findNode("note").visible = true
    timer = m.top.findNode("timer")
    timer.duration = m.top.seconds
    timer.control = "stop"
    timer.control = "start"
end sub

' The words and their plate, from the right edge; drawn again if the words change while it shows.
sub layout()
    words = m.top.findNode("words")
    words.text = m.top.text
    x = 24
    if m.top.dot then x = 52
    m.top.findNode("mark").visible = m.top.dot
    h = 64
    act = m.top.findNode("act")
    act.visible = (m.top.action <> "")
    if act.visible then h = 96
    words.height = h
    words.translation = [x, 0]
    w = x + words.boundingRect().width + 24
    if act.visible
        act.text = m.top.action
        act.translation = [w, (h - 72) / 2]
        w = w + act.buttonWidth + 12
    end if
    m.top.findNode("plate").width = w
    m.top.findNode("plate").height = h
    m.top.findNode("note").translation = [frames().toast.right - w, frames().toast.top]
end sub

sub hide()
    if not m.top.findNode("note").visible then return
    m.top.findNode("note").visible = false
    m.top.gone = true
end sub

sub onActive()
    if m.top.active and m.top.findNode("note").visible and m.top.action <> "" then m.top.setFocus(true)
end sub

sub paintButton()
    m.top.findNode("act").on = m.top.hasFocus()
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press or key <> "OK" or not m.top.hasFocus() then return false
    hide()
    m.top.pressed = true
    return true
end function
