sub init()
    m.top.layoutDirection = "horiz"
    m.top.vertAlignment = "center"
end sub

' 12 between a hint's keys and its words, 68 before the next hint (44 when small).
sub onHints()
    c = colours()
    apart = 68
    if m.top.small then apart = 44
    m.top.removeChildrenIndex(m.top.getChildCount(), 0)
    spacings = []
    for each hint in m.top.hints
        if spacings.count() > 0 then spacings[spacings.count() - 1] = apart
        for each key in hint.keys
            cap = m.top.createChild("Group")
            label = createObject("roSGNode", "Label")
            label.text = key
            setType(label, "keycap", c.text)
            w = label.boundingRect().width + 24
            if w < 46 then w = 46
            outline = cap.createChild("Poster")
            outline.uri = "pkg:/images/line-10.9.png"
            outline.blendColor = c.lineStrong
            outline.width = w
            outline.height = 36
            label.width = w
            label.height = 36
            label.horizAlign = "center"
            label.vertAlign = "center"
            cap.appendChild(label)
            spacings.push(12)
        end for
        words = m.top.createChild("Label")
        words.text = hint.text
        if m.top.small then setType(words, "caption", c.textTertiary) else setType(words, "meta", c.textSecondary)
        spacings.push(12)
    end for
    m.top.itemSpacings = spacings
end sub
