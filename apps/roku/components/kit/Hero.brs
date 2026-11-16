sub init()
    c = colours()
    m.eyebrow = m.top.findNode("eyebrow")
    m.title = m.top.findNode("title")
    m.meta = m.top.findNode("meta")
    m.year = m.top.findNode("year")
    m.facts = m.top.findNode("facts")
    m.tagBox = m.top.findNode("tagBox")
    m.tagLine = m.top.findNode("tagLine")
    m.tag = m.top.findNode("tag")
    m.progressBox = m.top.findNode("progressBox")
    m.bar = m.top.findNode("bar")
    m.left = m.top.findNode("left")
    m.synopsis = m.top.findNode("synopsis")
    m.hints = m.top.findNode("hints")
    m.okText = m.top.findNode("okText")
    m.buttonRow = m.top.findNode("buttonRow")
    m.dots = m.top.findNode("dots")
    setType(m.eyebrow, "eyebrow", c.accentStrong)
    setType(m.year, "metaStrong", c.text)
    setType(m.facts, "meta", c.textSecondary)
    setType(m.tag, "tag", c.text)
    setType(m.left, "metaStrong", c.text)
    setType(m.synopsis, "synopsis", c.textSecondary)
    setType(m.okText, "meta", c.textSecondary)
    setType(m.top.findNode("starText"), "meta", c.textSecondary)
    setType(m.top.findNode("okKeyText"), "keycap", c.text)
    setType(m.top.findNode("starKeyText"), "keycap", c.text)
    m.lockup = fontFor("lockup")
    m.lockupLong = fontFor("lockupLong")
    m.title.color = c.text
end sub

sub onModel()
    model = m.top.model
    if model = invalid then return
    spotlight = (m.top.mode = "spotlight")
    y = 150
    if spotlight then y = 222
    m.eyebrow.text = model.eyebrow
    m.eyebrow.translation = [0, y]
    if model.eyebrow <> "" then y = y + 44

    ' The title is never cut on one line: it steps down a size and wraps to a second line first.
    m.title.font = m.lockup
    m.title.wrap = false
    m.title.width = 0
    m.title.text = model.title
    titleHeight = 158
    if m.title.boundingRect().width > 1100
        m.title.font = m.lockupLong
        m.title.wrap = true
        m.title.width = 1100
        m.title.lineSpacing = -14
        if m.title.boundingRect().height > 160 then titleHeight = 214 else titleHeight = 122
    else
        m.title.width = 1100
    end if
    m.title.translation = [-4, y - 14]
    y = y + titleHeight

    m.year.text = model.year
    m.facts.text = model.facts
    m.tag.text = model.tag
    m.left.text = model.left
    showOnly(m.year, model.year <> "")
    showOnly(m.facts, model.facts <> "")
    showOnly(m.tagBox, model.tag <> "")
    if model.tag <> "" then m.tagLine.width = m.tag.boundingRect().width + 26
    showOnly(m.progressBox, model.progress >= 0)
    if model.progress >= 0 then m.bar.width = 160 * model.progress
    showOnly(m.left, model.left <> "")
    m.meta.translation = [0, y]
    y = y + 54

    m.synopsis.text = model.synopsis
    m.synopsis.translation = [0, y]
    if model.synopsis <> "" then y = y + 84

    m.hints.visible = not spotlight
    m.buttonRow.visible = spotlight
    m.dots.visible = spotlight
    if spotlight
        m.buttonRow.translation = [0, y + 14]
        m.dots.translation = [0, y + 118]
    else
        m.okText.text = m.top.okText
        m.hints.translation = [0, y + 16]
    end if
end sub

' A LayoutGroup lays out what it holds, so a part that is not shown is taken out of it.
sub showOnly(node as object, show as boolean)
    inside = (node.getParent() <> invalid)
    if show and not inside
        ' keep the drawn order: year, facts, tag, progress, left
        order = [m.year, m.facts, m.tagBox, m.progressBox, m.left]
        at = 0
        for each other in order
            if other.isSameNode(node) then exit for
            if other.getParent() <> invalid then at = at + 1
        end for
        m.meta.insertChild(node, at)
    else if not show and inside
        m.meta.removeChild(node)
    end if
end sub

sub onDots()
    m.dots.removeChildrenIndex(m.dots.getChildCount(), 0)
    if m.top.pages < 2 then return
    x = 0
    for i = 0 to m.top.pages - 1
        dot = m.dots.createChild("Poster")
        dot.uri = "pkg:/images/round-6.9.png"
        dot.height = 8
        if i = m.top.page
            dot.width = 34
            dot.blendColor = "0xEDF5FAFF"
        else
            dot.width = 8
            dot.blendColor = "0xEDF5FA59"
        end if
        dot.translation = [x, 0]
        x = x + dot.width + 12
    end for
end sub

sub onActive()
    if m.top.active then m.buttonRow.active = true
end sub
