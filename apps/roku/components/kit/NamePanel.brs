' Placed as the side sheet is (frames().sheet): words from `rowsX`, the field Search's height, the
' keys under the button, the remote's keys at the foot on `hintsY`.
sub init()
    c = colours()
    f = frames().sheet
    s = frames().search
    m.top.visible = false
    m.top.findNode("scrim").color = c.ink
    panel = m.top.findNode("panel")
    panel.color = c.overlay
    panel.translation = [f.x, 0]
    panel.width = f.width
    eyebrow = m.top.findNode("eyebrow")
    eyebrow.translation = [f.rowsX, f.top]
    eyebrow.width = f.rowsWidth
    setType(eyebrow, "caption", c.textTertiary)
    m.title = m.top.findNode("title")
    m.title.width = f.rowsWidth
    setType(m.title, "title", c.text)
    m.field = m.top.findNode("field")
    m.field.blendColor = c.raised
    m.field.width = f.rowsWidth
    m.field.height = s.boxHeight
    m.typed = m.top.findNode("typed")
    setType(m.typed, "heading", c.text)
    m.typed.width = f.rowsWidth - 48
    m.typed.height = s.boxHeight
    m.caret = m.top.findNode("caret")
    m.caret.color = c.accent
    m.caret.height = 36
    m.finish = m.top.findNode("finish")
    m.finish.observeField("pressed", "onFinish")
    m.keyboard = m.top.findNode("keyboard")
    m.keyboard.observeField("text", "onTyped")
    hints = m.top.findNode("hints")
    hints.hints = [{keys: ["OK"], text: "Type"}, {keys: ["Back"], text: "Close"}]
    hints.translation = [f.rowsX, f.hintsY]
end sub

sub onModel()
    f = frames().sheet
    model = m.top.model
    m.top.findNode("eyebrow").text = model.eyebrow
    m.title.text = model.title
    m.title.translation = [f.rowsX, f.top + 34]
    y = f.top + 34 + m.title.boundingRect().height + 24
    m.field.translation = [f.rowsX, y]
    m.typed.translation = [f.rowsX + 24, y]
    m.caretY = y + (m.field.height - m.caret.height) / 2
    y = y + m.field.height + 24
    m.finish.translation = [f.rowsX, y]
    m.finish.buttons = []
    m.finish.buttons = [{id: "finish", text: model.verb, icon: ""}]
    m.keyboard.translation = [f.rowsX, y + 72 + 32]
    value = ""
    if model.value <> invalid then value = model.value
    m.keyboard.text = value
    onTyped()
    m.top.visible = true
    m.onFinishRow = false
    m.keyboard.active = true
end sub

sub onTyped()
    m.typed.text = m.keyboard.text
    width = m.typed.boundingRect().width
    if width > m.typed.width then width = m.typed.width
    m.caret.translation = [m.typed.translation[0] + width + 4, m.caretY]
end sub

sub onFinish()
    name = m.keyboard.text.trim()
    if name = "" then return        ' nothing typed yet: the button waits
    m.top.visible = false
    m.top.chosen = name
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press then return true
    if key = "back"
        m.top.visible = false
        m.top.chosen = ""
        return true
    end if
    if key = "up" and not m.onFinishRow
        ' Up from the keys' top row: the button
        m.onFinishRow = true
        m.finish.active = true
    else if key = "down" and m.onFinishRow
        m.onFinishRow = false
        m.keyboard.active = true
    end if
    return true     ' the panel holds focus until it closes
end function
