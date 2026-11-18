sub init()
    c = colours()
    f = frames().dialog
    m.top.findNode("scrim").color = c.ink
    m.top.findNode("panel").blendColor = c.overlay
    m.top.findNode("panel").width = f.width
    m.top.findNode("title").width = f.width - f.pad * 2
    m.top.findNode("body").width = f.width - f.pad * 2
    setType(m.top.findNode("title"), "title", c.text)
    setType(m.top.findNode("body"), "body", c.textSecondary)
    m.buttons = m.top.findNode("buttons")
    m.buttons.observeField("pressed", "onPressed")
end sub

' The panel sits in the middle of the screen, as tall as its words; the buttons end at its right.
sub onModel()
    model = m.top.model
    f = frames().dialog
    title = m.top.findNode("title")
    body = m.top.findNode("body")
    title.text = model.title
    body.text = model.body
    h = f.pad + title.boundingRect().height + 14 + body.boundingRect().height + 44 + 72 + f.pad
    top = (1080 - h) / 2
    m.top.findNode("panel").translation = [f.x, top]
    m.top.findNode("panel").height = h
    title.translation = [f.x + f.pad, top + f.pad]
    body.translation = [f.x + f.pad, top + f.pad + title.boundingRect().height + 14]
    ' emptied first, so focus starts on Cancel every time it opens
    m.buttons.buttons = []
    m.buttons.buttons = [{id: "cancel", text: "Cancel", icon: ""}, {id: "confirm", text: model.verb, icon: "", danger: true}]
    width = m.buttons.getChild(0).buttonWidth + 18 + m.buttons.getChild(1).buttonWidth
    m.buttons.translation = [f.x + f.width - f.pad - width, top + h - f.pad - 72]
    m.top.visible = true
    m.buttons.active = true
end sub

sub onPressed()
    m.top.visible = false
    m.top.chosen = m.buttons.pressed
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if press and key = "back"
        m.top.visible = false
        m.top.chosen = "cancel"
    end if
    return true     ' the dialog holds focus until it closes
end function
