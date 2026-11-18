' The hero's slots, measured from its top. One set for Home and title pages, so the two read alike:
' the title's last line sits on `titleBase`, whatever its size or number of lines. The meta line and
' the hints are centred on theirs (a LayoutGroup centres what it holds on its own line).
function heroSlots() as object
    return {foldedTop: -22, foldedBottom: 70, foldedWidth: 1600, titleBase: 142, metaMid: 188, synopsisY: 220, hintsMid: 340, creditsY: 306, buttonsY: 364, longWidth: 1400}
end function

sub init()
    c = colours()
    m.slots = heroSlots()
    m.eyebrow = m.top.findNode("eyebrow")
    m.eyebrowMore = m.top.findNode("eyebrowMore")
    m.linkIcon = m.top.findNode("linkIcon")
    m.linkPlate = m.top.findNode("linkPlate")
    m.linkRing = m.top.findNode("linkRing")
    m.title = m.top.findNode("title")
    m.meta = m.top.findNode("meta")
    m.year = m.top.findNode("year")
    m.facts = m.top.findNode("facts")
    m.tagBox = m.top.findNode("tagBox")
    m.tagLine = m.top.findNode("tagLine")
    m.tag = m.top.findNode("tag")
    m.progressBox = m.top.findNode("progressBox")
    m.bar = m.top.findNode("bar")
    m.linkPlate.blendColor = c.raisedBright
    m.linkIcon.blendColor = c.accentStrong
    m.tagLine.blendColor = c.lineStrong
    m.top.findNode("track").color = c.track
    m.bar.color = c.accent
    m.left = m.top.findNode("left")
    m.synopsis = m.top.findNode("synopsis")
    m.hints = m.top.findNode("hints")
    m.credits = m.top.findNode("credits")
    m.buttonRow = m.top.findNode("buttonRow")
    m.buttonRow.flexible = "play"     ' a title page's Play widens; the other buttons keep their slots
    setType(m.eyebrow, "eyebrow", c.accentStrong)
    setType(m.eyebrowMore, "meta", c.text)
    setType(m.year, "metaStrong", c.text)
    setType(m.facts, "meta", c.textSecondary)
    setType(m.tag, "tag", c.text)
    setType(m.left, "metaStrong", c.text)
    setType(m.synopsis, "synopsis", c.textSecondary)
    setType(m.credits, "caption", c.textSecondary)
    m.lockup = fontFor("lockup")
    m.lockupLong = fontFor("lockupLong")
    m.title.color = c.text
    m.meta.translation = [0, m.slots.metaMid]
    m.synopsis.translation = [0, m.slots.synopsisY]
    m.hints.translation = [0, m.slots.hintsMid]
    m.credits.translation = [0, m.slots.creditsY]
    m.buttonRow.translation = [0, m.slots.buttonsY]
    ' the eyebrow's own group holds focus while the link has it (a parent cannot take focus from
    ' its own button row)
    m.linkBox = m.top.findNode("eyebrowBox")
    m.linkBox.focusable = true
    m.onLink = false      ' title mode: the eyebrow link has (or last had) focus, not the action row
    m.linkBox.observeField("focusedChild", "paintLink")
end sub

' The outlines stand where the eyebrow, title, meta line and two lines of synopsis will be.
sub onLoading()
    outline = m.top.findNode("outline")
    if outline.getChildCount() = 0
        s = m.slots
        for each bar in [[0, 4, 300, 24], [0, s.titleBase - 72, 560, 72], [0, s.metaMid - 15, 420, 30], [0, s.synopsisY + 6, 780, 24], [0, s.synopsisY + 41, 600, 24]]
            p = outline.createChild("Poster")
            p.uri = "pkg:/images/round-10.9.png"
            p.blendColor = colours().raisedHigh
            p.translation = [bar[0], bar[1]]
            p.width = bar[2]
            p.height = bar[3]
        end for
    end if
    outline.visible = m.top.loading
    for each id in ["eyebrowBox", "title", "details"]
        m.top.findNode(id).visible = not m.top.loading
    end for
end sub

sub onModel()
    model = m.top.model
    if model = invalid then return
    isTitle = (m.top.mode = "title")
    folded = isTitle and m.top.folded
    m.top.findNode("eyebrowBox").visible = not folded
    m.top.findNode("details").visible = not folded
    drawEyebrow(model)
    if folded then foldTitle(model.title) else fitTitle(model.title)

    m.year.text = model.year
    m.facts.text = model.facts
    m.tag.text = model.tag
    m.left.text = model.left
    showInMeta(m.year, model.year <> "")
    showInMeta(m.facts, model.facts <> "")
    showInMeta(m.tagBox, model.tag <> "")
    if model.tag <> "" then m.tagLine.width = m.tag.boundingRect().width + 26
    showInMeta(m.progressBox, model.progress >= 0)
    if model.progress >= 0 then m.bar.width = 160 * model.progress
    showInMeta(m.left, model.left <> "")
    m.synopsis.text = model.synopsis

    m.hints.visible = not isTitle
    m.hints.hints = [{keys: ["OK"], text: m.top.okText}, {keys: ["*"], text: "More options"}]
    m.credits.visible = isTitle
    m.buttonRow.visible = isTitle
    if isTitle and model.credits <> invalid then m.credits.text = model.credits else m.credits.text = ""
    if m.onLink and not isLink() then onActive()     ' the link went away under focus: the action row has it
end sub

' The eyebrow: plain words, or a link (a film's collection) with its icon and count.
sub drawEyebrow(model as object)
    link = isLink()
    more = ""
    if model.eyebrowMore <> invalid then more = model.eyebrowMore
    m.linkIcon.visible = link
    x = 0
    if link then x = 38
    m.eyebrow.text = model.eyebrow
    ' a collection's name is a label for the link, not the page's title: a very long one ends in an
    ' ellipsis rather than running off the screen (its own page shows it whole)
    m.eyebrow.width = 0
    if m.eyebrow.boundingRect().width > 1300 then m.eyebrow.width = 1300
    m.eyebrow.translation = [x, 0]
    x = x + m.eyebrow.boundingRect().width
    m.eyebrowMore.text = more
    m.eyebrowMore.translation = [x + 14, 0]
    if more <> "" then x = x + 14 + m.eyebrowMore.boundingRect().width
    ' the link's focus: a raised plate with the ring, drawn round the whole line
    out = metrics().focusSpread + 6
    m.linkPlate.translation = [-16, -8]
    m.linkPlate.width = x + 32
    m.linkPlate.height = 50
    m.linkRing.translation = [-16 - out, -8 - out]
    m.linkRing.width = x + 32 + out * 2
    m.linkRing.height = 50 + out * 2
    paintLink()
