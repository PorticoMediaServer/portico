sub init()
    c = colours()
    f = frames().textPanel
    m.top.visible = false
    m.top.findNode("surface").x = f.x
    x = f.x + f.pad
    width = 1920 - f.padRight - x
    eyebrow = m.top.findNode("eyebrow")
    eyebrow.translation = [x, f.top]
    setType(eyebrow, "metaStrong", c.textSecondary)
    m.title = m.top.findNode("title")
    m.title.width = width
    setType(m.title, "title", c.text)
    m.text = m.top.findNode("text")
    m.text.translation = [x, f.textY]
    m.text.width = width
    m.text.height = f.textBottom - f.textY
    m.text.font = fontFor("synopsis")
    m.text.color = c.textSecondary
    m.text.lineSpacing = typeRoles().synopsis[2] - typeRoles().synopsis[1] * 1.25
    hints = m.top.findNode("hints")
    hints.translation = [x, f.hintsY]
    hints.hints = [{keys: ["Up", "Down"], text: "Scroll"}, {keys: ["Back"], text: "Close"}]
end sub

sub onModel()
    model = m.top.model
    m.top.findNode("eyebrow").text = model.eyebrow
    m.title.text = model.title
    ' the title sits on the bottom of its two-line slot, so the text below never moves
    m.title.translation = [m.text.translation[0], frames().textPanel.textY - frames().textPanel.titleGap - m.title.boundingRect().height]
    m.text.text = model.text
    m.top.visible = true
    m.text.setFocus(true)
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if press and key = "back"
        m.top.visible = false
        m.top.closed = true
    end if
    return true     ' the panel holds focus until it closes
end function
