' The note's right edge and top line are frames().toast; it is as wide as its words.
sub init()
    c = colours()
    m.top.findNode("plate").blendColor = c.promptPlate
    m.top.findNode("mark").blendColor = c.danger
    setType(m.top.findNode("words"), "bodyStrong", c.text)
    m.top.findNode("timer").observeField("fire", "hide")
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
    words.translation = [x, 0]
    w = x + words.boundingRect().width + 24
    m.top.findNode("plate").width = w
    m.top.findNode("note").translation = [frames().toast.right - w, frames().toast.top]
end sub

sub hide()
    m.top.findNode("note").visible = false
end sub