end sub

function isLink() as boolean
    model = m.top.model
    return m.top.mode = "title" and model <> invalid and model.link = true
end function

' The title is never cut: a title too wide for one line at the lockup size steps down to the long
' size and wraps, as many lines as it needs, with its last line on the same baseline.
sub fitTitle(text as string)
    t = m.title
    t.text = text
    t.font = m.lockup
    t.wrap = false
    t.width = 0
    t.lineSpacing = 0
    size = typeRoles().lockup[1]
    x = -4
    if t.boundingRect().width > frames().title.copyWidth
        size = typeRoles().lockupLong[1]
        x = -2
        t.font = m.lockupLong
        t.width = m.slots.longWidth
        t.wrap = true
        t.maxLines = 0
        t.lineSpacing = size * 1.1 - size * 1.366     ' lines 1.1 apart, as tight as the lockup is drawn
    end if
    ' a Label's box runs 0.3 of its size below the baseline (Manrope's descent)
    t.translation = [x, m.slots.titleBase + size * 0.3 - t.boundingRect().height]
end sub

' Folded, the title is a page title (64/800) at the top of the page; a long one steps down to 44
' and wraps, its last line ending above the section strip, rather than being cut.
sub foldTitle(text as string)
    t = m.title
    setType(t, "pageTitle")
    t.wrap = false
    t.width = 0
    t.text = text
    t.translation = [-3, m.slots.foldedTop]
    if t.boundingRect().width > m.slots.foldedWidth
        setType(t, "title")
        t.width = m.slots.foldedWidth
        t.wrap = true
        t.maxLines = 0
        t.translation = [-2, m.slots.foldedBottom - t.boundingRect().height]
    end if
end sub

' A LayoutGroup lays out what it holds, so a part that is not shown is taken out of it.
sub showInMeta(node as object, show as boolean)
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

' Title mode takes focus where it last was: the action row, or the link above it.
sub onActive()
    if not m.top.active or m.top.mode <> "title" then return
    if m.onLink and isLink()
        m.linkBox.setFocus(true)
        paintLink()
    else
        m.onLink = false
        m.buttonRow.active = true
        paintLink()
    end if
end sub

sub paintLink()
    on = m.linkBox.hasFocus() and isLink()
    m.linkPlate.visible = on
    m.linkRing.visible = on
end sub

function onKeyEvent(key as string, press as boolean) as boolean
    if not press or not isLink() then return false
    if m.linkBox.hasFocus()
        if key = "down"
            m.onLink = false
            m.buttonRow.active = true
            paintLink()
            return true
        else if key = "OK"
            m.top.pressed = "link"
            return true
        end if
        return key = "up"           ' the top of the page
    end if
    if key = "up"
        m.onLink = true
        m.linkBox.setFocus(true)
        paintLink()
        return true
    end if
    return false
end function
