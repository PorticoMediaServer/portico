sub main(args as dynamic)
    screen = createObject("roSGScreen")
    port = createObject("roMessagePort")
    screen.setMessagePort(port)
    scene = screen.createScene("MainScene")
    screen.show()
    scene.launchArgs = args
    scene.observeField("exitApp", port)
    while true
        msg = wait(0, port)
        t = type(msg)
        if t = "roSGScreenEvent"
            if msg.isScreenClosed() then return
        else if t = "roSGNodeEvent"
            if msg.getField() = "exitApp" then return
        end if
    end while
end sub
