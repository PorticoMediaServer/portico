sub init()
    heights = [12, 22, 16]
    for i = 0 to 2
        bar = m.top.createChild("Rectangle")
        bar.width = 5
        bar.height = heights[i]
        bar.translation = [i * 8, 22 - heights[i]]
    end for
    m.top.color = colours().accentStrong
    paint()
end sub

sub paint()
    for i = 0 to m.top.getChildCount() - 1
        m.top.getChild(i).color = m.top.color
    end for
end sub
