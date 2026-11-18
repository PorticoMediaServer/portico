sub init()
    m.heading = m.top.findNode("heading")
    m.subheading = m.top.findNode("subheading")
    m.list = m.top.findNode("list")
    m.ring = m.top.findNode("ring")
    c = colours()
    setType(m.heading, "rowHeading", c.text)
    setType(m.subheading, "rowSubtitle", c.textTertiary)
    m.list.observeField("rowItemFocused", "onItemFocused")
    m.list.observeField("rowItemSelected", "onItemSelected")
    m.top.observeField("focusedChild", "onFocusChain")
    onShape()
end sub

sub onShape()
    g = shelfMetrics(m.top.shape)
    size = g.card
    k = metrics()
    m.list.translation = [0, g.headingGap - k.cardPad]
    m.list.itemSize = [1920, size[1] + k.cardPad * 2]
    m.list.rowItemSize = [[size[0], size[1] + k.cardPad * 2]]
    m.list.rowItemSpacing = [[k.cardGap, 0]]
    m.list.focusXOffset = [m.top.slotX]
    ' Nothing is drawn left of the focus slot: the cards a row has slid past are cut just inside
    ' the gap before it (the card in focus grows by less than that gap), so none shows under the rail.
    listY = g.headingGap - k.cardPad
    m.top.findNode("window").clippingRect = [m.top.slotX - k.cardGap + 2, listY - 40, 1920, size[1] + k.cardPad * 2 + 80]
    ' The ring goes round the grown card, its inner edge two pixels clear of the picture.
    grow = (k.focusScale - 1) / 2
    out = k.focusSpread + 6
    pictureHeight = size[1]
    if m.top.shape = "episode" then pictureHeight = size[0] * 9 / 16     ' the ring goes round the still, not its caption
    if m.top.shape = "gridPoster" then pictureHeight = 300
    m.ring.translation = [m.top.slotX - size[0] * grow - out, g.headingGap - pictureHeight * grow - out]
    m.ring.width = size[0] * k.focusScale + out * 2
    m.ring.height = pictureHeight * k.focusScale + out * 2
    m.ring.uri = "pkg:/images/focus-14.9.png"
    if m.top.shape = "person"
        ' a round ring round the face, which sits centred at the top of its card
        face = 150 * k.focusScale
        across = face * 296 / 216
        m.ring.uri = "pkg:/images/focus-disc.png"
        m.ring.width = across
        m.ring.height = across
        m.ring.translation = [m.top.slotX + size[0] / 2 - across / 2, g.headingGap + 75 - across / 2]
    end if
end sub

sub onHeading()
    m.heading.text = m.top.heading
    m.subheading.text = m.top.subheading
    m.heading.translation = [m.top.slotX, 0]
    m.subheading.translation = [m.top.slotX + m.heading.boundingRect().width + 18, 13]
end sub

sub onCards()
    root = createObject("roSGNode", "ContentNode")
    root.appendChild(m.top.cards)
    m.list.content = root
end sub

sub onJump()
    m.list.jumpToRowItem = [0, m.top.jumpTo]
end sub

sub onActive()
    if m.top.active then m.list.setFocus(true)
end sub

sub onFocusChain()
    m.ring.visible = m.top.isInFocusChain()
end sub

sub onItemFocused(event as object)
    m.top.focusedIndex = event.getData()[1]
end sub

sub onItemSelected(event as object)
    m.top.selectedIndex = event.getData()[1]
end sub
