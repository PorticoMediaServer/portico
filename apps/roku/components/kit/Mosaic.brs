sub init()
    m.top.findNode("plate").blendColor = colours().raised
end sub

sub draw()
    size = m.top.size
    plate = m.top.findNode("plate")
    plate.width = size
    plate.height = size
    covers = m.top.covers
    if covers = invalid then covers = []
    tiles = m.top.findNode("tiles")
    for i = 0 to 3
        tile = tiles.getChild(i)
        tile.width = size / 2
        tile.height = size / 2
        tile.translation = [(i mod 2) * size / 2, int(i / 2) * size / 2]
        tile.visible = (covers.count() >= 4)
        if tile.visible then tile.uri = covers[i]
    end for
    if covers.count() > 0 and covers.count() < 4
        ' fewer than four: the first cover fills the square
        first = tiles.getChild(0)
        first.visible = true
        first.uri = covers[0]
        first.width = size
        first.height = size
    end if
end sub
